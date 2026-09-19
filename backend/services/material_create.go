package services

import (
	"strings"

	database "uchoastock/backend/database"
	"uchoastock/backend/utils"
)

// CreateMaterialWeb cadastra um material no catálogo e já registra a
// entrada da quantidade inicial na obra siteID, tudo em uma única
// transação. O material nasce com saldo (mesmo zero) nessa obra, para
// aparecer no controle dela.
func CreateMaterialWeb(name string, quantity float64, unit string, minimum float64, siteID, userID int) error {
	name = strings.TrimSpace(name)
	if !utils.ValidateName(name) {
		return StockInputError{"o nome do material é obrigatório"}
	}
	quantity = utils.RoundQuantity(quantity)
	if quantity < 0 {
		return StockInputError{"a quantidade não pode ser negativa"}
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

	if err := requireOperableSiteTx(tx, siteID); err != nil {
		return err
	}

	// produtos.quantidade é a coluna do tempo em que havia um estoque só.
	// Ela é NOT NULL, então recebe 0; o saldo de verdade fica em saldos.
	result, err := tx.Exec(`
		INSERT INTO produtos (nome, quantidade, unidade, limite_minimo)
		VALUES (?, 0, ?, ?)
	`, name, unit, minimum)
	if err != nil {
		return err
	}

	materialID, err := result.LastInsertId()
	if err != nil {
		return err
	}

	if err := addToBalanceTx(tx, int(materialID), siteID, quantity); err != nil {
		return err
	}

	// Entrada é sempre de quantidade maior que zero. Sem estoque inicial,
	// o cadastro fica no histórico como atualização de catálogo (sem obra,
	// como a edição do material), e não como uma "Entrada de 0".
	if quantity > 0 {
		err = registerMovementTx(tx, int(materialID), siteID, userID, "ENTRADA", quantity, "Estoque inicial", 0)
	} else {
		err = registerMovementTx(tx, int(materialID), 0, userID, "ATUALIZACAO", 0, "Material cadastrado", 0)
	}
	if err != nil {
		return err
	}

	return tx.Commit()
}
