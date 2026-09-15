package services

import (
	"bufio"
	"fmt"
	"strings"

	database "uchoastock/backend/database"
	"uchoastock/backend/utils"
)

// CreateMaterialWeb cadastra um material e já registra a entrada da
// quantidade inicial no histórico de movimentações, tudo em uma única
// transação.
func CreateMaterialWeb(name string, quantity int, userID int) error {
	name = strings.TrimSpace(name)
	if !utils.ValidateName(name) {
		return fmt.Errorf("o nome do material é obrigatório")
	}
	if !utils.ValidateQuantity(quantity) {
		return fmt.Errorf("a quantidade não pode ser negativa")
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		INSERT INTO produtos (nome, quantidade)
		VALUES (?, ?)
	`, name, quantity)
	if err != nil {
		return err
	}

	materialID, err := result.LastInsertId()
	if err != nil {
		return err
	}

	if err := registerMovementTx(tx, int(materialID), userID, "ENTRADA", quantity); err != nil {
		return err
	}

	return tx.Commit()
}

func CreateMaterial(reader *bufio.Reader, userID int) {
	name := utils.ReadValidName(reader)
	quantity := utils.ReadValidQuantity(reader, "Quantidade: ")

	tx, err := database.DB.Begin()
	if err != nil {
		fmt.Println("Erro ao iniciar cadastro:", err)
		return
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		INSERT INTO produtos (nome, quantidade)
		VALUES (?, ?)
	`, name, quantity)

	if err != nil {
		fmt.Println("Erro ao cadastrar material:", err)
		return
	}

	materialID, err := result.LastInsertId()
	if err != nil {
		fmt.Println("Erro ao obter ID do material:", err)
		return
	}

	if err := registerMovementTx(tx, int(materialID), userID, "ENTRADA", quantity); err != nil {
		fmt.Println("Erro ao registrar movimentação:", err)
		return
	}

	if err := tx.Commit(); err != nil {
		fmt.Println("Erro ao confirmar cadastro:", err)
		return
	}

	fmt.Println("Material cadastrado com sucesso!")
}

func DeleteMaterial(reader *bufio.Reader) {
	id, err := utils.ReadInt(reader, "Digite o ID do material: ")
	if err != nil {
		fmt.Println("ID inválido.")
		return
	}

	result, err := database.DB.Exec(`
		DELETE FROM produtos
		WHERE id = ?
	`, id)

	if err != nil {
		fmt.Println("Erro ao remover material:", err)
		return
	}

	rows, err := result.RowsAffected()
	if err != nil {
		fmt.Println("Erro ao verificar remoção:", err)
		return
	}

	if rows > 0 {
		fmt.Println("Material removido com sucesso.")
	} else {
		fmt.Println("Material não encontrado.")
	}

}
