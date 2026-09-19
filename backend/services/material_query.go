package services

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
	"uchoastock/backend/utils"
)

// LowStockThreshold é o limite de aviso sugerido para um material novo:
// com essa quantidade ou menos, ele entra em "acabando". Cada material
// tem o seu próprio limite (coluna limite_minimo); este é só o padrão.
// Em obra a reposição demora, então o aviso precisa vir com folga.
const LowStockThreshold = database.DefaultMinimumStock

// materialSelect lê as colunas de material na ordem que scanMaterial
// espera. A quantidade vem de saldos: a da obra pedida, ou a soma de
// todas quando o ID é 0. Por isso o SELECT recebe o ID da obra duas
// vezes, nos dois "?" da subconsulta.
//
// COALESCE(..., 0) cobre o material que nunca passou pela obra: sem
// nenhuma linha em saldos, o SUM dá NULL, e para a tela isso é zero.
const materialSelect = `
	SELECT
		p.id,
		p.nome,
		COALESCE((
			SELECT ROUND(SUM(s.quantidade), 3)
			FROM saldos s
			WHERE s.produto_id = p.id AND (? = 0 OR s.obra_id = ?)
		), 0) AS quantidade,
		p.unidade,
		p.limite_minimo
	FROM produtos p
`

// rowScanner é o que *sql.Row e *sql.Rows têm em comum: o método Scan.
// Uma interface em Go é só uma lista de métodos — qualquer tipo que os
// tenha serve, sem precisar declarar nada. Assim a mesma scanMaterial lê
// tanto uma linha avulsa quanto cada linha de um resultado maior.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanMaterial lê uma linha de materialSelect e já preenche os campos
// calculados (situação do estoque e números formatados).
func scanMaterial(row rowScanner) (models.Material, error) {
	var material models.Material
	if err := row.Scan(
		&material.ID,
		&material.Name,
		&material.Quantity,
		&material.Unit,
		&material.MinimumStock,
	); err != nil {
		return material, err
	}
	fillMaterialDisplay(&material)
	return material, nil
}

func fillMaterialDisplay(material *models.Material) {
	material.StockStatus = stockStatus(material.Quantity, material.MinimumStock)
	material.FormattedQuantity = utils.FormatQuantity(material.Quantity)
	material.FormattedMinimum = utils.FormatQuantity(material.MinimumStock)
}

// GetMaterialByID busca um material ativo pelo ID, com a quantidade
// somada de todas as obras. É o que abre o modal de edição direto no
// material clicado.
func GetMaterialByID(materialID int) (*models.Material, error) {
	material, err := scanMaterial(database.DB.QueryRow(
		materialSelect+` WHERE p.id = ? AND p.ativo = 1`,
		0, 0, materialID,
	))
	if err != nil {
		return nil, fmt.Errorf("material não encontrado")
	}
	return &material, nil
}

// lowStockWhere é o filtro comum da lista e da contagem de materiais
// acabando, sobre saldos s, produtos p e obras o. Cada linha é um
// material numa obra: 200 sacos no central não resolvem a obra que está
// sem nenhum, então não se soma o saldo de obras diferentes. Obra
// concluída fica de fora, porque não vai mais receber material. Recebe o
// ID da obra duas vezes (0 = todas).
const lowStockWhere = `
	WHERE p.ativo = 1
		AND o.ativo = 1
		AND o.situacao <> 'CONCLUIDA'
		AND s.quantidade <= p.limite_minimo
		AND (? = 0 OR s.obra_id = ?)
`

// GetLowStockMaterials devolve os materiais que estão acabando em cada
// obra — saldo igual ou menor que o limite de aviso do material — junto
// de quem foi o último a movimentá-los ali. siteID 0 traz todas as
// obras. limit corta a lista; use 0 para trazer todos.
//
// A ordem põe os zerados primeiro e depois os mais perto do fim em
// proporção ao limite. Ordenar pela quantidade pura não serve:
// 5 sacos e 5 m³ não são comparáveis.
//
// O responsável sai de um LEFT JOIN com a última movimentação do
// material naquela obra. É LEFT (e não JOIN comum) de propósito: pode não
// haver movimentação nenhuma, e mesmo assim o material precisa aparecer.
func GetLowStockMaterials(limit, siteID int) ([]models.LowStockMaterial, error) {
	query := `
		SELECT
			p.id,
			p.nome,
			s.quantidade,
			p.unidade,
			p.limite_minimo,
			o.id,
			o.nome,
			COALESCE(u.nome, ''),
			COALESCE(m.data, ''),
			COALESCE(m.tipo, '')
		FROM saldos s
		JOIN produtos p ON p.id = s.produto_id
		JOIN obras o ON o.id = s.obra_id
		LEFT JOIN movimentacoes m ON m.id = (
			SELECT m2.id
			FROM movimentacoes m2
			WHERE m2.produto_id = s.produto_id AND m2.obra_id = s.obra_id
			ORDER BY m2.data DESC, m2.id DESC
			LIMIT 1
		)
		LEFT JOIN usuarios u ON u.id = m.usuario_id
		` + lowStockWhere + `
		ORDER BY
			CASE WHEN s.quantidade <= 0 THEN 0 ELSE 1 END,
			s.quantidade * 1.0 / MAX(p.limite_minimo, 0.001),
			p.nome COLLATE NOCASE ASC,
			o.nome COLLATE NOCASE ASC
	`

	args := []any{siteID, siteID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := database.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var materials []models.LowStockMaterial

	for rows.Next() {
		var material models.LowStockMaterial
		var rawDate string

		if err := rows.Scan(
			&material.ID,
			&material.Name,
			&material.Quantity,
			&material.Unit,
			&material.MinimumStock,
			&material.SiteID,
			&material.SiteName,
			&material.LastUser,
			&rawDate,
			&material.FormattedType,
		); err != nil {
			return nil, err
		}

		fillMaterialDisplay(&material.Material)

		if rawDate != "" {
			if parsed, err := parseMovementDate(rawDate); err == nil {
				material.LastMovedAt = parsed
				material.FormattedDate = parsed.Local().Format("02/01 15:04")
			}
		}

		material.FormattedType = map[string]string{
			"ENTRADA":          "Entrada",
			"SAIDA":            "Saída",
			"ATUALIZACAO":      "Atualização",
			MovementAdjustment: "Ajuste",
		}[material.FormattedType]

		materials = append(materials, material)
	}

	return materials, rows.Err()
}

// CountLowStockMaterials conta quantos materiais estão acabando (no
// limite de aviso ou abaixo), contando cada obra separadamente, para o
// cartão "Em falta". siteID 0 conta todas as obras.
func CountLowStockMaterials(siteID int) (int, error) {
	var total int
	err := database.DB.QueryRow(`
		SELECT COUNT(*)
		FROM saldos s
		JOIN produtos p ON p.id = s.produto_id
		JOIN obras o ON o.id = s.obra_id
	`+lowStockWhere, siteID, siteID).Scan(&total)
	return total, err
}

// UpdateMaterialWeb altera o cadastro de um material: nome, unidade e
// limite de aviso. A quantidade não muda por aqui — ela só se mexe por
// entrada e saída, para tudo ficar no histórico.
//
// A unidade só muda com o material parado: sem saldo em nenhuma obra e
// fora de solicitação e inventário em aberto. Trocar "saco" por "kg" com
// 200 em estoque transformaria 200 sacos em 200 kg, sem nenhuma
// movimentação explicando.
func UpdateMaterialWeb(materialID int, name string, unit string, minimum float64, userID int) error {
	name = strings.TrimSpace(name)
	if !utils.ValidateName(name) {
		return StockInputError{"o nome do material é obrigatório"}
	}
	if !utils.ValidateUnit(unit) {
		return StockInputError{"unidade inválida"}
	}
	minimum = utils.RoundQuantity(minimum)
	if minimum < 0 {
		return StockInputError{"o limite de aviso não pode ser negativo"}
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var currentUnit string
	err = tx.QueryRow(`SELECT unidade FROM produtos WHERE id = ? AND ativo = 1`, materialID).Scan(&currentUnit)
	if errors.Is(err, sql.ErrNoRows) {
		return StockInputError{"material não encontrado"}
	}
	if err != nil {
		return err
	}
	if unit != currentUnit {
		usage, err := materialUsageTx(tx, materialID)
		if err != nil {
			return err
		}
		if blockers := usage.blockers("Registre as saídas antes de trocar a unidade.", "Atenda, rejeite ou cancele antes de trocar a unidade.", "Aprove ou cancele o inventário antes de trocar a unidade."); len(blockers) > 0 {
			return StockInputError{fmt.Sprintf("não é possível trocar a unidade de %s para %s: %s", currentUnit, unit, strings.Join(blockers, " "))}
		}
	}

	if _, err := tx.Exec(`
		UPDATE produtos
		SET nome = ?, unidade = ?, limite_minimo = ?
		WHERE id = ? AND ativo = 1
	`, name, unit, minimum, materialID); err != nil {
		return err
	}

	// O cadastro é da empresa, não de uma obra: a atualização fica sem obra.
	if err := registerMovementTx(tx, materialID, 0, userID, "ATUALIZACAO", 0, "", 0); err != nil {
		return err
	}

	return tx.Commit()
}

// DeleteMaterialWeb remove o material do catálogo (ativo = 0). Não pode
// ser removido material com saldo em alguma obra (ele sumiria das telas
// com o estoque ainda lá, e ninguém conseguiria mais registrar a saída)
// nem material que está numa solicitação em aberto (pendente, aprovada ou
// parcial): o item ficaria impossível de atender. A checagem e a remoção
// ficam na mesma transação, para uma entrada ou solicitação que chegue no
// meio não passar despercebida.
func DeleteMaterialWeb(materialID int) error {
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		UPDATE produtos
		SET ativo = 0
		WHERE id = ? AND ativo = 1
	`, materialID)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return StockInputError{"material não encontrado"}
	}

	usage, err := materialUsageTx(tx, materialID)
	if err != nil {
		return err
	}
	blockers := usage.blockers("Registre as saídas antes de remover.", "Atenda, rejeite ou cancele antes de remover.", "Aprove ou cancele o inventário antes de remover.")
	// O return antes do Commit desfaz o UPDATE acima (defer tx.Rollback).
	if len(blockers) > 0 {
		return StockInputError{"não é possível remover: " + strings.Join(blockers, " ")}
	}

	return tx.Commit()
}

// materialUsage diz onde um material ainda está em uso: em quantas obras
// tem saldo, em quantas solicitações em aberto e em quantos inventários
// abertos aparece.
type materialUsage struct {
	sites           int
	openRequests    int
	openInventories int
}

// materialUsageTx conta os usos do material, dentro da transação de quem
// chama. É a checagem comum de remover o material e de trocar a unidade.
func materialUsageTx(tx *sql.Tx, materialID int) (materialUsage, error) {
	var usage materialUsage
	if err := tx.QueryRow(`
		SELECT COUNT(*) FROM saldos WHERE produto_id = ? AND quantidade > 0
	`, materialID).Scan(&usage.sites); err != nil {
		return usage, err
	}
	if err := tx.QueryRow(`
		SELECT COUNT(DISTINCT r.id)
		FROM solicitacao_itens i
		JOIN solicitacoes r ON r.id = i.solicitacao_id
		WHERE i.produto_id = ? AND r.status IN (?, ?, ?)
	`, materialID, RequestPending, RequestApproved, RequestPartial).Scan(&usage.openRequests); err != nil {
		return usage, err
	}
	// Material que está numa contagem aberta também conta: a aprovação
	// ainda pode ajustar o saldo dele.
	if err := tx.QueryRow(`
		SELECT COUNT(DISTINCT v.id)
		FROM inventario_itens i
		JOIN inventarios v ON v.id = i.inventario_id
		WHERE i.produto_id = ? AND v.status IN (?, ?)
	`, materialID, InventoryCounting, InventoryAwaitingApproval).Scan(&usage.openInventories); err != nil {
		return usage, err
	}
	return usage, nil
}

// blockers escreve um motivo por uso encontrado, cada um terminando com o
// que fazer (stockHint, requestHint e inventoryHint mudam conforme a
// ação: remover ou trocar a unidade). Vazio quando o material está livre.
func (u materialUsage) blockers(stockHint, requestHint, inventoryHint string) []string {
	var blockers []string
	if u.sites == 1 {
		blockers = append(blockers, "o material ainda tem saldo em 1 obra. "+stockHint)
	}
	if u.sites > 1 {
		blockers = append(blockers, fmt.Sprintf("o material ainda tem saldo em %d obras. %s", u.sites, stockHint))
	}
	if u.openRequests == 1 {
		blockers = append(blockers, "o material está em 1 solicitação em aberto (pendente, aprovada ou parcial). "+requestHint)
	}
	if u.openRequests > 1 {
		blockers = append(blockers, fmt.Sprintf("o material está em %d solicitações em aberto (pendentes, aprovadas ou parciais). %s", u.openRequests, requestHint))
	}
	if u.openInventories > 0 {
		blockers = append(blockers, "o material está num inventário aberto. "+inventoryHint)
	}
	return blockers
}

// stockStatus classifica o estoque para a cor do selo na tela. Os
// valores (empty/low/normal) viram classe CSS no template.
func stockStatus(quantity float64, minimum float64) string {
	switch {
	case quantity <= 0:
		return "empty"
	case quantity <= minimum:
		return "low"
	default:
		return "normal"
	}
}

// PaginatedMaterials fetches a page of active materials, optionally
// filtering by name (partial match) or exact ID. page starts at 1.
// siteID escolhe de qual obra vem a quantidade (0 = soma de todas).
func PaginatedMaterials(search string, page, perPage, siteID int) ([]models.Material, int, error) {
	return PaginatedSortedMaterials(search, page, perPage, "recentes", siteID)
}

func PaginatedSortedMaterials(search string, page, perPage int, order string, siteID int) ([]models.Material, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 10
	}

	search = strings.TrimSpace(search)
	nameFilter := "%" + search + "%"

	// A busca casa pelo nome (parte dele) ou pelo ID exato — o campo de
	// busca da tela diz "Buscar material ou ID".
	var total int
	if err := database.DB.QueryRow(`
		SELECT COUNT(*)
		FROM produtos
		WHERE ativo = 1 AND (nome LIKE ? OR CAST(id AS TEXT) = ?)
	`, nameFilter, search).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * perPage

	sorting := "p.id DESC"
	switch order {
	case "nome":
		sorting = "p.nome COLLATE NOCASE ASC"
	case "estoque":
		sorting = "quantidade ASC"
	}
	rows, err := database.DB.Query(
		materialSelect+`
		WHERE p.ativo = 1 AND (p.nome LIKE ? OR CAST(p.id AS TEXT) = ?)
		ORDER BY `+sorting+`
		LIMIT ? OFFSET ?
	`, siteID, siteID, nameFilter, search, perPage, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var materials []models.Material

	for rows.Next() {
		material, err := scanMaterial(rows)
		if err != nil {
			return nil, 0, err
		}
		materials = append(materials, material)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return materials, total, nil
}
