package services

import (
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
			"ENTRADA":     "Entrada",
			"SAIDA":       "Saída",
			"ATUALIZACAO": "Atualização",
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
func UpdateMaterialWeb(materialID int, name string, unit string, minimum float64, userID int) error {
	name = strings.TrimSpace(name)
	if !utils.ValidateName(name) {
		return fmt.Errorf("o nome do material é obrigatório")
	}
	if !utils.ValidateUnit(unit) {
		return fmt.Errorf("unidade inválida")
	}
	minimum = utils.RoundQuantity(minimum)
	if minimum < 0 {
		return fmt.Errorf("o limite de aviso não pode ser negativo")
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		UPDATE produtos
		SET nome = ?, unidade = ?, limite_minimo = ?
		WHERE id = ? AND ativo = 1
	`, name, unit, minimum, materialID)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("material não encontrado")
	}

	// O cadastro é da empresa, não de uma obra: a atualização fica sem obra.
	if err := registerMovementTx(tx, materialID, 0, userID, "ATUALIZACAO", 0, ""); err != nil {
		return err
	}

	return tx.Commit()
}

// DeleteMaterialWeb remove o material do catálogo (ativo = 0). Material
// com saldo em alguma obra não pode ser removido: ele sumiria das telas
// com o estoque ainda lá, e ninguém conseguiria mais registrar a saída.
// A checagem e a remoção ficam na mesma transação, para uma entrada que
// chegue no meio não passar despercebida.
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
		return fmt.Errorf("material não encontrado")
	}

	var sites int
	if err := tx.QueryRow(`
		SELECT COUNT(*) FROM saldos WHERE produto_id = ? AND quantidade > 0
	`, materialID).Scan(&sites); err != nil {
		return err
	}
	// O return antes do Commit desfaz o UPDATE acima (defer tx.Rollback).
	if sites == 1 {
		return fmt.Errorf("não é possível remover: o material ainda tem saldo em 1 obra. Registre a saída antes de remover")
	}
	if sites > 1 {
		return fmt.Errorf("não é possível remover: o material ainda tem saldo em %d obras. Registre as saídas antes de remover", sites)
	}

	return tx.Commit()
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
