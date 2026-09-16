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

// centralID devolve o ID do almoxarifado central, criado pela migração.
func centralID(t *testing.T) int {
	t.Helper()
	var id int
	if err := database.DB.QueryRow(`SELECT id FROM obras WHERE tipo = 'CENTRAL'`).Scan(&id); err != nil {
		t.Fatalf("ler almoxarifado central: %v", err)
	}
	return id
}

// createTestSite cadastra uma obra com a situação pedida e devolve o ID.
func createTestSite(t *testing.T, name, status string) int {
	t.Helper()
	if err := CreateSiteWeb(name, "", ""); err != nil {
		t.Fatalf("cadastrar obra %s: %v", name, err)
	}
	id := siteIDByName(t, name)
	if status != SiteStatusInProgress {
		if err := UpdateSiteWeb(id, name, "", "", status, true); err != nil {
			t.Fatalf("mudar situação de %s: %v", name, err)
		}
	}
	return id
}

// createTestMaterial cadastra um material em "saco", com a quantidade
// inicial no almoxarifado central, e devolve o ID dele.
func createTestMaterial(t *testing.T, userID int, name string, quantity, minimum float64) int {
	t.Helper()
	if err := CreateMaterialWeb(name, quantity, "saco", minimum, centralID(t), userID); err != nil {
		t.Fatalf("cadastrar %s: %v", name, err)
	}

	var id int
	if err := database.DB.QueryRow(`SELECT id FROM produtos WHERE nome = ?`, name).Scan(&id); err != nil {
		t.Fatalf("ler ID de %s: %v", name, err)
	}
	return id
}

// balanceOf devolve o saldo do material na obra (0 se nunca passou por lá).
func balanceOf(t *testing.T, materialID, siteID int) float64 {
	t.Helper()
	var quantity float64
	if err := database.DB.QueryRow(`
		SELECT COALESCE(SUM(quantidade), 0) FROM saldos WHERE produto_id = ? AND obra_id = ?
	`, materialID, siteID).Scan(&quantity); err != nil {
		t.Fatalf("ler saldo: %v", err)
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
	central := centralID(t)
	id := createTestMaterial(t, userID, "Areia média", 10, 5)

	if err := AddStockWeb(id, central, 2.5, userID, "  NF 123  "); err != nil {
		t.Fatalf("entrada falhou: %v", err)
	}

	if got := balanceOf(t, id, central); got != 12.5 {
		t.Errorf("saldo = %v, esperado 12.5", got)
	}

	var quantity float64
	var note string
	var siteID int
	if err := database.DB.QueryRow(`
		SELECT quantidade, observacao, obra_id FROM movimentacoes
		WHERE produto_id = ? AND tipo = 'ENTRADA'
		ORDER BY id DESC LIMIT 1
	`, id).Scan(&quantity, &note, &siteID); err != nil {
		t.Fatalf("ler movimentação: %v", err)
	}
	if quantity != 2.5 || note != "NF 123" || siteID != central {
		t.Errorf("movimentação = (%v, %q, obra %d), esperado (2.5, \"NF 123\", obra %d)", quantity, note, siteID, central)
	}
}

func TestAddStockWebRejectsInvalidQuantity(t *testing.T) {
	userID := setupTestDB(t)
	central := centralID(t)
	id := createTestMaterial(t, userID, "Cimento", 10, 5)

	// 0,0001 arredonda para zero, então também tem que ser recusado.
	for _, quantity := range []float64{0, -1, 0.0001} {
		if err := AddStockWeb(id, central, quantity, userID, ""); err == nil {
			t.Errorf("entrada de %v deveria falhar", quantity)
		}
		if err := RegisterStockExitWeb(id, central, quantity, userID, ""); err == nil {
			t.Errorf("saída de %v deveria falhar", quantity)
		}
	}

	if got := balanceOf(t, id, central); got != 10 {
		t.Errorf("saldo mudou para %v, esperado 10", got)
	}
	// Só a entrada do cadastro, nenhuma outra movimentação.
	if got := movementCount(t, "ENTRADA"); got != 1 {
		t.Errorf("%d entradas registradas, esperado 1", got)
	}
	if got := movementCount(t, "SAIDA"); got != 0 {
		t.Errorf("%d saídas registradas, esperado 0", got)
	}
}

func TestStockOperationsRejectUnknownMaterialOrSite(t *testing.T) {
	userID := setupTestDB(t)
	central := centralID(t)
	id := createTestMaterial(t, userID, "Cimento", 10, 5)

	if err := AddStockWeb(9999, central, 1, userID, ""); err == nil {
		t.Error("entrada em material inexistente deveria falhar")
	}
	if err := RegisterStockExitWeb(9999, central, 1, userID, ""); err == nil {
		t.Error("saída de material inexistente deveria falhar")
	}
	if err := AddStockWeb(id, 9999, 1, userID, ""); err == nil {
		t.Error("entrada em obra inexistente deveria falhar")
	}
	if err := RegisterStockExitWeb(id, 0, 1, userID, ""); err == nil {
		t.Error("saída sem obra deveria falhar")
	}
	if got := balanceOf(t, id, central); got != 10 {
		t.Errorf("saldo mudou para %v, esperado 10", got)
	}
}

func TestRegisterStockExitWebNeverGoesNegative(t *testing.T) {
	userID := setupTestDB(t)
	central := centralID(t)
	id := createTestMaterial(t, userID, "Cimento", 5, 2)

	err := RegisterStockExitWeb(id, central, 6, userID, "")
	if !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("erro = %v, esperado ErrInsufficientStock", err)
	}
	if !strings.Contains(err.Error(), "5 saco") {
		t.Errorf("mensagem %q deveria dizer quanto há (5 saco)", err.Error())
	}

	if got := balanceOf(t, id, central); got != 5 {
		t.Errorf("saldo = %v, esperado 5", got)
	}
	if got := movementCount(t, "SAIDA"); got != 0 {
		t.Errorf("%d saídas registradas, esperado 0", got)
	}
}

func TestRegisterStockExitWebEmptiesStockWithDecimals(t *testing.T) {
	userID := setupTestDB(t)
	central := centralID(t)
	id := createTestMaterial(t, userID, "Brita", 1.1, 0)

	// Em float puro, 1,1 - 0,3 - 0,8 não dá zero exato. O arredondamento
	// tem que garantir que a última saída zere o estoque sem sobrar nada.
	for _, quantity := range []float64{0.3, 0.8} {
		if err := RegisterStockExitWeb(id, central, quantity, userID, "Laje 4"); err != nil {
			t.Fatalf("saída de %v falhou: %v", quantity, err)
		}
	}

	if got := balanceOf(t, id, central); got != 0 {
		t.Errorf("saldo = %v, esperado 0", got)
	}

	if err := RegisterStockExitWeb(id, central, 0.001, userID, ""); !errors.Is(err, ErrInsufficientStock) {
		t.Errorf("saída com saldo zerado: erro = %v, esperado ErrInsufficientStock", err)
	}
}

// O saldo é de cada obra: o que está no central não pode sair da obra A.
func TestStockIsSeparatedBySite(t *testing.T) {
	userID := setupTestDB(t)
	central := centralID(t)
	siteA := createTestSite(t, "Obra A", SiteStatusInProgress)
	id := createTestMaterial(t, userID, "Cimento", 100, 10)

	if err := AddStockWeb(id, siteA, 20, userID, ""); err != nil {
		t.Fatalf("entrada na obra A: %v", err)
	}
	if err := RegisterStockExitWeb(id, siteA, 15, userID, ""); err != nil {
		t.Fatalf("saída da obra A: %v", err)
	}

	err := RegisterStockExitWeb(id, siteA, 6, userID, "")
	if !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("saída acima do saldo da obra A: erro = %v, esperado ErrInsufficientStock", err)
	}
	if !strings.Contains(err.Error(), "5 saco") {
		t.Errorf("mensagem %q deveria falar do saldo da obra A (5 saco), não do central", err.Error())
	}

	// Saída de material que nunca entrou na obra também é recusada.
	siteB := createTestSite(t, "Obra B", SiteStatusInProgress)
	if err := RegisterStockExitWeb(id, siteB, 1, userID, ""); !errors.Is(err, ErrInsufficientStock) {
		t.Errorf("saída de obra sem saldo: erro = %v, esperado ErrInsufficientStock", err)
	}

	if got := balanceOf(t, id, central); got != 100 {
		t.Errorf("central = %v, esperado 100 (não pode ser afetado)", got)
	}
	if got := balanceOf(t, id, siteA); got != 5 {
		t.Errorf("obra A = %v, esperado 5", got)
	}
}

// Concluída é só consulta; paralisada continua aceitando movimentação.
func TestStockRespectsSiteStatus(t *testing.T) {
	userID := setupTestDB(t)
	paused := createTestSite(t, "Obra parada", SiteStatusPaused)
	finished := createTestSite(t, "Obra entregue", SiteStatusFinished)
	id := createTestMaterial(t, userID, "Cimento", 10, 5)

	if err := AddStockWeb(id, paused, 3, userID, ""); err != nil {
		t.Errorf("entrada em obra paralisada deveria passar: %v", err)
	}
	if err := RegisterStockExitWeb(id, paused, 1, userID, ""); err != nil {
		t.Errorf("saída de obra paralisada deveria passar: %v", err)
	}

	if err := AddStockWeb(id, finished, 3, userID, ""); !errors.Is(err, ErrSiteFinished) {
		t.Errorf("entrada em obra concluída: erro = %v, esperado ErrSiteFinished", err)
	}
	if err := RegisterStockExitWeb(id, finished, 1, userID, ""); !errors.Is(err, ErrSiteFinished) {
		t.Errorf("saída de obra concluída: erro = %v, esperado ErrSiteFinished", err)
	}
	if got := balanceOf(t, id, finished); got != 0 {
		t.Errorf("saldo da obra concluída = %v, esperado 0", got)
	}
}

func TestStockMovementLimitsNoteLength(t *testing.T) {
	userID := setupTestDB(t)
	central := centralID(t)
	id := createTestMaterial(t, userID, "Cimento", 10, 5)

	if err := AddStockWeb(id, central, 1, userID, strings.Repeat("ã", maxNoteLength+1)); err == nil {
		t.Error("observação acima do limite deveria falhar")
	}
	if err := AddStockWeb(id, central, 1, userID, strings.Repeat("ã", maxNoteLength)); err != nil {
		t.Errorf("observação no limite exato deveria passar: %v", err)
	}
}

func TestRemovedMaterialCannotBeMoved(t *testing.T) {
	userID := setupTestDB(t)
	central := centralID(t)
	id := createTestMaterial(t, userID, "Cimento", 10, 5)

	if err := DeleteMaterialWeb(id); err != nil {
		t.Fatalf("remover: %v", err)
	}
	if err := AddStockWeb(id, central, 1, userID, ""); err == nil {
		t.Error("entrada em material removido deveria falhar")
	}
	if err := RegisterStockExitWeb(id, central, 1, userID, ""); err == nil {
		t.Error("saída de material removido deveria falhar")
	}
}

// TestVerifyStockMigration monta um banco de antes das obras, migra e
// confere que a verificação não acha divergência. Depois, uma saída e um
// material novo (que só existem nos saldos) passam a divergir.
func TestVerifyStockMigration(t *testing.T) {
	t.Setenv("DB_PATH", filepath.Join(t.TempDir(), "teste.db"))
	if err := database.Connect(); err != nil {
		t.Fatalf("conectar ao banco de teste: %v", err)
	}
	t.Cleanup(func() { database.DB.Close() })

	if _, err := database.DB.Exec(`
		CREATE TABLE produtos (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			nome TEXT NOT NULL,
			quantidade INTEGER NOT NULL,
			ativo INTEGER NOT NULL DEFAULT 1
		);
		CREATE TABLE movimentacoes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			produto_id INTEGER NOT NULL,
			usuario_id INTEGER NOT NULL,
			tipo TEXT NOT NULL,
			quantidade INTEGER NOT NULL,
			data DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO produtos (nome, quantidade, ativo) VALUES
			('Cimento', 40, 1), ('Areia', 2.5, 1), ('Brita', 0, 1), ('Cal antiga', 7, 0);
	`); err != nil {
		t.Fatalf("montar banco antigo: %v", err)
	}
	if err := database.CreateTables(); err != nil {
		t.Fatalf("migrar: %v", err)
	}

	divergences, checked, err := VerifyStockMigration()
	if err != nil {
		t.Fatal(err)
	}
	if checked != 4 || len(divergences) != 0 {
		t.Fatalf("logo depois da migração: %d conferidos e divergências %+v, esperado 4 e nenhuma", checked, divergences)
	}

	result, err := database.DB.Exec(`INSERT INTO usuarios (nome, email, senha, role) VALUES ('Teste', 'teste@empresa.com', 'x', 'admin')`)
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := result.LastInsertId()
	central := centralID(t)
	if err := RegisterStockExitWeb(1, central, 0.5, int(userID), ""); err != nil {
		t.Fatal(err)
	}
	if err := CreateMaterialWeb("Tijolo", 100, "milheiro", 1, central, int(userID)); err != nil {
		t.Fatal(err)
	}

	divergences, checked, err = VerifyStockMigration()
	if err != nil {
		t.Fatal(err)
	}
	if checked != 5 || len(divergences) != 2 {
		t.Fatalf("depois de usar: %d conferidos e %d divergências (%+v), esperado 5 e 2", checked, len(divergences), divergences)
	}
	cement, brick := divergences[0], divergences[1]
	if cement.Name != "Cimento" || cement.OldQuantity != 40 || cement.SiteTotal != 39.5 || !cement.Active {
		t.Errorf("divergência do cimento = %+v", cement)
	}
	if brick.Name != "Tijolo" || brick.OldQuantity != 0 || brick.SiteTotal != 100 {
		t.Errorf("divergência do tijolo = %+v", brick)
	}
}
