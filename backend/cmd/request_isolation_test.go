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
// pessoas e solicitações que os testes de solicitação usam.
type requestHTTPFixture struct {
	isolationFixture
	requesterToken      string // solicitante da obra A
	otherRequesterToken string // outro solicitante da obra A
	pendingB            int    // solicitação pendente da obra B
	approvedB           int    // solicitação aprovada da obra B
	approvedBItem       int    // item da solicitação aprovada da obra B
	colleagueA          int    // solicitação pendente do outro solicitante da obra A
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
			t.Fatalf("criar solicitação: %v", err)
		}
		return id
	}

	f.pendingB = create(managerBID, f.siteB, f.materialB)
	f.approvedB = create(f.storekeeperB, f.siteB, f.materialB)
	if err := services.ApproveRequest(actorFor(managerBID), f.approvedB); err != nil {
		t.Fatal(err)
	}
	f.approvedBItem = queryID(t, fmt.Sprintf(`SELECT id FROM solicitacao_itens WHERE solicitacao_id = %d`, f.approvedB))
	f.colleagueA = create(otherRequesterID, f.siteA, f.materialA)
	return f
}

// requestCall chama o handler de detalhe como a rota /solicitacoes/{id}
// chamaria, com a sessão do token.
func requestCall(token, method string, requestID int, form url.Values) *httptest.ResponseRecorder {
	path := fmt.Sprintf("/solicitacoes/%d", requestID)
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
// obra A tentam ver e agir em solicitações da obra B pelo endereço direto.
// Todos recebem 404 (sem saber se a solicitação existe), nada aparece na
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
				t.Errorf("%s abrindo a solicitação #%d da obra B: status %d, esperado 404", who, id, response.Code)
			}
		}

		// Mesmo escolhendo a obra B no seletor, a lista continua só da obra A.
		selectSite(t, token, f.siteB)
		list := get(token, requestListHandler, "/solicitacoes")
		for _, id := range []int{f.pendingB, f.approvedB} {
			if strings.Contains(list, fmt.Sprintf(`href="/solicitacoes/%d"`, id)) {
				t.Errorf("%s vê a solicitação #%d da obra B na lista", who, id)
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
				t.Errorf("%s: %s solicitação da obra B: status %d, esperado 404", who, a.name, response.Code)
			}
			if snapshot(t) != before {
				t.Errorf("%s: %s solicitação da obra B mudou o banco", who, a.name)
			}
		}
	}

	// Controle: o gestor da obra A vê e aprova a solicitação da própria obra.
	if response := requestCall(f.managerToken, http.MethodGet, f.colleagueA, nil); response.Code != http.StatusOK {
		t.Errorf("gestor abrindo solicitação da própria obra: status %d", response.Code)
	}
	if response := requestCall(f.managerToken, http.MethodPost, f.colleagueA, url.Values{"acao": {"aprovar"}}); response.Code != http.StatusSeeOther {
		t.Errorf("gestor aprovando solicitação da própria obra: status %d", response.Code)
	}
}

// TestRequesterSeesOnlyOwnRequests: um solicitante não abre, não lista e
// não cancela a solicitação de outro solicitante da mesma obra.
func TestRequesterSeesOnlyOwnRequests(t *testing.T) {
	f := setupRequestHTTP(t)

	if response := requestCall(f.requesterToken, http.MethodGet, f.colleagueA, nil); response.Code != http.StatusNotFound {
		t.Errorf("abrir a solicitação do colega: status %d, esperado 404", response.Code)
	}
	if strings.Contains(get(f.requesterToken, requestListHandler, "/solicitacoes"), fmt.Sprintf(`href="/solicitacoes/%d"`, f.colleagueA)) {
		t.Error("a solicitação do colega aparece na lista do solicitante")
	}
	before := snapshot(t)
	if response := requestCall(f.requesterToken, http.MethodPost, f.colleagueA, url.Values{"acao": {"cancelar"}}); response.Code != http.StatusNotFound {
		t.Errorf("cancelar a solicitação do colega: status %d, esperado 404", response.Code)
	}
	if snapshot(t) != before {
		t.Error("a tentativa de cancelar a solicitação do colega mudou o banco")
	}

	// Controle: o autor abre e cancela a dele.
	if response := requestCall(f.otherRequesterToken, http.MethodGet, f.colleagueA, nil); response.Code != http.StatusOK {
		t.Errorf("autor abrindo a própria solicitação: status %d", response.Code)
	}
	if response := requestCall(f.otherRequesterToken, http.MethodPost, f.colleagueA, url.Values{"acao": {"cancelar"}}); response.Code != http.StatusSeeOther {
		t.Errorf("autor cancelando a própria pendente: status %d", response.Code)
	}
}

// TestRequestFormIgnoresForgedIDs: obra e item vindos do formulário não
// são confiados. A solicitação nova vai para a obra do usuário mesmo com
// obra_id da obra B, e um item de outra solicitação no atendimento não é
// entregue.
func TestRequestFormIgnoresForgedIDs(t *testing.T) {
	f := setupRequestHTTP(t)

	form := url.Values{
		"obra_id":     {fmt.Sprint(f.siteB)},
		"material_id": {fmt.Sprint(f.materialA)},
		"quantidade":  {"1"},
		"observacao":  {"teste"},
	}
	response := post(f.requesterToken, newRequestHandler, "/solicitacoes/nova", form)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("criar solicitação: status %d\n%s", response.Code, response.Body.String())
	}
	var siteID int
	if err := database.DB.QueryRow(`SELECT obra_id FROM solicitacoes WHERE observacao = 'teste'`).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if siteID != f.siteA {
		t.Errorf("solicitação criada na obra %d, esperado a obra do usuário (%d)", siteID, f.siteA)
	}

	// Atender a solicitação da obra A mandando o ID do item da obra B.
	if response := requestCall(f.managerToken, http.MethodPost, f.colleagueA, url.Values{"acao": {"aprovar"}}); response.Code != http.StatusSeeOther {
		t.Fatalf("aprovar: status %d", response.Code)
	}
	before := snapshot(t)
	response = requestCall(f.storekeeperToken, http.MethodPost, f.colleagueA, url.Values{
		"acao": {"atender"},
		fmt.Sprintf("entrega_%d", f.approvedBItem): {"1"},
	})
	if response.Code == http.StatusSeeOther || !strings.Contains(response.Body.String(), "pelo menos um item") {
		t.Errorf("atender com item de outra solicitação: status %d", response.Code)
	}
	if snapshot(t) != before {
		t.Error("o item forjado mudou o banco")
	}
}

// TestApproveOwnRequestOverHTTP: o admin não aprova a própria solicitação
// pela tela (e o botão nem aparece); o gestor aprova a dele.
func TestApproveOwnRequestOverHTTP(t *testing.T) {
	f := setupRequestHTTP(t)

	selectSite(t, f.adminToken, f.siteA)
	form := url.Values{"obra_id": {fmt.Sprint(f.siteA)}, "material_id": {fmt.Sprint(f.materialA)}, "quantidade": {"1"}, "observacao": {"do admin"}}
	if response := post(f.adminToken, newRequestHandler, "/solicitacoes/nova", form); response.Code != http.StatusSeeOther {
		t.Fatalf("admin criando solicitação: status %d\n%s", response.Code, response.Body.String())
	}
	own := queryID(t, `SELECT id FROM solicitacoes WHERE observacao = 'do admin'`)

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
		t.Errorf("gestor aprovando a solicitação do admin: status %d", response.Code)
	}
}

// TestNotFoundPage: solicitação de outra obra, solicitação que não existe e
// ID inválido mostram a mesma página 404, no layout do sistema. A resposta
// é idêntica nos dois primeiros casos, para não revelar que a solicitação
// existe. Sem sessão, um endereço desconhecido mostra só o cartão.
func TestNotFoundPage(t *testing.T) {
	f := setupRequestHTTP(t)

	otherSite := requestCall(f.managerToken, http.MethodGet, f.pendingB, nil)
	missing := requestCall(f.managerToken, http.MethodGet, 999999, nil)
	for name, response := range map[string]*httptest.ResponseRecorder{"outra obra": otherSite, "inexistente": missing} {
		body := response.Body.String()
		if response.Code != http.StatusNotFound || !strings.Contains(body, "Página não encontrada") || !strings.Contains(body, `class="sidebar"`) {
			t.Errorf("%s: status %d, sem a página 404 no layout do sistema", name, response.Code)
		}
	}
	// O seletor de obra guarda o endereço atual (para voltar a ele), que é o
	// próprio número digitado; fora isso, as duas respostas são iguais.
	otherBody := strings.ReplaceAll(otherSite.Body.String(), fmt.Sprintf("/solicitacoes/%d", f.pendingB), "/solicitacoes/N")
	missingBody := strings.ReplaceAll(missing.Body.String(), "/solicitacoes/999999", "/solicitacoes/N")
	if otherBody != missingBody {
		t.Error("a 404 de uma solicitação de outra obra é diferente da 404 de uma inexistente: revela que ela existe")
	}

	invalid := httptest.NewRequest(http.MethodGet, "/solicitacoes/abc", nil)
	invalid.SetPathValue("id", "abc")
	invalid.AddCookie(&http.Cookie{Name: "sessao", Value: f.managerToken})
	recorder := httptest.NewRecorder()
	withUser(requestDetailHandler).ServeHTTP(recorder, invalid)
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "Página não encontrada") {
		t.Errorf("ID inválido: status %d", recorder.Code)
	}

	// Endereço desconhecido: com sessão, layout; sem sessão, só o cartão.
	loggedIn := httptest.NewRequest(http.MethodGet, "/nao-existe", nil)
	loggedIn.AddCookie(&http.Cookie{Name: "sessao", Value: f.managerToken})
	recorder = httptest.NewRecorder()
	indexHandler(recorder, loggedIn)
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `class="sidebar"`) {
		t.Errorf("endereço desconhecido com sessão: status %d, sem o layout", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	indexHandler(recorder, httptest.NewRequest(http.MethodGet, "/nao-existe", nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusNotFound || !strings.Contains(body, "Página não encontrada") || strings.Contains(body, `class="sidebar"`) {
		t.Errorf("endereço desconhecido sem sessão: status %d, esperado só o cartão", recorder.Code)
	}
}

// TestRedirectToRequestsKeepsOldLinksWorking: os endereços antigos de
// "/requisicoes" respondem 301 para "/solicitacoes", preservando o que vem
// depois e a query string. Sem isso, favorito e link mandado no grupo da
// obra virariam 404 depois da renomeação da tela.
func TestRedirectToRequestsKeepsOldLinksWorking(t *testing.T) {
	cases := []struct{ from, want string }{
		{"/requisicoes", "/solicitacoes"},
		{"/requisicoes/nova", "/solicitacoes/nova"},
		{"/requisicoes/12", "/solicitacoes/12"},
		{"/requisicoes/12?sucesso=criada", "/solicitacoes/12?sucesso=criada"},
		{"/requisicoes?busca=cimento&pagina=2", "/solicitacoes?busca=cimento&pagina=2"},
	}
	for _, c := range cases {
		recorder := httptest.NewRecorder()
		redirectToRequests(recorder, httptest.NewRequest(http.MethodGet, c.from, nil))
		if recorder.Code != http.StatusMovedPermanently {
			t.Errorf("%s: status %d, esperado %d", c.from, recorder.Code, http.StatusMovedPermanently)
		}
		if got := recorder.Header().Get("Location"); got != c.want {
			t.Errorf("%s: Location = %q, esperado %q", c.from, got, c.want)
		}
	}
}
