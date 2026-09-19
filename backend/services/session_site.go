package services

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	database "uchoastock/backend/database"
)

// SessionLifetime é quanto tempo uma sessão vale depois do login.
const SessionLifetime = 30 * 24 * time.Hour

// HashSessionToken devolve o hash do token do cookie. O banco guarda só o
// hash: quem ler a tabela sessoes não consegue montar um cookie válido.
func HashSessionToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// CreateSession gera um token aleatório, guarda apenas o hash dele no
// banco (nunca o token em texto puro) e devolve o token para ser colocado
// no cookie do navegador.
//
// Aproveita o login para apagar as sessões vencidas de todo mundo. Sem
// isso, a sessão de quem nunca voltou ficava na tabela para sempre: a
// sessão vencida só era apagada quando alguém tentava usá-la.
func CreateSession(userID int) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)

	if err := deleteExpiredSessions(); err != nil {
		return "", err
	}

	_, err := database.DB.Exec(`
		INSERT INTO sessoes (usuario_id, token_hash, expira_em)
		VALUES (?, ?, ?)
	`, userID, HashSessionToken(token), time.Now().UTC().Add(SessionLifetime))
	if err != nil {
		return "", err
	}
	return token, nil
}

// deleteExpiredSessions apaga as sessões que já venceram.
//
// A comparação é feita no Go, e não no SQL, por causa do formato: o driver
// grava expira_em como o texto de um time.Time do Go ("2026-09-17
// 22:48:07.99 -0300 -03"), que as funções de data do SQLite não entendem.
// O driver, ao ler a coluna de volta, entende.
//
// Primeiro junta os IDs e só depois apaga: com uma conexão só
// (SetMaxOpenConns(1)), um DELETE com a leitura ainda aberta ficaria
// esperando por ela para sempre.
func deleteExpiredSessions() error {
	rows, err := database.DB.Query(`SELECT id, expira_em FROM sessoes`)
	if err != nil {
		return err
	}
	var expired []int
	now := time.Now()
	for rows.Next() {
		var id int
		var expiresAt time.Time
		if err := rows.Scan(&id, &expiresAt); err != nil {
			// Data ilegível: fica, e é apagada quando alguém tentar usá-la.
			continue
		}
		if now.After(expiresAt) {
			expired = append(expired, id)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, id := range expired {
		if _, err := database.DB.Exec(`DELETE FROM sessoes WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

// SessionUserID devolve o ID do usuário dono da sessão, se ela existir,
// ainda não tiver expirado e o usuário continuar ativo. Sem a checagem de
// ativo, uma conta removida seguia usando o sistema até a sessão expirar,
// desde que a remoção não tivesse passado por DeleteUserWeb (que apaga as
// sessões). A sessão vencida é apagada aqui mesmo.
func SessionUserID(tokenHash string) (int, bool) {
	var userID int
	var expiresAt time.Time
	err := database.DB.QueryRow(`
		SELECT s.usuario_id, s.expira_em
		FROM sessoes s
		JOIN usuarios u ON u.id = s.usuario_id
		WHERE s.token_hash = ? AND u.ativo = 1
	`, tokenHash).Scan(&userID, &expiresAt)
	if err != nil {
		return 0, false
	}

	if time.Now().After(expiresAt) {
		_ = DeleteSession(tokenHash)
		return 0, false
	}
	return userID, true
}

// DeleteSession apaga uma sessão (logout).
func DeleteSession(tokenHash string) error {
	_, err := database.DB.Exec(`DELETE FROM sessoes WHERE token_hash = ?`, tokenHash)
	return err
}

// deleteUserSessionsTx derruba as sessões do usuário, menos a de hash
// keepTokenHash ("" derruba todas). É o que a troca de senha usa: quem
// sabia a senha antiga e já estava logado perde o acesso na hora, e a
// pessoa que trocou a própria senha continua logada no aparelho em que
// trocou.
func deleteUserSessionsTx(tx *sql.Tx, userID int, keepTokenHash string) error {
	_, err := tx.Exec(`
		DELETE FROM sessoes WHERE usuario_id = ? AND token_hash <> ?
	`, userID, keepTokenHash)
	return err
}

// GetSessionSiteID devolve a obra escolhida no seletor do topo para esta
// sessão, ou 0 se nenhuma foi escolhida ("Todas as obras").
//
// sql.NullInt64 é como o Go lê uma coluna que pode ser NULL: Valid diz se
// havia valor. Ler NULL direto num int daria erro.
//
// Sessão que não existe mais (apagada por um logout em outra aba entre a
// checagem do login e esta leitura) conta como "nenhuma obra escolhida",
// em vez de derrubar a tela com erro 500.
func GetSessionSiteID(tokenHash string) (int, error) {
	var siteID sql.NullInt64
	err := database.DB.QueryRow(`
		SELECT obra_id
		FROM sessoes
		WHERE token_hash = ?
	`, tokenHash).Scan(&siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !siteID.Valid {
		return 0, nil
	}
	return int(siteID.Int64), nil
}

// SetSessionSite guarda a obra escolhida na sessão. siteID 0 grava NULL,
// que é "Todas as obras". Quem chama já conferiu se o usuário pode ver
// essa opção e se a obra existe.
func SetSessionSite(tokenHash string, siteID int) error {
	var value any
	if siteID > 0 {
		value = siteID
	}

	result, err := database.DB.Exec(`
		UPDATE sessoes
		SET obra_id = ?
		WHERE token_hash = ?
	`, value, tokenHash)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("sessão não encontrada")
	}
	return nil
}
