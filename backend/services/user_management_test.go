package services

import (
	"strings"
	"testing"

	database "uchoastock/backend/database"
)

// userIDByEmail devolve o ID do usuário com esse email.
func userIDByEmail(t *testing.T, email string) int {
	t.Helper()
	var id int
	if err := database.DB.QueryRow(`SELECT id FROM usuarios WHERE email = ?`, email).Scan(&id); err != nil {
		t.Fatalf("ler ID de %s: %v", email, err)
	}
	return id
}

func siteLinkCount(t *testing.T, userID int) int {
	t.Helper()
	var total int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM usuario_obras WHERE usuario_id = ?`, userID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	return total
}

func TestCreateUserWebLinksSite(t *testing.T) {
	setupTestDB(t)
	siteA := createTestSite(t, "Obra A", SiteStatusInProgress)

	if err := CreateUserWeb("Gerente A", "gerentea@gmail.com", "senha!1", "gestor", siteA); err != nil {
		t.Fatalf("cadastro do gerente: %v", err)
	}
	manager, err := GetUserByID(userIDByEmail(t, "gerentea@gmail.com"))
	if err != nil {
		t.Fatal(err)
	}
	if manager.SiteID != siteA || manager.SiteName != "Obra A" {
		t.Errorf("obra do gerente = %d/%q, esperado %d/Obra A", manager.SiteID, manager.SiteName, siteA)
	}

	// Administrador não fica preso a uma obra, mesmo que uma seja enviada.
	if err := CreateUserWeb("Chefe", "chefe@gmail.com", "senha!1", "admin", siteA); err != nil {
		t.Fatalf("cadastro do admin: %v", err)
	}
	if got := siteLinkCount(t, userIDByEmail(t, "chefe@gmail.com")); got != 0 {
		t.Errorf("admin com %d vínculo(s), esperado 0", got)
	}

	// Sem obra também pode.
	if err := CreateUserWeb("Sem obra", "semobra@gmail.com", "senha!1", "solicitante", 0); err != nil {
		t.Fatalf("cadastro sem obra: %v", err)
	}
}

// Obra inválida não pode deixar um usuário criado pela metade.
func TestCreateUserWebWithUnknownSiteCreatesNothing(t *testing.T) {
	setupTestDB(t)

	err := CreateUserWeb("Fantasma", "fantasma@gmail.com", "senha!1", "gestor", 9999)
	if err == nil || !strings.Contains(err.Error(), "Obra não encontrada") {
		t.Fatalf("erro = %v, esperado obra não encontrada", err)
	}
	var total int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM usuarios WHERE email = 'fantasma@gmail.com'`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Error("o usuário foi criado mesmo com a obra inválida")
	}
}

func TestUpdateUserAccessWebKeepsOneSite(t *testing.T) {
	setupTestDB(t)
	siteA := createTestSite(t, "Obra A", SiteStatusInProgress)
	siteB := createTestSite(t, "Obra B", SiteStatusInProgress)
	if err := CreateUserWeb("Ana", "ana@gmail.com", "senha!1", "solicitante", siteA); err != nil {
		t.Fatal(err)
	}
	id := userIDByEmail(t, "ana@gmail.com")

	// Trocar de obra substitui o vínculo: continua sendo um só.
	if err := UpdateUserAccessWeb(id, "gestor", siteB); err != nil {
		t.Fatalf("trocar para obra B: %v", err)
	}
	user, err := GetUserByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if user.Role != "gestor" || user.SiteID != siteB {
		t.Errorf("usuário = %s na obra %d, esperado gerente na obra %d", user.Role, user.SiteID, siteB)
	}
	if got := siteLinkCount(t, id); got != 1 {
		t.Errorf("%d vínculos, esperado 1", got)
	}

	// Obra inválida: nada muda, nem a permissão.
	if err := UpdateUserAccessWeb(id, "solicitante", 9999); err == nil {
		t.Error("obra inexistente deveria falhar")
	}
	if user, _ := GetUserByID(id); user.Role != "gestor" || user.SiteID != siteB {
		t.Errorf("alteração recusada mudou o usuário: %s na obra %d", user.Role, user.SiteID)
	}

	// Virar administrador tira o vínculo.
	if err := UpdateUserAccessWeb(id, "admin", siteB); err != nil {
		t.Fatal(err)
	}
	if got := siteLinkCount(t, id); got != 0 {
		t.Errorf("admin com %d vínculo(s), esperado 0", got)
	}

	// 0 é "nenhuma obra".
	if err := UpdateUserAccessWeb(id, "solicitante", 0); err != nil {
		t.Fatal(err)
	}
	if got := siteLinkCount(t, id); got != 0 {
		t.Errorf("sem obra com %d vínculo(s), esperado 0", got)
	}
}

func TestSiteTeamsAndUserList(t *testing.T) {
	setupTestDB(t)
	siteA := createTestSite(t, "Obra A", SiteStatusInProgress)
	for _, u := range []struct{ name, email, role string }{
		{"Bruno", "bruno@gmail.com", "gestor"},
		{"Ana", "ana@gmail.com", "solicitante"},
		{"Removido", "removido@gmail.com", "solicitante"},
	} {
		if err := CreateUserWeb(u.name, u.email, "senha!1", u.role, siteA); err != nil {
			t.Fatal(err)
		}
	}
	if err := DeleteUserWeb(userIDByEmail(t, "removido@gmail.com")); err != nil {
		t.Fatal(err)
	}

	teams, err := GetSiteTeams()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(teams[siteA], ","); got != "Ana,Bruno" {
		t.Errorf("equipe da obra A = %q, esperado \"Ana,Bruno\" (sem o removido)", got)
	}

	users, _, err := ListPaginatedUsers("bruno", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].SiteName != "Obra A" {
		t.Errorf("lista = %+v, esperado Bruno na Obra A", users)
	}
}
