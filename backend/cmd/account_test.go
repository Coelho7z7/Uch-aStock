package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
	"uchoastock/backend/services"
)

// O almoxarife não abre a tela de usuários, mas troca a própria senha em
// "Minha senha", desde que informe a atual.
func TestMyPasswordForRoleWithoutUserManagement(t *testing.T) {
	f := setupIsolation(t)
	token := f.storekeeperToken

	wrong := post(token, myPasswordHandler, "/minha-senha", url.Values{
		"senha_atual": {"errada!1"}, "senha_nova": {"nova!1234"}, "confirmacao": {"nova!1234"},
	})
	if wrong.Code != http.StatusBadRequest {
		t.Fatalf("senha atual errada: status %d, esperado 400", wrong.Code)
	}

	mismatch := post(token, myPasswordHandler, "/minha-senha", url.Values{
		"senha_atual": {"senha!123"}, "senha_nova": {"nova!1234"}, "confirmacao": {"outra!1234"},
	})
	if mismatch.Code != http.StatusBadRequest {
		t.Fatalf("confirmação diferente: status %d, esperado 400", mismatch.Code)
	}
	if _, ok := services.AuthenticateUser("almox.a@empresa.com", "senha!123"); !ok {
		t.Fatal("uma tentativa recusada trocou a senha")
	}

	ok := post(token, myPasswordHandler, "/minha-senha", url.Values{
		"senha_atual": {"senha!123"}, "senha_nova": {"nova!1234"}, "confirmacao": {"nova!1234"},
	})
	if ok.Code != http.StatusSeeOther {
		t.Fatalf("troca certa: status %d, esperado 303", ok.Code)
	}
	if _, ok := services.AuthenticateUser("almox.a@empresa.com", "nova!1234"); !ok {
		t.Error("a senha nova não vale")
	}
	// Quem trocou continua logado.
	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	if _, logged := userFromSession(request); !logged {
		t.Error("a sessão de quem trocou a própria senha caiu")
	}
}

// Pela tela de usuários, a própria senha não é trocada (ela vai para
// "Minha senha", que pede a atual), e a senha de outro administrador só o
// superadmin troca.
func TestUserScreenPasswordRules(t *testing.T) {
	f := setupIsolation(t)
	adminID := queryID(t, `SELECT id FROM usuarios WHERE email = 'admin.teste@empresa.com'`)
	if err := services.CreateUserWeb("Outro admin", "outro.admin@empresa.com", "senha!123", "admin", 0); err != nil {
		t.Fatal(err)
	}
	otherAdmin := queryID(t, `SELECT id FROM usuarios WHERE email = 'outro.admin@empresa.com'`)

	own := post(f.adminToken, userHandler, "/usuarios", url.Values{
		"acao": {"redefinir_senha"}, "usuario_id": {fmt.Sprint(adminID)}, "senha": {"nova!1234"},
	})
	if own.Code == http.StatusSeeOther {
		t.Error("a própria senha foi trocada sem a senha atual")
	}

	before := snapshot(t)
	other := post(f.adminToken, userHandler, "/usuarios", url.Values{
		"acao": {"redefinir_senha"}, "usuario_id": {fmt.Sprint(otherAdmin)}, "senha": {"nova!1234"},
	})
	if other.Code != http.StatusForbidden {
		t.Errorf("admin trocando a senha de outro admin: status %d, esperado 403", other.Code)
	}
	if snapshot(t) != before {
		t.Error("o banco mudou com a troca recusada")
	}

	// Controle: a senha de um almoxarife o admin troca.
	control := post(f.adminToken, userHandler, "/usuarios", url.Values{
		"acao": {"redefinir_senha"}, "usuario_id": {fmt.Sprint(f.storekeeperA)}, "senha": {"nova!1234"},
	})
	if control.Code != http.StatusSeeOther {
		t.Errorf("admin trocando a senha do almoxarife: status %d, esperado 303", control.Code)
	}
}

func TestCanResetPassword(t *testing.T) {
	superadmin := &models.User{ID: 1, Role: services.RoleSuperadmin}
	admin := &models.User{ID: 2, Role: services.RoleAdmin}
	otherAdmin := &models.User{ID: 3, Role: services.RoleAdmin}
	storekeeper := &models.User{ID: 4, Role: services.RoleStorekeeper, SiteID: 7}

	cases := []struct {
		name          string
		actor, target *models.User
		want          bool
	}{
		{"admin -> outro admin", admin, otherAdmin, false},
		{"superadmin -> admin", superadmin, admin, true},
		{"admin -> almoxarife", admin, storekeeper, true},
		{"almoxarife -> admin", storekeeper, admin, false},
	}
	for _, c := range cases {
		if got := canResetPassword(c.actor, c.target); got != c.want {
			t.Errorf("%s: %v, esperado %v", c.name, got, c.want)
		}
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name, remote, forwarded, want string
	}{
		{"sem proxy", "192.0.2.10:5555", "", "192.0.2.10"},
		{"proxy do Railway", "10.0.0.2:80", "203.0.113.7", "203.0.113.7"},
		// O navegador pode mandar um X-Forwarded-For inventado; o proxy
		// acrescenta o endereço real no fim, e é ele que vale.
		{"cabeçalho forjado antes", "10.0.0.2:80", "1.2.3.4, 203.0.113.7", "203.0.113.7"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/login", nil)
		r.RemoteAddr = c.remote
		if c.forwarded != "" {
			r.Header.Set("X-Forwarded-For", c.forwarded)
		}
		if got := clientIP(r); got != c.want {
			t.Errorf("%s: clientIP = %q, esperado %q", c.name, got, c.want)
		}
	}
}

// Erro de regra de negócio vai para a tela como veio; erro do banco não:
// ele pode revelar detalhe interno (nome de tabela, caminho do arquivo).
func TestStockErrorMessageHidesDatabaseErrors(t *testing.T) {
	business := []error{
		services.StockInputError{Message: "material não encontrado"},
		fmt.Errorf("%w: há só 2 saco nesta obra", services.ErrInsufficientStock),
		services.ErrSiteFinished,
	}
	for _, err := range business {
		if got := stockErrorMessage(err); got != err.Error() {
			t.Errorf("erro de negócio %q virou %q", err, got)
		}
	}

	internal := errors.New("SQL logic error: no such table: saldos (1)")
	if got := stockErrorMessage(internal); strings.Contains(got, "saldos") || strings.Contains(got, "SQL") {
		t.Errorf("erro do banco apareceu na tela: %q", got)
	}
}

// A tela de estoque não mostra o erro cru do banco: com a tabela do
// histórico fora do lugar (no banco temporário do teste), a entrada falha
// com a mensagem genérica.
func TestStockScreenDoesNotLeakDatabaseError(t *testing.T) {
	f := setupIsolation(t)
	selectSite(t, f.managerToken, f.siteA)
	if _, err := database.DB.Exec(`ALTER TABLE movimentacoes RENAME TO movimentacoes_fora`); err != nil {
		t.Fatal(err)
	}

	response := post(f.managerToken, stockHandler, "/estoque", url.Values{
		"acao": {"entrada"}, "material_id": {fmt.Sprint(f.materialA)}, "quantidade": {"2"}, "obra_id": {fmt.Sprint(f.siteA)},
	})
	body := response.Body.String()
	// "movimentacoes" sozinho aparece no link da barra lateral; o que não
	// pode aparecer é o texto do erro do SQLite.
	if strings.Contains(body, "no such table") || strings.Contains(body, "SQL logic") {
		t.Error("a mensagem do banco apareceu na tela de estoque")
	}
	if !strings.Contains(body, "Não foi possível concluir a operação") {
		t.Errorf("a tela não mostrou a mensagem genérica (status %d)", response.Code)
	}
}
