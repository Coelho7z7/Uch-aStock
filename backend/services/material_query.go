package services

import (
	"bufio"
	"fmt"
	"strings"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
	"uchoastock/backend/utils"
)

// LowStockThreshold é a quantidade a partir da qual um material é
// considerado "prestes a acabar". Em obra a reposição demora, então o
// aviso precisa vir com folga.
const LowStockThreshold = 10

func ListMaterials() {
	rows, err := database.DB.Query(`
		SELECT id, nome, quantidade
		FROM produtos
	`)
	if err != nil {
		fmt.Println("Erro ao buscar materiais:", err)
		return
	}
	defer rows.Close()

	found := false

	for rows.Next() {
		var material models.Material

		if err := rows.Scan(&material.ID, &material.Name, &material.Quantity); err != nil {
			fmt.Println("Erro ao ler material:", err)
			return
		}

		fmt.Println("ID:", material.ID)
		fmt.Println("Nome:", material.Name)
		fmt.Println("Quantidade:", material.Quantity)
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
		SELECT id, nome, quantidade
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
		var material models.Material

		if err := rows.Scan(&material.ID, &material.Name, &material.Quantity); err != nil {
			fmt.Println("Erro ao ler material:", err)
			return
		}

		fmt.Println("Material encontrado!")
		fmt.Println("ID:", material.ID)
		fmt.Println("Nome:", material.Name)
		fmt.Println("Quantidade:", material.Quantity)
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
		SELECT id, nome, quantidade
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
		var material models.Material

		if err := rows.Scan(
			&material.ID,
			&material.Name,
			&material.Quantity,
		); err != nil {
			return nil, err
		}

		material.StockStatus = stockStatus(material.Quantity)
		materials = append(materials, material)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return materials, nil
}

// GetLowStockMaterials devolve os materiais que estão acabando, do mais
// crítico para o menos crítico, junto de quem foi o último a movimentar
// cada um. limit corta a lista; use 0 para trazer todos.
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
		WHERE p.ativo = 1 AND p.quantidade <= ?
		ORDER BY p.quantidade ASC, p.nome COLLATE NOCASE ASC
	`
	args := []any{LowStockThreshold}
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
			&material.LastUser,
			&rawDate,
			&material.FormattedType,
		); err != nil {
			return nil, err
		}

		material.StockStatus = stockStatus(material.Quantity)

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

// CountLowStockMaterials conta quantos materiais estão acabando, para o
// cartão "Em falta" do dashboard.
func CountLowStockMaterials() (int, error) {
	var total int
	err := database.DB.QueryRow(`
		SELECT COUNT(*)
		FROM produtos
		WHERE ativo = 1 AND quantidade <= ?
	`, LowStockThreshold).Scan(&total)
	return total, err
}

func UpdateMaterialWeb(materialID int, name string, userID int) error {
	name = strings.TrimSpace(name)
	if !utils.ValidateName(name) {
		return fmt.Errorf("o nome do material é obrigatório")
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		UPDATE produtos
		SET nome = ?
		WHERE id = ? AND ativo = 1
	`, name, materialID)
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

	if err := registerMovementTx(tx, materialID, userID, "ATUALIZACAO", 0); err != nil {
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

func stockStatus(quantity int) string {
	switch {
	case quantity == 0:
		return "empty"
	case quantity <= LowStockThreshold:
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

	if err := registerMovement(materialID, userID, "ATUALIZACAO", 0); err != nil {
		fmt.Println("Erro ao registrar atualização:", err)
		return
	}

	fmt.Println("Material atualizado com sucesso!")
}

// PaginatedMaterials fetches a page of active materials, optionally
// filtering by name (partial match). page starts at 1.
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

	var total int
	if err := database.DB.QueryRow(`
		SELECT COUNT(*)
		FROM produtos
		WHERE ativo = 1 AND nome LIKE ?
	`, nameFilter).Scan(&total); err != nil {
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
		SELECT id, nome, quantidade
		FROM produtos
		WHERE ativo = 1 AND nome LIKE ?
		ORDER BY `+sorting+`
		LIMIT ? OFFSET ?
	`, nameFilter, perPage, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var materials []models.Material

	for rows.Next() {
		var material models.Material

		if err := rows.Scan(&material.ID, &material.Name, &material.Quantity); err != nil {
			return nil, 0, err
		}

		material.StockStatus = stockStatus(material.Quantity)
		materials = append(materials, material)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return materials, total, nil
}
