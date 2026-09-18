package services

import (
	"database/sql"
	"fmt"

	database "uchoastock/backend/database"
)

// GetSessionSiteID devolve a obra escolhida no seletor do topo para esta
// sessão, ou 0 se nenhuma foi escolhida ("Todas as obras").
//
// sql.NullInt64 é como o Go lê uma coluna que pode ser NULL: Valid diz se
// havia valor. Ler NULL direto num int daria erro.
func GetSessionSiteID(tokenHash string) (int, error) {
	var siteID sql.NullInt64
	err := database.DB.QueryRow(`
		SELECT obra_id
		FROM sessoes
		WHERE token_hash = ?
	`, tokenHash).Scan(&siteID)
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
