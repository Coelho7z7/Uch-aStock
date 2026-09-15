package services

import (
	"testing"
	"time"
)

func TestGetMovementsFilteredWeb(t *testing.T) {
	userID := setupTestDB(t)
	cement := createTestMaterial(t, userID, "Cimento", 10, 5)
	createTestMaterial(t, userID, "Areia", 5, 2)
	if err := RegisterStockExitWeb(cement, 2, userID, "Obra A"); err != nil {
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
	if exit.Material != "Cimento" || exit.Note != "Obra A" || exit.FormattedQuantity != "2" || exit.Unit != "saco" {
		t.Errorf("saída = %+v", exit)
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
}
