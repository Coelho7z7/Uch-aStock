package services

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	database "uchoastock/backend/database"
	"uchoastock/backend/utils"
)

// ErrInsufficientStock é devolvido quando a saída pedida é maior que o
// saldo da obra. É um erro "sentinela": quem chama confere com errors.Is
// em vez de comparar o texto da mensagem, que pode mudar (e muda: a
// mensagem final diz quanto ainda há).
var ErrInsufficientStock = errors.New("estoque insuficiente")

// ErrSiteFinished é devolvido quando alguém tenta movimentar material
// numa obra concluída. Obra concluída é só para consulta.
var ErrSiteFinished = errors.New("obra concluída: só consulta, não aceita entrada nem saída")

// maxNoteLength limita a observação da movimentação. Dá para
// "Bloco B, laje 4 - retirado pelo João" com folga.
const maxNoteLength = 120

// AddStockWeb registra a chegada de material numa obra: soma ao saldo
// daquela obra e grava a entrada no histórico, na mesma transação. note
// é a observação opcional (de onde veio, nota fiscal...).
func AddStockWeb(materialID, siteID int, quantity float64, userID int, note string) error {
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

	if _, err := activeMaterialUnitTx(tx, materialID); err != nil {
		return err
	}
	if err := requireOperableSiteTx(tx, siteID); err != nil {
		return err
	}
	if err := addToBalanceTx(tx, materialID, siteID, quantity); err != nil {
		return err
	}
	if err := registerMovementTx(tx, materialID, siteID, userID, "ENTRADA", quantity, note); err != nil {
		return err
	}

	return tx.Commit()
}

// RegisterStockExitWeb registra a saída de material de uma obra. O saldo
// daquela obra nunca fica negativo: material que está em outra obra não
// conta, porque não está fisicamente ali.
func RegisterStockExitWeb(materialID, siteID int, quantity float64, userID int, note string) error {
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

	unit, err := activeMaterialUnitTx(tx, materialID)
	if err != nil {
		return err
	}
	if err := requireOperableSiteTx(tx, siteID); err != nil {
		return err
	}

	// A checagem do saldo está dentro do próprio UPDATE (o "AND ... >= ?").
	// Se não houver o suficiente, nenhuma linha muda. Assim não existe
	// intervalo entre ler o saldo e debitar em que outra saída possa
	// passar na frente.
	result, err := tx.Exec(`
		UPDATE saldos
		SET quantidade = ROUND(quantidade - ?, 3)
		WHERE produto_id = ? AND obra_id = ? AND ROUND(quantidade, 3) >= ?
	`, quantity, materialID, siteID, quantity)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		available, err := balanceTx(tx, materialID, siteID)
		if err != nil {
			return err
		}
		// %w "embrulha" o erro sentinela: a mensagem ganha o detalhe, e
		// errors.Is(err, ErrInsufficientStock) continua dando true.
		return fmt.Errorf("%w: há só %s %s nesta obra", ErrInsufficientStock, utils.FormatQuantity(available), unit)
	}

	if err := registerMovementTx(tx, materialID, siteID, userID, "SAIDA", quantity, note); err != nil {
		return err
	}

	return tx.Commit()
}

// activeMaterialUnitTx confere que o material existe e não foi removido,
// e devolve a unidade dele (para a mensagem de saldo insuficiente).
func activeMaterialUnitTx(tx *sql.Tx, materialID int) (string, error) {
	var unit string
	err := tx.QueryRow(`SELECT unidade FROM produtos WHERE id = ? AND ativo = 1`, materialID).Scan(&unit)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("material não encontrado")
	}
	return unit, err
}

// requireOperableSiteTx confere que a obra existe e aceita movimentação.
// Paralisada aceita (dá para devolver ou retirar material); concluída não.
func requireOperableSiteTx(tx *sql.Tx, siteID int) error {
	var status string
	err := tx.QueryRow(`SELECT situacao FROM obras WHERE id = ? AND ativo = 1`, siteID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("obra não encontrada")
	}
	if err != nil {
		return err
	}
	if status == SiteStatusFinished {
		return ErrSiteFinished
	}
	return nil
}

// addToBalanceTx soma quantity ao saldo do material na obra. Se ainda não
// existe saldo daquele material ali, o INSERT cria; se já existe, o
// ON CONFLICT transforma o INSERT num UPDATE que soma. ROUND no próprio
// SQL: a conta é feita pelo SQLite, e o resíduo de fração binária nunca
// chega a ser gravado.
func addToBalanceTx(tx *sql.Tx, materialID, siteID int, quantity float64) error {
	_, err := tx.Exec(`
		INSERT INTO saldos (produto_id, obra_id, quantidade)
		VALUES (?, ?, ROUND(?, 3))
		ON CONFLICT (produto_id, obra_id)
		DO UPDATE SET quantidade = ROUND(quantidade + excluded.quantidade, 3)
	`, materialID, siteID, quantity)
	return err
}

// balanceTx devolve o saldo do material na obra (0 se nunca passou por lá).
func balanceTx(tx *sql.Tx, materialID, siteID int) (float64, error) {
	var quantity float64
	err := tx.QueryRow(`
		SELECT COALESCE(SUM(quantidade), 0)
		FROM saldos
		WHERE produto_id = ? AND obra_id = ?
	`, materialID, siteID).Scan(&quantity)
	return quantity, err
}

// registerMovementTx grava uma linha no histórico. siteID 0 grava a obra
// como NULL: é o caso da atualização de cadastro, que não acontece em
// nenhuma obra.
func registerMovementTx(tx *sql.Tx, materialID, siteID, userID int, movementType string, quantity float64, note string) error {
	var site any
	if siteID > 0 {
		site = siteID
	}
	_, err := tx.Exec(`
		INSERT INTO movimentacoes
		(produto_id, obra_id, usuario_id, tipo, quantidade, observacao)
		VALUES (?, ?, ?, ?, ?, ?)
	`, materialID, site, userID, movementType, quantity, note)
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

// StockDivergence é um material cuja quantidade antiga (produtos.quantidade,
// do tempo em que havia um estoque só) não bate com a soma dos saldos das
// obras.
type StockDivergence struct {
	MaterialID  int
	Name        string
	Unit        string
	Active      bool
	OldQuantity float64
	SiteTotal   float64
}

// VerifyStockMigration compara, para cada material (inclusive os
// removidos), produtos.quantidade com a soma dos saldos de todas as obras,
// e devolve as divergências e quantos materiais foram conferidos.
//
// Serve para conferir a migração para o saldo por obra: logo depois dela,
// as duas contas têm de bater. Depois que o sistema passa a ser usado, elas
// se afastam de propósito — produtos.quantidade não é mais atualizada —,
// então o resultado só diz algo num banco recém-migrado.
func VerifyStockMigration() ([]StockDivergence, int, error) {
	rows, err := database.DB.Query(`
		SELECT
			p.id,
			p.nome,
			p.unidade,
			p.ativo,
			p.quantidade,
			COALESCE((SELECT SUM(s.quantidade) FROM saldos s WHERE s.produto_id = p.id), 0)
		FROM produtos p
		ORDER BY p.id
	`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var divergences []StockDivergence
	checked := 0
	for rows.Next() {
		var d StockDivergence
		if err := rows.Scan(&d.MaterialID, &d.Name, &d.Unit, &d.Active, &d.OldQuantity, &d.SiteTotal); err != nil {
			return nil, 0, err
		}
		checked++
		// Arredonda as duas antes de comparar: 2,5 somado em partes pode
		// virar 2,4999999999 em ponto flutuante.
		d.OldQuantity = utils.RoundQuantity(d.OldQuantity)
		d.SiteTotal = utils.RoundQuantity(d.SiteTotal)
		if d.OldQuantity != d.SiteTotal {
			divergences = append(divergences, d)
		}
	}
	return divergences, checked, rows.Err()
}
