package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	database "uchoastock/backend/database"
	"uchoastock/backend/services"
)

// requestHTTPFixture acrescenta ao banco de isolamento (obras A e B) as
// pessoas e requisições que os testes de requisição usam.
type requestHTTPFixture struct {
	isolationFixture
	requesterToken      string // solicitante da obra A
	otherRequesterToken string // outro solicitante da obra A
	pendingB            int    // requisição pendente da obra B
	approvedB           int    // requisição aprovada da obra B
	approvedBItem       int    // item da requisição aprovada da obra B
	colleagueA          int    // requisição pendente do outro solicitante da obra A
}

func setupRequestHTTP(t *testing.T) requestHTTPFixture {
	t.Helper()
	f := requestHTTPFixture{isolationFixture: setupIsolation(t)}

	newUser := func(name, email, role string, site int) int {
		if err := services.CreateUserWeb(name, email, "senha!123", role, site); err != nil {
			t.Fatalf("criar %s: %v", email, err)
		}
		return queryID(t, fmt.Sprintf(`SELECT id FROM usuarios WHERE email = '%s'`, email))
	}
	token := func(userID int) string {
		value, err := createSession(userID)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	requesterID := newUser("Solic A", "solic.a@empresa.com", "solicitante", f.siteA)
	otherRequesterID := newUser("Solic Dois", "solic.dois@empresa.com", "solicitante", f.siteA)
	managerBID := newUser("Gestor B", "gestor.b@empresa.com", "gestor", f.siteB)
	f.requesterToken = token(requesterID)
	f.otherRequesterToken = token(otherRequesterID)

	actorFor := func(userID int) services.RequestActor {
		user, err := services.GetUserByID(userID)
		if err != nil {
			t.Fatal(err)
		}
		return requestActor(user)
	}
	create := func(userID, siteID, material int) int {
		id, err := services.CreateRequest(actorFor(userID), siteID, "Bloco B - laje", []services.RequestItemInput{{MaterialID: material, Quantity: 2}})
		if err != nil {
			t.Fatalf("criar requisição: %v", err)
		}
		return id
	}

	f.pendingB = create(managerBID, f.siteB, f.materialB)
	f.approvedB = create(f.storekeeperB, f.siteB, f.materialB)
	if err := services.ApproveRequest(actorFor(managerBID), f.approvedB); err != nil {
		t.Fatal(err)
	}
	f.approvedBItem = queryID(t, fmt.Sprintf(`SELECT id FROM requisicao_itens WHERE requisicao_id = %d`, f.approvedB))
	f.colleagueA = create(otherRequesterID, f.siteA, f.materialA)
	return f
}

// requestCall chama o handler de detalhe como a rota /requisicoes/{id}
// chamaria, com a sessão do token.
func requestCall(token, method string, requestID int, form url.Values) *httptest.ResponseRecorder {
	path := fmt.Sprintf("/requisicoes/%d", requestID)
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
	request.SetPathValue("id", fmt.Sprint(requestID))
	request.AddCookie(&http.Cookie{Name: "sessao", Value: token})
	recorder := httptest.NewRecorder()
	withUser(requestDetailHandler).ServeHTTP(recorder, request)
	return recorder
}

// TestRequestIsolationBetweenSites: gestor, almoxarife e solicitante da
// obra A tentam ver e agir em requisições da obra B pelo endereço direto.
// Todos recebem 404 (sem saber se a requisição existe), nada aparece na
// lista deles e o banco não muda.
func TestRequestIsolationBetweenSites(t *testing.T) {
	f := setupRequestHTTP(t)

	attackers := map[string]string{
		"gestor da obra A":      f.managerToken,
		"almoxarife da obra A":  f.storekeeperToken,
		"solicitante da obra A": f.requesterToken,
	}
	for who, token := range attackers {
		for _, id := range []int{f.pendingB, f.approvedB} {
			if response := requestCall(token, http.MethodGet, id, nil); response.Code != http.StatusNotFound {
				t.Errorf("%s abrindo a requisição #%d da obra B: status %d, esperado 404", who, id, response.Code)
			}
		}

		// Mesmo escolhendo a obra B no seletor, a lista continua só da obra A.
		selectSite(t, token, f.siteB)
		list := get(token, requestListHandler, "/requisicoes")
		for _, id := range []int{f.pendingB, f.approvedB} {
			if strings.Contains(list, fmt.Sprintf(`href="/requisicoes/%d"`, id)) {
				t.Errorf("%s vê a requisição #%d da obra B na lista", who, id)
			}
		}
		selectSite(t, token, f.siteA)

		actions := []struct {
			name string
			id   int
			form url.Values
		}{
			{"aprovar", f.pendingB, url.Values{"acao": {"aprovar"}}},
			{"rejeitar", f.pendingB, url.Values{"acao": {"rejeitar"}, "motivo": {"invasão"}}},
			{"cancelar", f.pendingB, url.Values{"acao": {"cancelar"}}},
			{"cancelar aprovada", f.approvedB, url.Values{"acao": {"cancelar"}}},
			{"atender", f.approvedB, url.Values{"acao": {"atender"}, fmt.Sprintf("entrega_%d", f.approvedBItem): {"1"}}},
		}
		for _, a := range actions {
			before := snapshot(t)
			response := requestCall(token, http.MethodPost, a.id, a.form)
			if response.Code != http.StatusNotFound {
				t.Errorf("%s: %s requisição da obra B: status %d, esperado 404", who, a.name, response.Code)
			}
			if snapshot(t) != before {
				t.Errorf("%s: %s requisição da obra B mudou o banco", who, a.name)
			}
		}
	}

	// Controle: o gestor da obra A vê e aprova a requisição da própria obra.
	if response := requestCall(f.managerToken, http.MethodGet, f.colleagueA, nil); response.Code != http.StatusOK {
		t.Errorf("gestor abrindo requisição da própria obra: status %d", response.Code)
	}
	if response := requestCall(f.managerToken, http.MethodPost, f.colleagueA, url.Values{"acao": {"aprovar"}}); response.Code != http.StatusSeeOther {
		t.Errorf("gestor aprovando requisição da própria obra: status %d", response.Code)
	}
}

// TestRequesterSeesOnlyOwnRequests: um solicitante não abre, não lista e
// não cancela a requisição de outro solicitante da mesma obra.
func TestRequesterSeesOnlyOwnRequests(t *testing.T) {
	f := setupRequestHTTP(t)

	if response := requestCall(f.requesterToken, http.MethodGet, f.colleagueA, nil); response.Code != http.StatusNotFound {
		t.Errorf("abrir a requisição do colega: status %d, esperado 404", response.Code)
	}
	if strings.Contains(get(f.requesterToken, requestListHandler, "/requisicoes"), fmt.Sprintf(`href="/requisicoes/%d"`, f.colleagueA)) {
		t.Error("a requisição do colega aparece na lista do solicitante")
	}
	before := snapshot(t)
	if response := requestCall(f.requesterToken, http.MethodPost, f.colleagueA, url.Values{"acao": {"cancelar"}}); response.Code != http.StatusNotFound {
		t.Errorf("cancelar a requisição do colega: status %d, esperado 404", response.Code)
	}
	if snapshot(t) != before {
		t.Error("a tentativa de cancelar a requisição do colega mudou o banco")
	}

	// Controle: o autor abre e cancela a dele.
	if response := requestCall(f.otherRequesterToken, http.MethodGet, f.colleagueA, nil); response.Code != http.StatusOK {
		t.Errorf("autor abrindo a própria requisição: status %d", response.Code)
	}
	if response := requestCall(f.otherRequesterToken, http.MethodPost, f.colleagueA, url.Values{"acao": {"cancelar"}}); response.Code != http.StatusSeeOther {
		t.Errorf("autor cancelando a própria pendente: status %d", response.Code)
	}
}

// TestRequestFormIgnoresForgedIDs: obra e item vindos do formulário não
// são confiados. A requisição nova vai para a obra do usuário mesmo com
// obra_id da obra B, e um item de outra requisição no atendimento não é
// entregue.
func TestRequestFormIgnoresForgedIDs(t *testing.T) {
	f := setupRequestHTTP(t)

	form := url.Values{
		"obra_id":     {fmt.Sprint(f.siteB)},
		"material_id": {fmt.Sprint(f.materialA)},
		"quantidade":  {"1"},
		"observacao":  {"teste"},
	}
	response := post(f.requesterToken, newRequestHandler, "/requisicoes/nova", form)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("criar requisição: status %d\n%s", response.Code, response.Body.String())
	}
	var siteID int
	if err := database.DB.QueryRow(`SELECT obra_id FROM requisicoes WHERE observacao = 'teste'`).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if siteID != f.siteA {
		t.Errorf("requisição criada na obra %d, esperado a obra do usuário (%d)", siteID, f.siteA)
	}

	// Atender a requisição da obra A mandando o ID do item da obra B.
	if response := requestCall(f.managerToken, http.MethodPost, f.colleagueA, url.Values{"acao": {"aprovar"}}); response.Code != http.StatusSeeOther {
		t.Fatalf("aprovar: status %d", response.Code)
	}
	before := snapshot(t)
	response = requestCall(f.storekeeperToken, http.MethodPost, f.colleagueA, url.Values{
		"acao": {"atender"},
		fmt.Sprintf("entrega_%d", f.approvedBItem): {"1"},
	})
	if response.Code == http.StatusSeeOther || !strings.Contains(response.Body.String(), "pelo menos um item") {
		t.Errorf("atender com item de outra requisição: status %d", response.Code)
	}
	if snapshot(t) != before {
		t.Error("o item forjado mudou o banco")
	}
}

// TestApproveOwnRequestOverHTTP: o admin não aprova a própria requisição
// pela tela (e o botão nem aparece); o gestor aprova a dele.
func TestApproveOwnRequestOverHTTP(t *testing.T) {
	f := setupRequestHTTP(t)

	selectSite(t, f.adminToken, f.siteA)
	form := url.Values{"obra_id": {fmt.Sprint(f.siteA)}, "material_id": {fmt.Sprint(f.materialA)}, "quantidade": {"1"}, "observacao": {"do admin"}}
	if response := post(f.adminToken, newRequestHandler, "/requisicoes/nova", form); response.Code != http.StatusSeeOther {
		t.Fatalf("admin criando requisição: status %d\n%s", response.Code, response.Body.String())
	}
	own := queryID(t, `SELECT id FROM requisicoes WHERE observacao = 'do admin'`)

	page := requestCall(f.adminToken, http.MethodGet, own, nil).Body.String()
	if strings.Contains(page, `name="acao" value="aprovar"`) {
		t.Error("o botão Aprovar aparece para o autor")
	}
	before := snapshot(t)
	response := requestCall(f.adminToken, http.MethodPost, own, url.Values{"acao": {"aprovar"}})
	if response.Code == http.StatusSeeOther || !strings.Contains(response.Body.String(), "não pode aprovar a própria") {
		t.Errorf("admin aprovando a própria: status %d", response.Code)
	}
	if snapshot(t) != before {
		t.Error("a aprovação recusada mudou o banco")
	}

	if response := requestCall(f.managerToken, http.MethodPost, own, url.Values{"acao": {"aprovar"}}); response.Code != http.StatusSeeOther {
		t.Errorf("gestor aprovando a requisição do admin: status %d", response.Code)
	}
}
