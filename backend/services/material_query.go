package services

import (
	"bufio"
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

// materialColumns são as colunas lidas em toda consulta de material, na
// mesma ordem em que scanMaterial as espera.
const materialColumns = `id, nome, quantidade, unidade, limite_minimo`

// rowScanner é o que *sql.Row e *sql.Rows têm em comum: o método Scan.
// Uma interface em Go é só uma lista de métodos — qualquer tipo que os
// tenha serve, sem precisar declarar nada. Assim a mesma scanMaterial lê
// tanto uma linha avulsa quanto cada linha de um resultado maior.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanMaterial lê uma linha com materialColumns e já preenche os campos
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

func ListMaterials() {
	rows, err := database.DB.Query(`
		SELECT ` + materialColumns + `
		FROM produtos
	`)
	if err != nil {
		fmt.Println("Erro ao buscar materiais:", err)
		return
	}
	defer rows.Close()

	found := false

	for rows.Next() {
		material, err := scanMaterial(rows)
		if err != nil {
			fmt.Println("Erro ao ler material:", err)
			return
		}

		fmt.Println("ID:", material.ID)
		fmt.Println("Nome:", material.Name)
		fmt.Println("Quantidade:", material.FormattedQuantity, material.Unit)
		fmt.Println("----------------------")
		found = true
	}

	if err := rows.Err(); err != nil {
		fmt.Println("Erro ao percorrer materiais:", err)
		return
	}

	if !found {
		fmt.Println("Nenhum material cadastrado.")
	}
}

func FindMaterial(reader *bufio.Reader) {
	search := strings.TrimSpace(utils.ReadText(reader, "Digite o nome do material: "))

	rows, err := database.DB.Query(`
		SELECT `+materialColumns+`
		FROM produtos
		WHERE nome LIKE ?
	`, "%"+search+"%")
	if err != nil {
		fmt.Println("Erro ao buscar material:", err)
		return
	}
	defer rows.Close()

	found := false

	for rows.Next() {
		material, err := scanMaterial(rows)
		if err != nil {
			fmt.Println("Erro ao ler material:", err)
			return
		}

		fmt.Println("Material encontrado!")
		fmt.Println("ID:", material.ID)
		fmt.Println("Nome:", material.Name)
		fmt.Println("Quantidade:", material.FormattedQuantity, material.Unit)
		found = true
	}

	if err := rows.Err(); err != nil {
		fmt.Println("Erro ao percorrer materiais:", err)
		return
	}

	if !found {
		fmt.Println("Material não encontrado.")
	}
}

func GetAllMaterials() ([]models.Material, error) {
	rows, err := database.DB.Query(`
		SELECT ` + materialColumns + `
		FROM produtos
		WHERE ativo = 1
		ORDER BY id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var materials []models.Material

	for rows.Next() {
		material, err := scanMaterial(rows)
		if err != nil {
			return nil, err
		}
		materials = append(materials, material)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return materials, nil
}

// GetMaterialByID busca um material ativo pelo ID. É o que abre o modal
// de edição direto no material clicado.
func GetMaterialByID(materialID int) (*models.Material, error) {
	material, err := scanMaterial(database.DB.QueryRow(`
		SELECT `+materialColumns+`
		FROM produtos
		WHERE id = ? AND ativo = 1
	`, materialID))
	if err != nil {
		return nil, fmt.Errorf("material não encontrado")
	}
	return &material, nil
}

// GetLowStockMaterials devolve os materiais que estão acabando — os que
// têm quantidade igual ou menor que o próprio limite de aviso — junto de
// quem foi o último a movimentar cada um. limit corta a lista; use 0
// para trazer todos.
//
// A ordem põe os zerados primeiro e depois os mais perto do fim em
// proporção ao limite. Ordenar pela quantidade pura não serve mais:
// 5 sacos e 5 m³ não são comparáveis.
//
// O responsável sai de um LEFT JOIN com a última linha de movimentacoes
// do material. É LEFT (e não JOIN comum) de propósito: material recém
// cadastrado pode ainda não ter movimentação, e mesmo assim precisa
// aparecer no alerta.
func GetLowStockMaterials(limit int) ([]models.LowStockMaterial, error) {
	query := `
		SELECT
			p.id,
			p.nome,
			p.quantidade,
			p.unidade,
			p.limite_minimo,
			COALESCE(u.nome, ''),
			COALESCE(m.data, ''),
			COALESCE(m.tipo, '')
		FROM produtos p
		LEFT JOIN movimentacoes m ON m.id = (
			SELECT m2.id
			FROM movimentacoes m2
			WHERE m2.produto_id = p.id
			ORDER BY m2.data DESC, m2.id DESC
			LIMIT 1
		)
		LEFT JOIN usuarios u ON u.id = m.usuario_id
		WHERE p.ativo = 1 AND p.quantidade <= p.limite_minimo
		ORDER BY
			CASE WHEN p.quantidade <= 0 THEN 0 ELSE 1 END,
			p.quantidade * 1.0 / MAX(p.limite_minimo, 0.001),
			p.nome COLLATE NOCASE ASC
	`
	var args []any
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
// limite de aviso de cada um ou abaixo dele), para o cartão "Em falta".
func CountLowStockMaterials() (int, error) {
	var total int
	err := database.DB.QueryRow(`
		SELECT COUNT(*)
		FROM produtos
		WHERE ativo = 1 AND quantidade <= limite_minimo
	`).Scan(&total)
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

	if err := registerMovementTx(tx, materialID, userID, "ATUALIZACAO", 0, ""); err != nil {
		return err
	}

	return tx.Commit()
}

func DeleteMaterialWeb(materialID int) error {
	result, err := database.DB.Exec(`
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

	return nil
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

func UpdateMaterial(reader *bufio.Reader, userID int) {
	id, err := utils.ReadInt(reader, "Digite o ID do material: ")
	if err != nil {
		fmt.Println("ID inválido.")
		return
	}

	var materialID int
	var currentName string

	err = database.DB.QueryRow(`
		SELECT id, nome
		FROM produtos
		WHERE id = ? AND ativo = 1
	`, id).Scan(&materialID, &currentName)

	if err != nil {
		fmt.Println("Material não encontrado.")
		return
	}

	fmt.Println("Material encontrado.")
	fmt.Println("Nome atual:", currentName)

	newName := utils.ReadValidName(reader)

	_, err = database.DB.Exec(`
		UPDATE produtos
		SET nome = ?
		WHERE id = ?
	`, newName, materialID)
	if err != nil {
		fmt.Println("Erro ao atualizar material:", err)
		return
	}

	if err := registerMovement(materialID, userID, "ATUALIZACAO", 0, ""); err != nil {
		fmt.Println("Erro ao registrar atualização:", err)
		return
	}

	fmt.Println("Material atualizado com sucesso!")
}

// PaginatedMaterials fetches a page of active materials, optionally
// filtering by name (partial match) or exact ID. page starts at 1.
func PaginatedMaterials(search string, page int, perPage int) ([]models.Material, int, error) {
	return PaginatedSortedMaterials(search, page, perPage, "recentes")
}

func PaginatedSortedMaterials(search string, page int, perPage int, order string) ([]models.Material, int, error) {
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

	sorting := "id DESC"
	switch order {
	case "nome":
		sorting = "nome COLLATE NOCASE ASC"
	case "estoque":
		sorting = "quantidade ASC"
	}
	rows, err := database.DB.Query(`
		SELECT `+materialColumns+`
		FROM produtos
		WHERE ativo = 1 AND (nome LIKE ? OR CAST(id AS TEXT) = ?)
		ORDER BY `+sorting+`
		LIMIT ? OFFSET ?
	`, nameFilter, search, perPage, offset)
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
