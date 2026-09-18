package services

import (
	"testing"
	"time"

	database "uchoastock/backend/database"
)

func TestGetMovementsFilteredWeb(t *testing.T) {
	userID := setupTestDB(t)
	central := centralID(t)
	cement := createTestMaterial(t, userID, "Cimento", 10, 5)
	createTestMaterial(t, userID, "Areia", 5, 2)
	if err := RegisterStockExitWeb(cement, central, 2, userID, "Laje 4"); err != nil {
		t.Fatal(err)
	}

	count := func(filter MovementFilter) int {
		t.Helper()
		movements, err := GetMovementsFilteredWeb(filter)
		if err != nil {
			t.Fatalf("filtro %+v: %v", filter, err)
		}
		return len(movements)
	}

	// Duas entradas de cadastro e uma saída.
	if got := count(MovementFilter{}); got != 3 {
		t.Errorf("sem filtro = %d, esperado 3", got)
	}

	exits, err := GetMovementsFilteredWeb(MovementFilter{Type: "SAIDA"})
	if err != nil {
		t.Fatal(err)
	}
	if len(exits) != 1 {
		t.Fatalf("saídas = %d, esperado 1", len(exits))
	}
	exit := exits[0]
	if exit.Material != "Cimento" || exit.Note != "Laje 4" || exit.FormattedQuantity != "2" || exit.Unit != "saco" {
		t.Errorf("saída = %+v", exit)
	}
	if exit.SiteID != central || exit.SiteName != "Almoxarifado central" {
		t.Errorf("obra da saída = %d/%q, esperado %d/Almoxarifado central", exit.SiteID, exit.SiteName, central)
	}

	if got := count(MovementFilter{Material: "are"}); got != 1 {
		t.Errorf("filtro por material = %d, esperado 1", got)
	}

	// Tipo inventado é ignorado, não zera o histórico.
	if got := count(MovementFilter{Type: "VENDA"}); got != 3 {
		t.Errorf("tipo inválido = %d, esperado 3", got)
	}

	now := time.Now()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")

	if got := count(MovementFilter{From: today}); got != 3 {
		t.Errorf("a partir de hoje = %d, esperado 3", got)
	}
	if got := count(MovementFilter{From: tomorrow}); got != 0 {
		t.Errorf("a partir de amanhã = %d, esperado 0", got)
	}
	if got := count(MovementFilter{To: yesterday}); got != 0 {
		t.Errorf("até ontem = %d, esperado 0", got)
	}
	if got := count(MovementFilter{From: yesterday, To: today}); got != 3 {
		t.Errorf("de ontem até hoje = %d, esperado 3", got)
	}

	// Filtro por usuário: só o que cada um registrou.
	result, err := database.DB.Exec(`
		INSERT INTO usuarios (nome, email, senha, role)
		VALUES ('Solicitante', 'solicitante@gmail.com', 'x', 'solicitante')
	`)
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterStockExitWeb(cement, central, 1, int(otherID), "Retirada"); err != nil {
		t.Fatal(err)
	}
	if got := count(MovementFilter{UserID: int(otherID)}); got != 1 {
		t.Errorf("movimentações do solicitante = %d, esperado 1", got)
	}
	if got := count(MovementFilter{UserID: userID}); got != 3 {
		t.Errorf("movimentações do usuário de teste = %d, esperado 3", got)
	}
	if got := count(MovementFilter{UserID: int(otherID), Type: "ENTRADA"}); got != 0 {
		t.Errorf("entradas do solicitante = %d, esperado 0", got)
	}
	if got := count(MovementFilter{}); got != 4 {
		t.Errorf("sem filtro de usuário = %d, esperado 4", got)
	}
}

// Cada obra vê só o que aconteceu nela; atualização de cadastro (sem obra)
// aparece apenas na visão de todas as obras.
func TestMovementsAndSummaryFilterBySite(t *testing.T) {
	userID := setupTestDB(t)
	central := centralID(t)
	siteA := createTestSite(t, "Obra A", SiteStatusInProgress)
	cement := createTestMaterial(t, userID, "Cimento", 100, 10) // entrada no central
	if err := AddStockWeb(cement, siteA, 8, userID, ""); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStockExitWeb(cement, siteA, 8, userID, ""); err != nil {
		t.Fatal(err)
	}
	if err := UpdateMaterialWeb(cement, "Cimento CP-II", "saco", 10, userID); err != nil {
		t.Fatal(err)
	}

	count := func(siteID int) int {
		t.Helper()
		list, err := GetMovementsFilteredWeb(MovementFilter{SiteID: siteID})
		if err != nil {
			t.Fatal(err)
		}
		return len(list)
	}
	if got := count(0); got != 4 {
		t.Errorf("todas = %d, esperado 4", got)
	}
	if got := count(central); got != 1 {
		t.Errorf("central = %d, esperado 1", got)
	}
	if got := count(siteA); got != 2 {
		t.Errorf("obra A = %d, esperado 2", got)
	}

	summaryA, err := GetDashboardSummary(siteA)
	if err != nil {
		t.Fatal(err)
	}
	want := DashboardSummary{TotalMaterials: 1, EmptyStock: 1, LowStock: 1, TotalMovements: 2, TodayEntries: 1, TodayExits: 1}
	if summaryA != want {
		t.Errorf("resumo da obra A = %+v, esperado %+v", summaryA, want)
	}

	summaryAll, err := GetDashboardSummary(0)
	if err != nil {
		t.Fatal(err)
	}
	want = DashboardSummary{TotalMaterials: 1, EmptyStock: 1, LowStock: 1, TotalMovements: 4, TodayEntries: 2, TodayExits: 1}
	if summaryAll != want {
		t.Errorf("resumo geral = %+v, esperado %+v", summaryAll, want)
	}
}

func TestSessionSite(t *testing.T) {
	userID := setupTestDB(t)
	siteA := createTestSite(t, "Obra A", SiteStatusInProgress)
	if _, err := database.DB.Exec(`
		INSERT INTO sessoes (usuario_id, token_hash, expira_em)
		VALUES (?, 'hash-teste', datetime('now', '+1 day'))
	`, userID); err != nil {
		t.Fatal(err)
	}

	get := func() int {
		t.Helper()
		id, err := GetSessionSiteID("hash-teste")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	if got := get(); got != 0 {
		t.Errorf("sessão nova = %d, esperado 0 (nenhuma obra)", got)
	}
	if err := SetSessionSite("hash-teste", siteA); err != nil {
		t.Fatal(err)
	}
	if got := get(); got != siteA {
		t.Errorf("depois de escolher = %d, esperado %d", got, siteA)
	}
	if err := SetSessionSite("hash-teste", 0); err != nil {
		t.Fatal(err)
	}
	if got := get(); got != 0 {
		t.Errorf("depois de voltar para todas = %d, esperado 0", got)
	}
	if err := SetSessionSite("hash-inexistente", siteA); err == nil {
		t.Error("sessão inexistente deveria falhar")
	}
	// A chave estrangeira impede guardar uma obra que não existe.
	if err := SetSessionSite("hash-teste", 9999); err == nil {
		t.Error("obra inexistente deveria falhar")
	}
}
