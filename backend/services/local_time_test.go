package services

import (
	"database/sql"
	"testing"
	"time"

	database "uchoastock/backend/database"
)

// useBrasiliaTime faz o teste rodar no fuso de Brasília, como o servidor
// (SetupTimezone), e devolve o fuso anterior no fim.
func useBrasiliaTime(t *testing.T) {
	t.Helper()
	previous := time.Local
	if err := SetupTimezone(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { time.Local = previous })
}

func TestDayStartUTC(t *testing.T) {
	useBrasiliaTime(t)

	// Brasília é UTC-3: o dia 18 começa às 03:00 em UTC.
	if got, err := dayStartUTC("2026-09-18", 0); err != nil || got != "2026-09-18 03:00:00" {
		t.Errorf("início do dia 18 = %q (%v), esperado 2026-09-18 03:00:00", got, err)
	}
	if got, _ := dayStartUTC("2026-09-18", 1); got != "2026-09-19 03:00:00" {
		t.Errorf("fim do dia 18 = %q, esperado 2026-09-19 03:00:00", got)
	}
	if _, err := dayStartUTC("18/09/2026", 0); err == nil {
		t.Error("data fora do formato deveria dar erro")
	}
}

func TestFormatDBTime(t *testing.T) {
	useBrasiliaTime(t)

	cases := []struct {
		raw  sql.NullString
		want string
	}{
		// 02:00 em UTC ainda é o dia anterior no Brasil.
		{sql.NullString{String: "2026-09-18 02:00:00", Valid: true}, "17/09/2026 23:00"},
		{sql.NullString{String: "2026-09-18T15:30:00Z", Valid: true}, "18/09/2026 12:30"},
		{sql.NullString{}, ""},
		{sql.NullString{String: "lixo", Valid: true}, ""},
	}
	for _, c := range cases {
		if got := formatDBTime(c.raw, "02/01/2006 15:04"); got != c.want {
			t.Errorf("formatDBTime(%q) = %q, esperado %q", c.raw.String, got, c.want)
		}
	}
}

// O "hoje" do cartão e o filtro por dia seguem o calendário de Brasília,
// não o dia em UTC: uma saída às 23h de ontem (02h de hoje em UTC) é de
// ontem.
func TestTodayFollowsLocalCalendar(t *testing.T) {
	useBrasiliaTime(t)
	userID := setupTestDB(t)
	central := centralID(t)
	cement := createTestMaterial(t, userID, "Cimento", 100, 10)
	if err := RegisterStockExitWeb(cement, central, 1, userID, "hoje"); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStockExitWeb(cement, central, 1, userID, "ontem"); err != nil {
		t.Fatal(err)
	}

	// Uma hora antes da meia-noite de hoje (horário de Brasília), em UTC.
	today := time.Now().In(time.Local).Format("2006-01-02")
	midnight, _ := dayStartUTC(today, 0)
	start, _ := time.Parse(dbTimeLayout, midnight)
	lastNight := start.Add(-time.Hour).Format(dbTimeLayout)
	if _, err := database.DB.Exec(`UPDATE movimentacoes SET data = ? WHERE observacao = 'ontem'`, lastNight); err != nil {
		t.Fatal(err)
	}
	// A entrada do cadastro também vai para ontem, para o cartão contar só
	// a saída de hoje.
	if _, err := database.DB.Exec(`UPDATE movimentacoes SET data = ? WHERE tipo = 'ENTRADA'`, lastNight); err != nil {
		t.Fatal(err)
	}

	entries, exits, err := CountTodayMovements(0)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 0 || exits != 1 {
		t.Errorf("hoje = %d entradas e %d saídas, esperado 0 e 1", entries, exits)
	}

	yesterday := time.Now().In(time.Local).AddDate(0, 0, -1).Format("2006-01-02")
	list, err := GetMovementsFilteredWeb(MovementFilter{Type: "SAIDA", From: yesterday, To: yesterday})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Note != "ontem" {
		t.Errorf("saídas de ontem = %+v, esperado só a de ontem", list)
	}
	if len(list) == 1 && list[0].FormattedTime != "23:00" {
		t.Errorf("hora da saída de ontem = %q, esperado 23:00 (horário de Brasília)", list[0].FormattedTime)
	}
}
