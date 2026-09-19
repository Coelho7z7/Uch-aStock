package services

import (
	"fmt"
	"time"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
	"uchoastock/backend/utils"
)

// validMovementTypes são os valores aceitos em movimentacoes.tipo.
var validMovementTypes = map[string]bool{"ENTRADA": true, "SAIDA": true, "ATUALIZACAO": true, MovementAdjustment: true}

// IsValidMovementType indica se o texto é um tipo de movimentação
// conhecido. O handler usa para ignorar um ?tipo= inventado na URL.
func IsValidMovementType(movementType string) bool {
	return validMovementTypes[movementType]
}

// MovementFilter reúne os filtros opcionais do histórico. Campo vazio
// significa "sem filtro" naquele critério. From e To vêm no formato
// AAAA-MM-DD e são inclusivos — quem chama já validou o formato.
//
// SiteID 0 traz o histórico de todas as obras, inclusive as atualizações
// de cadastro (que não têm obra). Com uma obra escolhida, só aparece o
// que aconteceu nela.
//
// UserID maior que zero traz só o que esse usuário registrou: é o recorte
// de quem não tem permissão de ver as movimentações de todos.
type MovementFilter struct {
	Type     string
	Material string
	From     string
	To       string
	SiteID   int
	UserID   int
	// Limit maior que zero traz só essa quantidade de linhas, a partir de
	// Offset: é a paginação feita no banco. Sem ela, o histórico inteiro
	// era lido a cada tela só para mostrar 20 linhas (ou 5, no dashboard).
	Limit  int
	Offset int
}

// movementWhere monta o WHERE do histórico. Só pedaços fixos de SQL são
// concatenados; todo valor digitado pelo usuário entra por placeholder
// "?". Usa os apelidos m (movimentacoes) e p (produtos).
func movementWhere(filter MovementFilter) (string, []any, error) {
	where := ` WHERE 1 = 1`
	var args []any

	if filter.SiteID > 0 {
		where += ` AND m.obra_id = ?`
		args = append(args, filter.SiteID)
	}
	if filter.UserID > 0 {
		where += ` AND m.usuario_id = ?`
		args = append(args, filter.UserID)
	}
	if validMovementTypes[filter.Type] {
		where += ` AND m.tipo = ?`
		args = append(args, filter.Type)
	}
	if filter.Material != "" {
		where += ` AND p.nome LIKE ?`
		args = append(args, "%"+filter.Material+"%")
	}
	// A data é gravada em UTC e o dia que importa é o do Brasil: o dia
	// local vira um intervalo em UTC (ver dayStartUTC). datetime() deixa a
	// data gravada no mesmo formato do intervalo, mesmo numa linha antiga
	// gravada em outro formato ("2025-05-01T12:00:00Z").
	if filter.From != "" {
		start, err := dayStartUTC(filter.From, 0)
		if err != nil {
			return "", nil, err
		}
		where += ` AND datetime(m.data) >= ?`
		args = append(args, start)
	}
	if filter.To != "" {
		end, err := dayStartUTC(filter.To, 1)
		if err != nil {
			return "", nil, err
		}
		where += ` AND datetime(m.data) < ?`
		args = append(args, end)
	}
	return where, args, nil
}

// CountMovements conta as movimentações do filtro (Limit e Offset não
// contam), para a paginação do histórico.
func CountMovements(filter MovementFilter) (int, error) {
	where, args, err := movementWhere(filter)
	if err != nil {
		return 0, err
	}
	var total int
	err = database.DB.QueryRow(`
		SELECT COUNT(*)
		FROM movimentacoes m
		JOIN produtos p ON p.id = m.produto_id
	`+where, args...).Scan(&total)
	return total, err
}

// GetMovementsFilteredWeb devolve o histórico aplicando os filtros no
// próprio SQL, do mais recente para o mais antigo.
//
// O JOIN com obras é LEFT porque a atualização de cadastro não tem obra:
// com JOIN comum, essas linhas sumiriam do histórico.
func GetMovementsFilteredWeb(filter MovementFilter) ([]models.Movement, error) {
	where, args, err := movementWhere(filter)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT
			m.id,
			m.produto_id,
			m.usuario_id,
			COALESCE(m.obra_id, 0),
			COALESCE(o.nome, ''),
			m.data,
			p.nome,
			p.unidade,
			u.nome,
			m.tipo,
			m.quantidade,
			m.observacao,
			COALESCE(m.solicitacao_id, 0),
			COALESCE(rq.solicitante_id, 0)
		FROM movimentacoes m
		JOIN produtos p ON p.id = m.produto_id
		JOIN usuarios u ON u.id = m.usuario_id
		LEFT JOIN obras o ON o.id = m.obra_id
		LEFT JOIN solicitacoes rq ON rq.id = m.solicitacao_id
	` + where + ` ORDER BY m.data DESC, m.id DESC`

	if filter.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, filter.Limit, filter.Offset)
	}

	rows, err := database.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var movements []models.Movement
	for rows.Next() {
		var movement models.Movement
		var date string

		if err := rows.Scan(
			&movement.ID,
			&movement.MaterialID,
			&movement.UserID,
			&movement.SiteID,
			&movement.SiteName,
			&date,
			&movement.Material,
			&movement.Unit,
			&movement.User,
			&movement.Type,
			&movement.Quantity,
			&movement.Note,
			&movement.RequestID,
			&movement.RequestRequesterID,
		); err != nil {
			return nil, err
		}

		formattedDate, err := parseMovementDate(date)
		if err != nil {
			return nil, err
		}

		movement.Date = formattedDate
		movement.FormattedDate = formattedDate.Local().Format("02/01/2006")
		movement.FormattedTime = formattedDate.Local().Format("15:04")
		movement.FormattedQuantity = utils.FormatQuantity(movement.Quantity)
		// O ajuste tem sinal: "+3" quando sobrou, "-2" quando faltou.
		if movement.Type == MovementAdjustment && movement.Quantity > 0 {
			movement.FormattedQuantity = "+" + movement.FormattedQuantity
		}
		movement.FormattedType = map[string]string{
			"ENTRADA":          "Entrada",
			"SAIDA":            "Saída",
			"ATUALIZACAO":      "Atualização",
			MovementAdjustment: "Ajuste",
		}[movement.Type]
		if movement.FormattedType == "" {
			movement.FormattedType = movement.Type
		}
		movements = append(movements, movement)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return movements, nil
}

func parseMovementDate(date string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
	}

	for _, format := range formats {
		if formattedDate, err := time.Parse(format, date); err == nil {
			return formattedDate, nil
		}
	}

	return time.Time{}, fmt.Errorf("data inválida: %s", date)
}

// CountTodayMovements conta quantas entradas e quantas saídas foram
// registradas hoje na obra (siteID 0 = todas), para o cartão de resumo
// do turno.
//
// O SQLite grava CURRENT_TIMESTAMP em UTC. Comparar direto com a data de
// hoje em UTC faria o "hoje" virar à meia-noite de Londres — no Brasil o
// turno mudaria de dia às 21h. Por isso o hoje local vira um intervalo em
// UTC (todayRangeUTC).
func CountTodayMovements(siteID int) (entries int, exits int, err error) {
	start, end := todayRangeUTC()
	err = database.DB.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN tipo = 'ENTRADA' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN tipo = 'SAIDA'   THEN 1 ELSE 0 END), 0)
		FROM movimentacoes
		WHERE datetime(data) >= ? AND datetime(data) < ?
			AND (? = 0 OR obra_id = ?)
	`, start, end, siteID, siteID).Scan(&entries, &exits)
	return entries, exits, err
}
