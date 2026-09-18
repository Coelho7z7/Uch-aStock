package main

import (
	"strings"
	"testing"

	"uchoastock/backend/services"
)

// TestReportPermission confere quem entra no relatório: quem pode ver as
// movimentações de todos abre a tela; o solicitante, que só vê as próprias,
// é barrado e não recebe nem a aba na barra lateral.
func TestReportPermission(t *testing.T) {
	f := setupIsolation(t)

	if err := services.CreateUserWeb("Pedreiro A", "pedreiro.a@empresa.com", "senha!123", "solicitante", f.siteA); err != nil {
		t.Fatal(err)
	}
	requesterID := queryID(t, `SELECT id FROM usuarios WHERE email = 'pedreiro.a@empresa.com'`)
	requesterToken, err := createSession(requesterID)
	if err != nil {
		t.Fatal(err)
	}

	for _, allowed := range []struct {
		who   string
		token string
	}{
		{"admin", f.adminToken},
		{"gestor", f.managerToken},
		{"almoxarife", f.storekeeperToken},
	} {
		page := get(allowed.token, reportHandler, "/relatorios")
		if !strings.Contains(page, "Consumo de materiais") {
			t.Errorf("%s não abriu o relatório", allowed.who)
		}
		if !strings.Contains(page, `href="/relatorios"`) {
			t.Errorf("%s não recebeu a aba Relatórios", allowed.who)
		}
	}

	if denied := get(requesterToken, reportHandler, "/relatorios"); !strings.Contains(denied, "Ação bloqueada") {
		t.Error("o solicitante abriu o relatório")
	}
	if dashboard := get(requesterToken, dashboardHandler, "/dashboard"); strings.Contains(dashboard, `href="/relatorios"`) {
		t.Error("a aba Relatórios apareceu para o solicitante")
	}
}

// TestReportShowsPeriodConsumption confere o que a tela mostra: o consumo
// da obra selecionada, o comparativo só na visão de todas as obras, e o
// período pedido na URL.
func TestReportShowsPeriodConsumption(t *testing.T) {
	f := setupIsolation(t)

	adminID := queryID(t, `SELECT id FROM usuarios WHERE email = 'admin.teste@empresa.com'`)
	if err := services.RegisterStockExitWeb(f.materialA, f.siteA, 2, adminID, "Fundação"); err != nil {
		t.Fatal(err)
	}

	selectSite(t, f.adminToken, f.siteA)
	page := get(f.adminToken, reportHandler, "/relatorios")
	if !strings.Contains(page, "Areia A") {
		t.Error("o material movimentado não apareceu no relatório da obra")
	}
	if strings.Contains(page, "Comparativo entre obras") {
		t.Error("o comparativo apareceu com uma obra escolhida")
	}

	selectSite(t, f.adminToken, 0)
	all := get(f.adminToken, reportHandler, "/relatorios")
	if !strings.Contains(all, "Comparativo entre obras") {
		t.Error("o comparativo não apareceu na visão de todas as obras")
	}
	if !strings.Contains(all, "Obra B") {
		t.Error("a obra sem movimentação ficou fora do comparativo")
	}

	// Período que termina antes do sistema existir: a tela abre vazia, e
	// não com o total de sempre.
	empty := get(f.adminToken, reportHandler, "/relatorios?de=2020-01-01&ate=2020-01-31")
	if !strings.Contains(empty, "Nenhuma entrada ou saída nesse período.") {
		t.Error("o período antigo trouxe movimentação")
	}
	if !strings.Contains(empty, "de 01/01/2020 a 31/01/2020") {
		t.Error("o cabeçalho não mostrou o período pedido")
	}

	// Data inválida na URL não quebra a consulta: vira aviso na tela.
	broken := get(f.adminToken, reportHandler, "/relatorios?de=31/01/2020")
	if !strings.Contains(broken, "Data inicial inválida.") {
		t.Error("data inválida não virou aviso")
	}
}
