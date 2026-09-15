package services

import (
	"bufio"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	database "uchoastock/backend/database"
	"uchoastock/backend/utils"
)

// ErrInsufficientStock é devolvido quando a saída pedida é maior que o
// estoque. É um erro "sentinela": quem chama confere com errors.Is em vez
// de comparar o texto da mensagem, que pode mudar (e muda: a mensagem
// final diz quanto ainda há).
var ErrInsufficientStock = errors.New("estoque insuficiente")

// maxNoteLength limita a observação da movimentação. Dá para
// "Obra Jardim Europa - bloco B - retirado pelo João" com folga.
const maxNoteLength = 120

func AddStock(reader *bufio.Reader, userID int) {
	id, err := utils.ReadInt(reader, "Digite o ID do produto: ")
	if err != nil {
		fmt.Println("ID inválido.")
		return
	}

	materialID, name, currentStock, unit, err := getStockData(id)
	if err != nil {
		fmt.Println("Material não encontrado.")
		return
	}

	fmt.Println("Material:", name)
	fmt.Println("Estoque atual:", utils.FormatQuantity(currentStock), unit)

	quantity := utils.ReadValidQuantity(reader, "Quantidade que chegou: ")
	if quantity <= 0 {
		fmt.Println("A quantidade deve ser maior que zero.")
		return
	}

	if err := AddStockWeb(materialID, float64(quantity), userID, ""); err != nil {
		fmt.Println("Erro ao atualizar estoque:", err)
		return
	}

	fmt.Println("Estoque atualizado com sucesso!")
}

// AddStockWeb registra a chegada de material: soma a quantidade e grava
// a entrada no histórico, na mesma transação. note é a observação
// opcional (de onde veio, nota fiscal...).
func AddStockWeb(materialID int, quantity float64, userID int, note string) error {
	quantity = utils.RoundQuantity(quantity)
	if quantity <= 0 {
		return fmt.Errorf("a quantidade deve ser maior que zero")
	}
	note, err := normalizeNote(note)
	if err != nil {
		return err
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// ROUND no próprio UPDATE: a conta é feita pelo SQLite, e assim o
	// resíduo de fração binária nunca chega a ser gravado.
	result, err := tx.Exec(`
		UPDATE produtos
		SET quantidade = ROUND(quantidade + ?, 3)
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
		return fmt.Errorf("material não encontrado")
	}

	if err := registerMovementTx(tx, materialID, userID, "ENTRADA", quantity, note); err != nil {
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

	materialID, name, currentStock, unit, err := getStockData(id)
	if err != nil {
		fmt.Println("Material não encontrado.")
		return
	}

	fmt.Println("Material:", name)
	fmt.Println("Estoque atual:", utils.FormatQuantity(currentStock), unit)

	quantity := utils.ReadValidQuantity(reader, "Quantidade que saiu: ")
	if quantity <= 0 {
		fmt.Println("A quantidade deve ser maior que zero.")
		return
	}

	if err := RegisterStockExitWeb(materialID, float64(quantity), userID, ""); err != nil {
		if errors.Is(err, ErrInsufficientStock) {
			fmt.Println("Estoque insuficiente.")
			fmt.Println("Estoque disponível:", utils.FormatQuantity(currentStock), unit)
			return
		}

		fmt.Println("Erro ao registrar saída:", err)
		return
	}

	fmt.Println("Saída registrada com sucesso!")
}

// RegisterStockExitWeb registra a retirada de material. Confere o
// estoque antes de debitar — ele nunca pode ficar negativo — e grava a
// saída no histórico, tudo na mesma transação.
func RegisterStockExitWeb(materialID int, quantity float64, userID int, note string) error {
	quantity = utils.RoundQuantity(quantity)
	if quantity <= 0 {
		return fmt.Errorf("a quantidade deve ser maior que zero")
	}
	note, err := normalizeNote(note)
	if err != nil {
		return err
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var stock float64
	var unit string

	err = tx.QueryRow(`
		SELECT quantidade, unidade
		FROM produtos
		WHERE id = ? AND ativo = 1
	`, materialID).Scan(&stock, &unit)

	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("material não encontrado")
		}

		return err
	}

	if quantity > utils.RoundQuantity(stock) {
		// %w "embrulha" o erro sentinela: a mensagem ganha o detalhe, e
		// errors.Is(err, ErrInsufficientStock) continua dando true.
		return fmt.Errorf("%w: há só %s %s", ErrInsufficientStock, utils.FormatQuantity(stock), unit)
	}

	_, err = tx.Exec(`
		UPDATE produtos
		SET quantidade = ROUND(quantidade - ?, 3)
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
		note,
	); err != nil {
		return err
	}

	return tx.Commit()
}

func getStockData(materialID int) (int, string, float64, string, error) {
	var name string
	var quantity float64
	var unit string

	err := database.DB.QueryRow(`
		SELECT id, nome, quantidade, unidade
		FROM produtos
		WHERE id = ?
	`, materialID).Scan(&materialID, &name, &quantity, &unit)

	return materialID, name, quantity, unit, err
}

func registerMovementTx(tx *sql.Tx, materialID int, userID int, movementType string, quantity float64, note string) error {
	_, err := tx.Exec(`
		INSERT INTO movimentacoes
		(produto_id, usuario_id, tipo, quantidade, observacao)
		VALUES (?, ?, ?, ?, ?)
	`, materialID, userID, movementType, quantity, note)
	return err
}

// normalizeNote tira os espaços das pontas da observação e confere o
// tamanho. utf8.RuneCountInString conta letras, não bytes: "ç" e "ã"
// ocupam 2 bytes cada, e len() contaria errado.
func normalizeNote(note string) (string, error) {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > maxNoteLength {
		return "", fmt.Errorf("a observação pode ter no máximo %d caracteres", maxNoteLength)
	}
	return note, nil
}
