package main

import (
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"net/http"
	"strings"

	database "uchoastock/backend/database"
	"uchoastock/backend/services"
)

// loginPageData é o que a tela de login espera receber. LockSeconds
// maior que zero avisa quanto tempo falta para liberar; quem de fato
// recusa a tentativa antes disso é o servidor, não a tela.
type loginPageData struct {
	Email       string
	Error       string
	LockSeconds int
}

// renderLogin desenha a tela de login. Existe porque ela é devolvida em
// três situações diferentes (tela inicial, senha errada e espera), e
// repetir o ParseFiles em cada uma já tinha começado a divergir.
func renderLogin(w http.ResponseWriter, status int, data loginPageData) {
	tmpl, err := template.ParseFiles("frontend/html/index.html")
	if err != nil {
		http.Error(w, "Erro ao carregar tela de login", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(status)
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "Erro ao renderizar tela de login", http.StatusInternalServerError)
	}
}

// indexHandler exibe a tela de login (rota "/").
func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		// Endereço que não existe. Com sessão, a 404 aparece no layout do
		// sistema; sem sessão, só o cartão.
		user, _ := loggedUser(r)
		renderNotFound(w, r, user)
		return
	}

	renderLogin(w, http.StatusOK, loginPageData{})
}

// loginHandler processa o formulário de login.
func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("senha")

	// A espera é conferida antes da senha. Durante o bloqueio não há o
	// que ganhar conferindo o hash, e não conferir também evita que o
	// tempo de resposta denuncie se o email existe ou não.
	if seconds := services.LoginLockSeconds(email); seconds > 0 {
		renderLogin(w, http.StatusTooManyRequests, loginPageData{Email: email, LockSeconds: seconds})
		return
	}

	user, success := services.AuthenticateUser(email, password)
	if !success {
		seconds := services.RegisterFailedLogin(email)
		if seconds > 0 {
			renderLogin(w, http.StatusTooManyRequests, loginPageData{Email: email, LockSeconds: seconds})
			return
		}

		renderLogin(w, http.StatusUnauthorized, loginPageData{Email: email, Error: "Email ou senha incorretos."})
		return
	}

	// Entrou: os erros anteriores deixam de contar.
	services.ClearLoginAttempts(email)

	token, err := createSession(user.ID)
	if err != nil {
		http.Error(w, "Erro ao iniciar sessão", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "sessao",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60 * 60 * 24 * 30,
	})

	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// logoutHandler apaga a sessão atual e redireciona para o login.
func logoutHandler(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("sessao"); err == nil {
		hash := sha256.Sum256([]byte(cookie.Value))
		tokenHash := hex.EncodeToString(hash[:])
		database.DB.Exec("DELETE FROM sessoes WHERE token_hash = ?", tokenHash)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "sessao",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPS(r),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// isHTTPS indica se a requisição chegou por HTTPS, para o cookie de sessão
// sair com Secure (o navegador só o devolve por conexão criptografada).
// No Railway o HTTPS termina no proxy, que repassa para cá em HTTP comum e
// conta o protocolo original no cabeçalho X-Forwarded-Proto. Com vários
// proxies o cabeçalho vem como lista ("https, http"): vale o primeiro,
// que é o do navegador.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}
