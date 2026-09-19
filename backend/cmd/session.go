package main

import (
	"net/http"

	"uchoastock/backend/services"
)

// sessionCookieName é o nome do cookie que guarda o token da sessão.
const sessionCookieName = "sessao"

// createSession abre uma sessão para o usuário e devolve o token do
// cookie. A regra (token aleatório, só o hash no banco) mora em services.
func createSession(userID int) (string, error) {
	return services.CreateSession(userID)
}

// sessionTokenHash devolve o hash do token do cookie de sessão, que é
// como a sessão está identificada no banco. Não confere se a sessão é
// válida: isso é papel de userFromSession.
func sessionTokenHash(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	return services.HashSessionToken(cookie.Value), true
}

// userFromSession lê o cookie de sessão da requisição e retorna o ID
// do usuário logado, se a sessão for válida (ver services.SessionUserID).
func userFromSession(r *http.Request) (int, bool) {
	hash, ok := sessionTokenHash(r)
	if !ok {
		return 0, false
	}
	return services.SessionUserID(hash)
}
