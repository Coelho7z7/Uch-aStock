package services

import (
	"testing"
	"time"

	database "uchoastock/backend/database"
)

// TestReportConsumption confere as três consultas do relatório: o resumo,
// o consumo por material e o comparativo entre obras.
func TestReportConsumption(t *testing.T) {
	userID := setupTestDB(t)
	central := centralID(t)
	quiet := createTestSite(t, "Obra Parada", SiteStatusInProgress)

	// O cadastro do material já registra uma entrada ("Estoque inicial").
	cement := createTestMaterial(t, userID, "Cimento", 100, 10)
	sand := createTestMaterial(t, userID, "Areia", 20, 5)

	if err := AddStockWeb(cement, central, 50, userID, "Nota 123"); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStockExitWeb(cement, central, 30, userID, "Laje"); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStockExitWeb(cement, central, 20, userID, "Contrapiso"); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStockExitWeb(sand, central, 5, userID, ""); err != nil {
		t.Fatal(err)
	}
	// Atualização de cadastro não é material entrando nem saindo: não pode
	// aparecer em nenhum número do relatório.
	if err := UpdateMaterialWeb(sand, "Areia média", "m³", 5, userID); err != nil {
		t.Fatal(err)
	}

	summary, err := GetReportSummary(ReportFilter{})
	if err != nil {
		t.Fatal(err)
	}
	// 2 entradas de cadastro + 1 reposição; 3 saídas; 2 materiais.
	if summary.Entries != 3 || summary.Exits != 3 || summary.Materials != 2 {
		t.Errorf("resumo = %+v, esperado 3 entradas, 3 saídas e 2 materiais", summary)
	}

	materials, err := GetMaterialConsumption(ReportFilter{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(materials) != 2 {
		t.Fatalf("materiais = %d, esperado 2", len(materials))
	}

	// O que mais saiu vem primeiro: cimento (50) antes da areia (5).
	cementLine := materials[0]
	if cementLine.Material != "Cimento" {
		t.Fatalf("primeiro material = %q, esperado Cimento", cementLine.Material)
	}
	if cementLine.Entered != 150 || cementLine.Exited != 50 || cementLine.Balance != 100 {
		t.Errorf("cimento = entrou %v, saiu %v, saldo %v; esperado 150/50/100",
			cementLine.Entered, cementLine.Exited, cementLine.Balance)
	}
	if cementLine.ExitCount != 2 {
		t.Errorf("saídas de cimento = %d, esperado 2", cementLine.ExitCount)
	}
	if cementLine.FormattedEntered != "150" || cementLine.FormattedBalance != "100" {
		t.Errorf("cimento formatado = %q e %q", cementLine.FormattedEntered, cementLine.FormattedBalance)
	}

	// O limite corta a lista no topo, sem mudar a ordem.
	top, err := GetMaterialConsumption(ReportFilter{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 || top[0].Material != "Cimento" {
		t.Errorf("com limite 1 = %+v, esperado só o cimento", top)
	}

	// Obra sem movimentação: o relatório fica zerado, não some.
	empty, err := GetReportSummary(ReportFilter{SiteID: quiet})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Entries != 0 || empty.Exits != 0 || empty.Materials != 0 {
		t.Errorf("obra parada = %+v, esperado tudo zero", empty)
	}

	sites, err := GetSiteConsumption(ReportFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 {
		t.Fatalf("obras no comparativo = %d, esperado 2", len(sites))
	}
	if sites[0].SiteID != central || sites[0].Entries != 3 || sites[0].Exits != 3 || sites[0].Materials != 2 {
		t.Errorf("central no comparativo = %+v, esperado 3/3/2", sites[0])
	}
	// A obra sem movimentação precisa aparecer zerada: é o que o LEFT JOIN
	// com as condições no ON garante.
	if sites[1].SiteID != quiet || sites[1].Entries != 0 || sites[1].Exits != 0 || sites[1].Materials != 0 {
		t.Errorf("obra parada no comparativo = %+v, esperado zerada", sites[1])
	}
}

// TestReportPeriod confere o filtro de período e o saldo negativo: quando
// a entrada ficou fora da janela, o relatório mostra que a obra gastou o
// que já estava no estoque.
func TestReportPeriod(t *testing.T) {
	userID := setupTestDB(t)
	central := centralID(t)
	cement := createTestMaterial(t, userID, "Cimento", 100, 10)
	if err := RegisterStockExitWeb(cement, central, 40, userID, "Laje"); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")

	if summary, err := GetReportSummary(ReportFilter{From: tomorrow}); err != nil {
		t.Fatal(err)
	} else if summary.Entries != 0 || summary.Exits != 0 {
		t.Errorf("a partir de amanhã = %+v, esperado zero", summary)
	}
	if summary, err := GetReportSummary(ReportFilter{To: yesterday}); err != nil {
		t.Fatal(err)
	} else if summary.Entries != 0 || summary.Exits != 0 {
		t.Errorf("até ontem = %+v, esperado zero", summary)
	}

	// Joga a entrada do cadastro para dez dias atrás, deixando só a saída
	// dentro do período de hoje. É um UPDATE no banco temporário do teste,
	// que não existe fora dele.
	if _, err := database.DB.Exec(`
		UPDATE movimentacoes SET data = datetime('now', '-10 days') WHERE tipo = 'ENTRADA'
	`); err != nil {
		t.Fatal(err)
	}

	materials, err := GetMaterialConsumption(ReportFilter{From: today}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(materials) != 1 {
		t.Fatalf("materiais de hoje = %d, esperado 1", len(materials))
	}
	line := materials[0]
	if line.Entered != 0 || line.Exited != 40 || line.Balance != -40 {
		t.Errorf("cimento hoje = entrou %v, saiu %v, saldo %v; esperado 0/40/-40",
			line.Entered, line.Exited, line.Balance)
	}

	// A entrada antiga continua contando quando o período a alcança.
	summary, err := GetReportSummary(ReportFilter{From: now.AddDate(0, 0, -30).Format("2006-01-02"), To: today})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Entries != 1 || summary.Exits != 1 {
		t.Errorf("últimos 30 dias = %+v, esperado 1 entrada e 1 saída", summary)
	}
}
