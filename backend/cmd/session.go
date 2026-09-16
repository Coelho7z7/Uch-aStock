package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	database "uchoastock/backend/database"
)

// createSession gera um token aleatório, guarda apenas o hash dele no
// banco (nunca o token em texto puro) e devolve o token para ser
// colocado no cookie do navegador.
func createSession(userID int) (string, error) {
	bytes := make([]byte, 32)

	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	token := hex.EncodeToString(bytes)

	hash := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(hash[:])

	expiresAt := time.Now().Add(30 * 24 * time.Hour)

	_, err := database.DB.Exec(`
		INSERT INTO sessoes (usuario_id, token_hash, expira_em)
		VALUES (?, ?, ?)
	`, userID, tokenHash, expiresAt)

	if err != nil {
		return "", err
	}

	return token, nil
}

// sessionTokenHash devolve o hash do token do cookie de sessão, que é
// como a sessão está identificada no banco. Não confere se a sessão é
// válida: isso é papel de userFromSession.
func sessionTokenHash(r *http.Request) (string, bool) {
	cookie, err := r.Cookie("sessao")
	if err != nil {
		return "", false
	}
	hash := sha256.Sum256([]byte(cookie.Value))
	return hex.EncodeToString(hash[:]), true
}

// userFromSession lê o cookie de sessão da requisição e retorna o ID
// do usuário logado, se a sessão existir, ainda não tiver expirado e o
// usuário continuar ativo. Sem a checagem de ativo, uma conta removida
// seguia usando o sistema até a sessão expirar, desde que a remoção não
// tivesse passado por DeleteUserWeb (que apaga as sessões).
func userFromSession(r *http.Request) (int, bool) {
	cookie, err := r.Cookie("sessao")

	if err != nil {
		return 0, false
	}

	hash := sha256.Sum256([]byte(cookie.Value))
	tokenHash := hex.EncodeToString(hash[:])

	var userID int
	var expiresAt time.Time

	err = database.DB.QueryRow(`
		SELECT s.usuario_id, s.expira_em
		FROM sessoes s
		JOIN usuarios u ON u.id = s.usuario_id
		WHERE s.token_hash = ? AND u.ativo = 1
	`, tokenHash).Scan(&userID, &expiresAt)

	if err != nil {
		return 0, false
	}

	if time.Now().After(expiresAt) {
		database.DB.Exec(`
			DELETE FROM sessoes
			WHERE token_hash = ?
		`, tokenHash)

		return 0, false
	}

	return userID, true
}
