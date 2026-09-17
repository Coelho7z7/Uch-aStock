package services

import (
	database "uchoastock/backend/database"
	"uchoastock/backend/utils"
)

// ReportFilter é o recorte do relatório: a obra escolhida no seletor do
// topo (SiteID 0 = todas) e o período, em AAAA-MM-DD, inclusivo nas duas
// pontas. Quem chama já validou o formato das datas; data vazia significa
// "sem limite" naquela ponta.
type ReportFilter struct {
	SiteID int
	From   string
	To     string
}

// ReportSummary são os números do topo do relatório, todos contagens.
//
// Não existe "total de quantidade movimentada": cada material tem a sua
// unidade (saco, m³, barra), e somar tudo daria um número sem sentido —
// o mesmo motivo que tirou esse número do dashboard. Quantidade só é
// somada por material, em MaterialConsumption, onde a unidade é uma só.
type ReportSummary struct {
	// Entries e Exits são quantas entradas e quantas saídas aconteceram.
	Entries int
	Exits   int
	// Materials é quantos materiais diferentes se mexeram no período.
	Materials int
}

// reportConditions monta as condições comuns às consultas do relatório e
// os valores dos placeholders, na ordem. Volta sem a palavra WHERE porque
// GetSiteConsumption usa as mesmas condições no ON de um LEFT JOIN.
//
// Só entrada e saída entram: a atualização de cadastro não acontece em
// obra nenhuma e a quantidade dela não é material que entrou ou saiu.
//
// Só pedaços fixos de SQL são concatenados; todo valor digitado entra por
// placeholder "?".
func reportConditions(filter ReportFilter) (string, []any) {
	conditions := `m.tipo IN ('ENTRADA', 'SAIDA')`
	var args []any

	if filter.SiteID > 0 {
		conditions += ` AND m.obra_id = ?`
		args = append(args, filter.SiteID)
	}
	// 'localtime' pelo mesmo motivo de GetMovementsFilteredWeb: a data é
	// gravada em UTC, e o dia que importa é o do Brasil.
	if filter.From != "" {
		conditions += ` AND date(m.data, 'localtime') >= ?`
		args = append(args, filter.From)
	}
	if filter.To != "" {
		conditions += ` AND date(m.data, 'localtime') <= ?`
		args = append(args, filter.To)
	}

	return conditions, args
}

// GetReportSummary conta entradas, saídas e materiais distintos no
// recorte do filtro.
func GetReportSummary(filter ReportFilter) (ReportSummary, error) {
	conditions, args := reportConditions(filter)

	var summary ReportSummary
	err := database.DB.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN m.tipo = 'ENTRADA' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN m.tipo = 'SAIDA'   THEN 1 ELSE 0 END), 0),
			COUNT(DISTINCT m.produto_id)
		FROM movimentacoes m
		WHERE `+conditions, args...).Scan(&summary.Entries, &summary.Exits, &summary.Materials)

	return summary, err
}

// MaterialConsumption é uma linha do relatório por material: quanto
// entrou e quanto saiu no período.
//
// Aqui somar quantidade é seguro, porque cada linha é um material só,
// logo uma unidade só.
type MaterialConsumption struct {
	MaterialID int
	Material   string
	Unit       string
	Entered    float64
	Exited     float64
	// Balance é o que entrou menos o que saiu. Negativo significa que a
	// obra consumiu mais do que recebeu no período — gastou o que já
	// estava no estoque.
	Balance float64
	// ExitCount é em quantas saídas esse material foi retirado. Separa o
	// material que sai muito em pouca quantidade (ferramenta, EPI) do que
	// sai de uma vez só (uma carga de areia).
	ExitCount        int
	FormattedEntered string
	FormattedExited  string
	FormattedBalance string
}

// GetMaterialConsumption devolve o consumo por material, do que mais saiu
// para o que menos saiu. limit maior que zero corta a lista nos primeiros
// (a tela mostra só o topo); limit 0 traz todos.
func GetMaterialConsumption(filter ReportFilter, limit int) ([]MaterialConsumption, error) {
	conditions, args := reportConditions(filter)

	query := `
		SELECT
			p.id,
			p.nome,
			p.unidade,
			COALESCE(ROUND(SUM(CASE WHEN m.tipo = 'ENTRADA' THEN m.quantidade ELSE 0 END), 3), 0) AS entrou,
			COALESCE(ROUND(SUM(CASE WHEN m.tipo = 'SAIDA'   THEN m.quantidade ELSE 0 END), 3), 0) AS saiu,
			COALESCE(SUM(CASE WHEN m.tipo = 'SAIDA' THEN 1 ELSE 0 END), 0)
		FROM movimentacoes m
		JOIN produtos p ON p.id = m.produto_id
		WHERE ` + conditions + `
		GROUP BY p.id, p.nome, p.unidade
		ORDER BY saiu DESC, entrou DESC, p.nome
	`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := database.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var consumption []MaterialConsumption
	for rows.Next() {
		var line MaterialConsumption
		if err := rows.Scan(
			&line.MaterialID,
			&line.Material,
			&line.Unit,
			&line.Entered,
			&line.Exited,
			&line.ExitCount,
		); err != nil {
			return nil, err
		}

		line.Balance = utils.RoundQuantity(line.Entered - line.Exited)
		line.FormattedEntered = utils.FormatQuantity(line.Entered)
		line.FormattedExited = utils.FormatQuantity(line.Exited)
		line.FormattedBalance = utils.FormatQuantity(line.Balance)
		consumption = append(consumption, line)
	}

	return consumption, rows.Err()
}

// SiteConsumption é uma linha do comparativo entre obras.
//
// Não tem coluna de quantidade, pelo motivo explicado em ReportSummary:
// somar saco com m³ não significa nada. O que se compara é o movimento —
// quantas entradas, quantas saídas e quantos materiais diferentes
// passaram pela obra.
type SiteConsumption struct {
	SiteID    int
	SiteName  string
	Entries   int
	Exits     int
	Materials int
}

// GetSiteConsumption devolve o comparativo entre as obras ativas, da que
// mais movimentou para a que menos movimentou.
//
// O LEFT JOIN, com as condições do período no ON e não no WHERE, é o que
// faz a obra sem nenhuma movimentação no período aparecer zerada — e essa
// linha é justamente uma das informações que o relatório dá. No WHERE,
// as condições apagariam essas obras do resultado.
func GetSiteConsumption(filter ReportFilter) ([]SiteConsumption, error) {
	conditions, args := reportConditions(filter)

	rows, err := database.DB.Query(`
		SELECT
			o.id,
			o.nome,
			COALESCE(SUM(CASE WHEN m.tipo = 'ENTRADA' THEN 1 ELSE 0 END), 0) AS entradas,
			COALESCE(SUM(CASE WHEN m.tipo = 'SAIDA'   THEN 1 ELSE 0 END), 0) AS saidas,
			COUNT(DISTINCT m.produto_id)
		FROM obras o
		LEFT JOIN movimentacoes m ON m.obra_id = o.id AND `+conditions+`
		WHERE o.ativo = 1
		GROUP BY o.id, o.nome
		ORDER BY saidas DESC, entradas DESC, o.nome
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var consumption []SiteConsumption
	for rows.Next() {
		var line SiteConsumption
		if err := rows.Scan(&line.SiteID, &line.SiteName, &line.Entries, &line.Exits, &line.Materials); err != nil {
			return nil, err
		}
		consumption = append(consumption, line)
	}

	return consumption, rows.Err()
}
