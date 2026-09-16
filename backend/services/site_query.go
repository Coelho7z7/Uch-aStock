package services

import (
	"database/sql"
	"strings"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
)

// Tipos e situações de obra, como ficam gravados no banco.
const (
	SiteTypeCentral = "CENTRAL"
	SiteTypeProject = "OBRA"

	SiteStatusInProgress = "ANDAMENTO"
	SiteStatusPaused     = "PARALISADA"
	SiteStatusFinished   = "CONCLUIDA"
)

// SiteStatusLabels liga cada situação ao texto da tela, na ordem em que
// aparecem no filtro e no formulário.
var SiteStatusLabels = []struct{ Value, Label string }{
	{SiteStatusInProgress, "Em andamento"},
	{SiteStatusPaused, "Paralisada"},
	{SiteStatusFinished, "Concluída"},
}

// siteColumns são as colunas lidas em toda consulta de obra, na mesma
// ordem em que scanSite as espera.
const siteColumns = `id, nome, tipo, cidade, responsavel, situacao`

func scanSite(row rowScanner) (models.Site, error) {
	var site models.Site
	if err := row.Scan(
		&site.ID,
		&site.Name,
		&site.Type,
		&site.City,
		&site.Manager,
		&site.Status,
	); err != nil {
		return site, err
	}

	site.FormattedType = "Obra"
	if site.Type == SiteTypeCentral {
		// Curto de propósito: o selo fica ao lado do nome, que já costuma
		// ser "Almoxarifado central".
		site.FormattedType = "Central"
	}
	site.FormattedStatus = siteStatusLabel(site.Status)
	return site, nil
}

// siteStatusLabel devolve o texto da situação, ou "" se ela não existe.
// O "" é o que isValidSiteStatus usa para recusar valor desconhecido.
func siteStatusLabel(status string) string {
	for _, s := range SiteStatusLabels {
		if s.Value == status {
			return s.Label
		}
	}
	return ""
}

func isValidSiteStatus(status string) bool {
	return siteStatusLabel(status) != ""
}

// GetSites lista as obras, com o almoxarifado central sempre no topo,
// depois as em andamento, as paralisadas e por último as concluídas.
// search procura no nome, na cidade e no responsável; status vazio ou
// desconhecido traz todas as situações.
func GetSites(search, status string) ([]models.Site, error) {
	query := `SELECT ` + siteColumns + ` FROM obras WHERE ativo = 1`
	var args []any

	if search = strings.TrimSpace(search); search != "" {
		query += ` AND (nome LIKE ? OR cidade LIKE ? OR responsavel LIKE ?)`
		pattern := "%" + search + "%"
		args = append(args, pattern, pattern, pattern)
	}

	if isValidSiteStatus(status) {
		query += ` AND situacao = ?`
		args = append(args, status)
	}

	query += `
		ORDER BY
			CASE WHEN tipo = 'CENTRAL' THEN 0 ELSE 1 END,
			CASE situacao WHEN 'ANDAMENTO' THEN 0 WHEN 'PARALISADA' THEN 1 ELSE 2 END,
			nome COLLATE NOCASE
	`

	rows, err := database.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sites []models.Site
	for rows.Next() {
		site, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		sites = append(sites, site)
	}
	return sites, rows.Err()
}

// GetSiteByID busca uma obra pelo ID. Devolve sql.ErrNoRows se ela não
// existir.
func GetSiteByID(id int) (*models.Site, error) {
	site, err := scanSite(database.DB.QueryRow(`
		SELECT `+siteColumns+`
		FROM obras
		WHERE id = ? AND ativo = 1
	`, id))
	if err != nil {
		return nil, err
	}
	return &site, nil
}

// siteNameTaken indica se já existe outra obra com o mesmo nome,
// ignorando maiúsculas e minúsculas. A comparação é feita no Go com
// strings.EqualFold porque o LOWER do SQLite só entende letras sem
// acento: para ele "UCHÔA" e "uchôa" seriam nomes diferentes.
//
// Recebe a transação para que a checagem e a gravação aconteçam juntas.
// exceptID é a própria obra, na edição (0 no cadastro).
func siteNameTaken(tx *sql.Tx, name string, exceptID int) (bool, error) {
	rows, err := tx.Query(`SELECT nome FROM obras WHERE ativo = 1 AND id <> ?`, exceptID)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var existing string
		if err := rows.Scan(&existing); err != nil {
			return false, err
		}
		if strings.EqualFold(strings.TrimSpace(existing), name) {
			return true, nil
		}
	}
	return false, rows.Err()
}
