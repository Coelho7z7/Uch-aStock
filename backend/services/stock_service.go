package services

import (
	"bufio"
	"database/sql"
	"fmt"

	database "uchoastock/backend/database"
	"uchoastock/backend/utils"
)

func AddStock(reader *bufio.Reader, userID int) {
	id, err := utils.ReadInt(reader, "Digite o ID do produto: ")
	if err != nil {
		fmt.Println("ID inválido.")
		return
	}

	materialID, name, currentStock, err := getStockData(id)
	if err != nil {
		fmt.Println("Material não encontrado.")
		return
	}

	fmt.Println("Material:", name)
	fmt.Println("Estoque atual:", currentStock)

	quantity := utils.ReadValidQuantity(reader, "Quantidade que chegou: ")
	if quantity <= 0 {
		fmt.Println("A quantidade deve ser maior que zero.")
		return
	}

	if err := AddStockWeb(materialID, quantity, userID); err != nil {
		fmt.Println("Erro ao atualizar estoque:", err)
		return
	}

	fmt.Println("Estoque atualizado com sucesso!")
}

func AddStockWeb(materialID int, quantity int, userID int) error {
	if quantity <= 0 {
		return fmt.Errorf("a quantidade deve ser maior que zero")
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		UPDATE produtos
		SET quantidade = quantidade + ?
		WHERE id = ? AND ativo = 1
	`, quantity, materialID)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("produto não encontrado")
	}

	if err := registerMovementTx(tx, materialID, userID, "ENTRADA", quantity); err != nil {
		return err
	}

	return tx.Commit()
}

func RegisterStockExit(reader *bufio.Reader, userID int) {
	id, err := utils.ReadInt(reader, "Digite o ID do produto: ")
	if err != nil {
		fmt.Println("ID inválido.")
		return
	}

	materialID, name, currentStock, err := getStockData(id)
	if err != nil {
		fmt.Println("Material não encontrado.")
		return
	}

	fmt.Println("Material:", name)
	fmt.Println("Estoque atual:", currentStock)

	quantity := utils.ReadValidQuantity(reader, "Quantidade que saiu: ")
	if quantity <= 0 {
		fmt.Println("A quantidade deve ser maior que zero.")
		return
	}

	if err := RegisterStockExitWeb(materialID, quantity, userID); err != nil {
		if err.Error() == "estoque insuficiente" {
			fmt.Println("Estoque insuficiente.")
			fmt.Println("Estoque disponível:", currentStock)
			return
		}

		fmt.Println("Erro ao registrar saída:", err)
		return
	}

	fmt.Println("Saída registrada com sucesso!")
}

func RegisterStockExitWeb(materialID int, quantity int, userID int) error {
	if quantity <= 0 {
		return fmt.Errorf("a quantidade deve ser maior que zero")
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var stock int

	err = tx.QueryRow(`
		SELECT quantidade
		FROM produtos
		WHERE id = ? AND ativo = 1
	`, materialID).Scan(&stock)

	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("produto não encontrado")
		}

		return err
	}

	if quantity > stock {
		return fmt.Errorf("estoque insuficiente")
	}

	_, err = tx.Exec(`
		UPDATE produtos
		SET quantidade = quantidade - ?
		WHERE id = ?
	`, quantity, materialID)

	if err != nil {
		return err
	}

	if err := registerMovementTx(
		tx,
		materialID,
		userID,
		"SAIDA",
		quantity,
	); err != nil {
		return err
	}

	return tx.Commit()
}
func getStockData(materialID int) (int, string, int, error) {
	var name string
	var quantity int

	err := database.DB.QueryRow(`
		SELECT id, nome, quantidade
		FROM produtos
		WHERE id = ?
	`, materialID).Scan(&materialID, &name, &quantity)

	return materialID, name, quantity, err
}

func registerMovementTx(tx *sql.Tx, materialID int, userID int, movementType string, quantity int) error {
	_, err := tx.Exec(`
		INSERT INTO movimentacoes
		(produto_id, usuario_id, tipo, quantidade)
		VALUES (?, ?, ?, ?)
	`, materialID, userID, movementType, quantity)
	return err
}
