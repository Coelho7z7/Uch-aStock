package services

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	database "uchoastock/backend/database"
)

// setupTestDB cria um banco vazio numa pasta temporária (o Go apaga a
// pasta sozinho no fim do teste), com as tabelas e um usuário de teste,
// e devolve o ID desse usuário. Cada teste ganha o seu banco: um não
// interfere no outro e nenhum toca no banco real.
func setupTestDB(t *testing.T) int {
	t.Helper()
	t.Setenv("DB_PATH", filepath.Join(t.TempDir(), "teste.db"))

	if err := database.Connect(); err != nil {
		t.Fatalf("conectar ao banco de teste: %v", err)
	}
	t.Cleanup(func() { database.DB.Close() })

	if err := database.CreateTables(); err != nil {
		t.Fatalf("criar tabelas: %v", err)
	}

	result, err := database.DB.Exec(`
		INSERT INTO usuarios (nome, email, senha, role)
		VALUES ('Teste', 'teste@gmail.com', 'x', 'admin')
	`)
	if err != nil {
		t.Fatalf("criar usuário de teste: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("ler ID do usuário de teste: %v", err)
	}
	return int(id)
}

// createTestMaterial cadastra um material em "saco" e devolve o ID dele.
func createTestMaterial(t *testing.T, userID int, name string, quantity, minimum float64) int {
	t.Helper()
	if err := CreateMaterialWeb(name, quantity, "saco", minimum, userID); err != nil {
		t.Fatalf("cadastrar %s: %v", name, err)
	}

	var id int
	if err := database.DB.QueryRow(`SELECT id FROM produtos WHERE nome = ?`, name).Scan(&id); err != nil {
		t.Fatalf("ler ID de %s: %v", name, err)
	}
	return id
}

func stockOf(t *testing.T, materialID int) float64 {
	t.Helper()
	var quantity float64
	if err := database.DB.QueryRow(`SELECT quantidade FROM produtos WHERE id = ?`, materialID).Scan(&quantity); err != nil {
		t.Fatalf("ler estoque: %v", err)
	}
	return quantity
}

func movementCount(t *testing.T, movementType string) int {
	t.Helper()
	var total int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM movimentacoes WHERE tipo = ?`, movementType).Scan(&total); err != nil {
		t.Fatalf("contar movimentações: %v", err)
	}
	return total
}

func TestAddStockWebAddsQuantityAndRecordsMovement(t *testing.T) {
	userID := setupTestDB(t)
	id := createTestMaterial(t, userID, "Areia média", 10, 5)

	if err := AddStockWeb(id, 2.5, userID, "  Obra Jardim  "); err != nil {
		t.Fatalf("entrada falhou: %v", err)
	}

	if got := stockOf(t, id); got != 12.5 {
		t.Errorf("estoque = %v, esperado 12.5", got)
	}

	var quantity float64
	var note string
	if err := database.DB.QueryRow(`
		SELECT quantidade, observacao FROM movimentacoes
		WHERE produto_id = ? AND tipo = 'ENTRADA'
		ORDER BY id DESC LIMIT 1
	`, id).Scan(&quantity, &note); err != nil {
		t.Fatalf("ler movimentação: %v", err)
	}
	if quantity != 2.5 || note != "Obra Jardim" {
		t.Errorf("movimentação = (%v, %q), esperado (2.5, \"Obra Jardim\")", quantity, note)
	}
}

func TestAddStockWebRejectsInvalidQuantity(t *testing.T) {
	userID := setupTestDB(t)
	id := createTestMaterial(t, userID, "Cimento", 10, 5)

	// 0,0001 arredonda para zero, então também tem que ser recusado.
	for _, quantity := range []float64{0, -1, 0.0001} {
		if err := AddStockWeb(id, quantity, userID, ""); err == nil {
			t.Errorf("entrada de %v deveria falhar", quantity)
		}
		if err := RegisterStockExitWeb(id, quantity, userID, ""); err == nil {
			t.Errorf("saída de %v deveria falhar", quantity)
		}
	}

	if got := stockOf(t, id); got != 10 {
		t.Errorf("estoque mudou para %v, esperado 10", got)
	}
	// Só a entrada do cadastro, nenhuma outra movimentação.
	if got := movementCount(t, "ENTRADA"); got != 1 {
		t.Errorf("%d entradas registradas, esperado 1", got)
	}
	if got := movementCount(t, "SAIDA"); got != 0 {
		t.Errorf("%d saídas registradas, esperado 0", got)
	}
}

func TestStockOperationsRejectUnknownMaterial(t *testing.T) {
	userID := setupTestDB(t)

	if err := AddStockWeb(9999, 1, userID, ""); err == nil {
		t.Error("entrada em material inexistente deveria falhar")
	}
	if err := RegisterStockExitWeb(9999, 1, userID, ""); err == nil {
		t.Error("saída de material inexistente deveria falhar")
	}
}

func TestRegisterStockExitWebNeverGoesNegative(t *testing.T) {
	userID := setupTestDB(t)
	id := createTestMaterial(t, userID, "Cimento", 5, 2)

	err := RegisterStockExitWeb(id, 6, userID, "")
	if !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("erro = %v, esperado ErrInsufficientStock", err)
	}
	if !strings.Contains(err.Error(), "5 saco") {
		t.Errorf("mensagem %q deveria dizer quanto há (5 saco)", err.Error())
	}

	if got := stockOf(t, id); got != 5 {
		t.Errorf("estoque = %v, esperado 5", got)
	}
	if got := movementCount(t, "SAIDA"); got != 0 {
		t.Errorf("%d saídas registradas, esperado 0", got)
	}
}

func TestRegisterStockExitWebEmptiesStockWithDecimals(t *testing.T) {
	userID := setupTestDB(t)
	id := createTestMaterial(t, userID, "Brita", 1.1, 0)

	// Em float puro, 1,1 - 0,3 - 0,8 não dá zero exato. O arredondamento
	// tem que garantir que a última saída zere o estoque sem sobrar nada.
	for _, quantity := range []float64{0.3, 0.8} {
		if err := RegisterStockExitWeb(id, quantity, userID, "Obra A"); err != nil {
			t.Fatalf("saída de %v falhou: %v", quantity, err)
		}
	}

	if got := stockOf(t, id); got != 0 {
		t.Errorf("estoque = %v, esperado 0", got)
	}

	if err := RegisterStockExitWeb(id, 0.001, userID, ""); !errors.Is(err, ErrInsufficientStock) {
		t.Errorf("saída com estoque zerado: erro = %v, esperado ErrInsufficientStock", err)
	}
}

func TestStockMovementLimitsNoteLength(t *testing.T) {
	userID := setupTestDB(t)
	id := createTestMaterial(t, userID, "Cimento", 10, 5)

	if err := AddStockWeb(id, 1, userID, strings.Repeat("ã", maxNoteLength+1)); err == nil {
		t.Error("observação acima do limite deveria falhar")
	}
	if err := AddStockWeb(id, 1, userID, strings.Repeat("ã", maxNoteLength)); err != nil {
		t.Errorf("observação no limite exato deveria passar: %v", err)
	}
}

func TestRemovedMaterialCannotBeMoved(t *testing.T) {
	userID := setupTestDB(t)
	id := createTestMaterial(t, userID, "Cimento", 10, 5)

	if err := DeleteMaterialWeb(id); err != nil {
		t.Fatalf("remover: %v", err)
	}
	if err := AddStockWeb(id, 1, userID, ""); err == nil {
		t.Error("entrada em material removido deveria falhar")
	}
	if err := RegisterStockExitWeb(id, 1, userID, ""); err == nil {
		t.Error("saída de material removido deveria falhar")
	}
}
