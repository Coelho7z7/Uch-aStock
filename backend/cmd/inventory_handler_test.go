package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"uchoastock/backend/models"
	"uchoastock/backend/services"
)

// inventoryCall chama um handler de /inventarios/{id} como o roteador de
// verdade faria (o httptest não preenche o {id}: SetPathValue faz isso).
func inventoryCall(token string, handler authenticatedHandler, method string, id int, path string, form url.Values) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	request := httptest.NewRequest(method, path, body)
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	request.SetPathValue("id", fmt.Sprint(id))
	request.AddCookie(&http.Cookie{Name: "sessao", Value: token})
	recorder := httptest.NewRecorder()
	withUser(handler).ServeHTTP(recorder, request)
	return recorder
}

// TestInventoryOverHTTP percorre o inventário pelas telas de verdade (o que
// também confere que os templates renderizam): o almoxarife inicia e conta,
// o gestor aprova, e quem não pode é barrado.
func TestInventoryOverHTTP(t *testing.T) {
	f := setupIsolation(t)

	if err := services.CreateUserWeb("Pedreiro A", "pedreiro.a@empresa.com", "senha!123", "solicitante", f.siteA); err != nil {
		t.Fatal(err)
	}
	requesterToken, err := createSession(queryID(t, `SELECT id FROM usuarios WHERE email = 'pedreiro.a@empresa.com'`))
	if err != nil {
		t.Fatal(err)
	}

	// Solicitante não vê a aba nem entra na rota.
	if page := get(requesterToken, inventoryListHandler, "/inventarios"); !strings.Contains(page, "Ação bloqueada") {
		t.Error("o solicitante abriu a lista de inventários")
	}
	if page := get(requesterToken, dashboardHandler, "/dashboard"); strings.Contains(page, `href="/inventarios"`) {
		t.Error("a aba Inventários apareceu para o solicitante")
	}

	list := get(f.storekeeperToken, inventoryListHandler, "/inventarios")
	if !strings.Contains(list, "Iniciar inventário") || !strings.Contains(list, "Como funciona") {
		t.Fatal("a lista não mostrou o botão de iniciar e o Como funciona")
	}

	// O almoxarife da obra A não abre inventário na obra B.
	response := post(f.storekeeperToken, inventoryListHandler, "/inventarios", url.Values{"acao": {"iniciar"}, "obra": {fmt.Sprint(f.siteB)}})
	if response.Code != http.StatusForbidden {
		t.Errorf("almoxarife A iniciando na obra B: status %d, esperado 403", response.Code)
	}

	response = post(f.storekeeperToken, inventoryListHandler, "/inventarios", url.Values{"acao": {"iniciar"}, "obra": {fmt.Sprint(f.siteA)}})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("iniciar: status %d, esperado 303", response.Code)
	}
	id := queryID(t, `SELECT id FROM inventarios WHERE obra_id = `+fmt.Sprint(f.siteA))
	itemID := queryID(t, fmt.Sprintf(`SELECT id FROM inventario_itens WHERE inventario_id = %d AND produto_id = %d`, id, f.materialA))
	detailPath := fmt.Sprintf("/inventarios/%d", id)

	// A tela de estoque avisa do bloqueio.
	if stock := get(f.storekeeperToken, stockHandler, "/estoque"); !strings.Contains(stock, "Obra em inventário") {
		t.Error("a tela de estoque não avisou que a obra está em inventário")
	}

	detail := inventoryCall(f.storekeeperToken, inventoryDetailHandler, http.MethodGet, id, detailPath, nil)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), fmt.Sprintf(`name="contado_%d"`, itemID)) {
		t.Fatalf("detalhe para o almoxarife: status %d, sem o campo de contagem", detail.Code)
	}

	// Quantidade inválida volta com a mensagem e o que foi digitado.
	response = inventoryCall(f.storekeeperToken, inventoryDetailHandler, http.MethodPost, id, detailPath, url.Values{
		"acao": {"salvar"}, fmt.Sprintf("contado_%d", itemID): {"dois"},
	})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Quantidade contada inválida") ||
		!strings.Contains(response.Body.String(), `value="dois"`) {
		t.Errorf("quantidade inválida: status %d, esperado 400 com a mensagem e o valor digitado", response.Code)
	}

	// Areia A tinha 3; contou 2. Sem JS, Enviar manda acao=salvar e enviar=1.
	response = inventoryCall(f.storekeeperToken, inventoryDetailHandler, http.MethodPost, id, detailPath, url.Values{
		"acao": {"salvar"}, "enviar": {"1"},
		fmt.Sprintf("contado_%d", itemID):       {"2"},
		fmt.Sprintf("justificativa_%d", itemID): {"um saco molhado"},
	})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("enviar: status %d, esperado 303: %s", response.Code, response.Body.String())
	}

	// O almoxarife vê, mas não aprova.
	if page := inventoryCall(f.storekeeperToken, inventoryDetailHandler, http.MethodGet, id, detailPath, nil).Body.String(); strings.Contains(page, `value="aprovar"`) {
		t.Error("o almoxarife recebeu o botão de aprovar")
	}
	response = inventoryCall(f.storekeeperToken, inventoryDetailHandler, http.MethodPost, id, detailPath, url.Values{"acao": {"aprovar"}})
	if response.Code != http.StatusForbidden {
		t.Errorf("almoxarife aprovando: status %d, esperado 403", response.Code)
	}

	managerPage := inventoryCall(f.managerToken, inventoryDetailHandler, http.MethodGet, id, detailPath, nil).Body.String()
	if !strings.Contains(managerPage, "Aprovar ajuste") {
		t.Fatal("o gestor não recebeu o botão de aprovar")
	}
	response = inventoryCall(f.managerToken, inventoryDetailHandler, http.MethodPost, id, detailPath, url.Values{"acao": {"aprovar"}})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("gestor aprovando: status %d, esperado 303", response.Code)
	}
	if got := queryID(t, fmt.Sprintf(`SELECT CAST(quantidade AS INTEGER) FROM saldos WHERE produto_id = %d AND obra_id = %d`, f.materialA, f.siteA)); got != 2 {
		t.Errorf("saldo depois do ajuste = %d, esperado 2", got)
	}

	// O ajuste aparece nas movimentações e no dashboard.
	if page := get(f.managerToken, movementHandler, "/movimentacoes?tipo=AJUSTE"); !strings.Contains(page, "activity-AJUSTE") || !strings.Contains(page, "-1") {
		t.Error("as movimentações não mostraram o ajuste de -1")
	}
	if page := get(f.managerToken, dashboardHandler, "/dashboard"); !strings.Contains(page, ">Ajuste<") {
		t.Error("o dashboard não mostrou o ajuste na atividade recente")
	}

	// Exportação da contagem.
	csv := inventoryCall(f.managerToken, inventoryExportHandler, http.MethodGet, id, detailPath+"/exportar", nil)
	if csv.Code != http.StatusOK || !strings.Contains(csv.Header().Get("Content-Type"), "text/csv") ||
		!strings.Contains(csv.Body.String(), "Areia A;") || !strings.Contains(csv.Body.String(), "um saco molhado") {
		t.Errorf("exportar: status %d, tipo %q, corpo %q", csv.Code, csv.Header().Get("Content-Type"), csv.Body.String())
	}

	// Inventário de outra obra é "não encontrado" para o gestor da obra A.
	idB, err := services.StartInventory(inventoryActor(mustUserByToken(t, f.adminToken)), f.siteB)
	if err != nil {
		t.Fatal(err)
	}
	if response := inventoryCall(f.managerToken, inventoryDetailHandler, http.MethodGet, idB, fmt.Sprintf("/inventarios/%d", idB), nil); response.Code != http.StatusNotFound {
		t.Errorf("gestor A abrindo inventário da obra B: status %d, esperado 404", response.Code)
	}
	if page := get(f.managerToken, inventoryListHandler, "/inventarios"); strings.Contains(page, services.InventoryCode(idB)) {
		t.Error("o inventário da obra B apareceu na lista do gestor A")
	}
}

// mustUserByToken devolve o usuário da sessão, para montar o actor.
func mustUserByToken(t *testing.T, token string) *models.User {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: "sessao", Value: token})
	user, ok := loggedUser(request)
	if !ok || user == nil {
		t.Fatal("sessão inválida")
	}
	return user
}
