package services

import (
	"testing"
	"time"

	database "uchoastock/backend/database"
)

// createPasswordTestUser cria uma conta ativa com senha de verdade (bcrypt)
// e devolve o ID.
func createPasswordTestUser(t *testing.T, email, role, password string) int {
	t.Helper()
	if err := CreateUserWeb("Conta "+email, email, password, role, 0); err != nil {
		t.Fatalf("criar %s: %v", email, err)
	}
	var id int
	if err := database.DB.QueryRow(`SELECT id FROM usuarios WHERE email = ?`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// sessionExists diz se a sessão do token ainda está no banco.
func sessionExists(t *testing.T, token string) bool {
	t.Helper()
	var count int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM sessoes WHERE token_hash = ?`, HashSessionToken(token)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count > 0
}

func openSession(t *testing.T, userID int) string {
	t.Helper()
	token, err := CreateSession(userID)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// Quem troca a senha de outra pessoa derruba todas as sessões dela: quem
// entrou com a senha antiga perde o acesso na hora.
func TestResetUserPasswordEndsTargetSessions(t *testing.T) {
	adminID := setupTestDB(t)
	target := createPasswordTestUser(t, "ana@empresa.com", RoleStorekeeper, "antiga!1")
	phone := openSession(t, target)
	laptop := openSession(t, target)
	adminSession := openSession(t, adminID)

	if err := ResetUserPasswordWeb(target, adminID, "nova!123", HashSessionToken(adminSession)); err != nil {
		t.Fatalf("trocar a senha: %v", err)
	}
	if sessionExists(t, phone) || sessionExists(t, laptop) {
		t.Error("as sessões da conta continuaram depois da troca de senha")
	}
	if !sessionExists(t, adminSession) {
		t.Error("a sessão de quem trocou a senha (outra conta) não deveria cair")
	}
	if _, ok := AuthenticateUser("ana@empresa.com", "antiga!1"); ok {
		t.Error("a senha antiga continuou valendo")
	}
	if _, ok := AuthenticateUser("ana@empresa.com", "nova!123"); !ok {
		t.Error("a senha nova não vale")
	}
}

// Quem troca a própria senha continua logado no aparelho em que trocou;
// os outros aparelhos caem.
func TestChangeOwnPasswordKeepsCurrentSession(t *testing.T) {
	setupTestDB(t)
	user := createPasswordTestUser(t, "bia@empresa.com", RoleRequester, "antiga!1")
	current := openSession(t, user)
	other := openSession(t, user)

	if err := ChangeOwnPassword(user, "errada!1", "nova!123", HashSessionToken(current)); err == nil {
		t.Fatal("a troca com a senha atual errada deveria falhar")
	}
	if !sessionExists(t, other) {
		t.Fatal("a tentativa recusada derrubou sessões")
	}
	if err := ChangeOwnPassword(user, "antiga!1", "antiga!1", HashSessionToken(current)); err == nil {
		t.Error("a senha nova igual à atual deveria ser recusada")
	}
	if err := ChangeOwnPassword(user, "antiga!1", "curta", HashSessionToken(current)); err == nil {
		t.Error("senha nova fora da regra deveria ser recusada")
	}

	if err := ChangeOwnPassword(user, "antiga!1", "nova!123", HashSessionToken(current)); err != nil {
		t.Fatalf("trocar a própria senha: %v", err)
	}
	if !sessionExists(t, current) {
		t.Error("a sessão de onde a senha foi trocada caiu")
	}
	if sessionExists(t, other) {
		t.Error("a sessão do outro aparelho continuou")
	}
	if _, ok := AuthenticateUser("bia@empresa.com", "nova!123"); !ok {
		t.Error("a senha nova não vale")
	}
}

// O reset-password da linha de comando também derruba as sessões.
func TestResetPasswordCommandEndsSessions(t *testing.T) {
	setupTestDB(t)
	user := createPasswordTestUser(t, "caio@empresa.com", RoleAuditor, "antiga!1")
	session := openSession(t, user)

	if err := ResetPassword("caio@empresa.com", "nova!123"); err != nil {
		t.Fatalf("reset-password: %v", err)
	}
	if sessionExists(t, session) {
		t.Error("a sessão continuou depois do reset-password")
	}
}

// Remover o usuário desativa a conta e derruba as sessões juntos.
func TestDeleteUserEndsSessions(t *testing.T) {
	setupTestDB(t)
	user := createPasswordTestUser(t, "davi@empresa.com", RoleRequester, "senha!12")
	session := openSession(t, user)

	if err := DeleteUserWeb(user); err != nil {
		t.Fatalf("remover: %v", err)
	}
	if sessionExists(t, session) {
		t.Error("a sessão continuou depois da remoção")
	}
	if _, ok := SessionUserID(HashSessionToken(session)); ok {
		t.Error("a sessão de um usuário removido ainda autentica")
	}
}

// O login apaga as sessões vencidas de todo mundo, e só elas.
func TestCreateSessionCleansExpiredSessions(t *testing.T) {
	userID := setupTestDB(t)
	valid := openSession(t, userID)

	// Uma sessão vencida há um dia, gravada como o driver grava um
	// time.Time (com fuso), e outra no formato do CURRENT_TIMESTAMP.
	if _, err := database.DB.Exec(`
		INSERT INTO sessoes (usuario_id, token_hash, expira_em) VALUES (?, 'vencida-1', ?), (?, 'vencida-2', datetime('now', '-1 hour'))
	`, userID, time.Now().Add(-24*time.Hour), userID); err != nil {
		t.Fatal(err)
	}

	openSession(t, userID)

	var expired int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM sessoes WHERE token_hash LIKE 'vencida-%'`).Scan(&expired); err != nil {
		t.Fatal(err)
	}
	if expired != 0 {
		t.Errorf("%d sessões vencidas continuaram no banco", expired)
	}
	if !sessionExists(t, valid) {
		t.Error("a limpeza apagou uma sessão ainda válida")
	}
	if _, ok := SessionUserID(HashSessionToken(valid)); !ok {
		t.Error("a sessão válida não autentica mais")
	}
}
