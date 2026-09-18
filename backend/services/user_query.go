package services

import (
	database "uchoastock/backend/database"
	"uchoastock/backend/models"
)

// userSiteJoin traz a obra do usuário junto. É LEFT JOIN porque nem todo
// usuário tem obra (administradores não têm), e com JOIN comum eles
// sumiriam do resultado.
const userSiteJoin = `
	LEFT JOIN usuario_obras uo ON uo.usuario_id = u.id
	LEFT JOIN obras o ON o.id = uo.obra_id AND o.ativo = 1
`

func GetUserByID(id int) (*models.User, error) {
	var user models.User

	err := database.DB.QueryRow(`
		SELECT u.id, u.nome, u.email, u.role, u.ativo, COALESCE(o.id, 0), COALESCE(o.nome, '')
		FROM usuarios u
		`+userSiteJoin+`
		WHERE u.id = ?
	`, id).Scan(
		&user.ID,
		&user.Name,
		&user.Email,
		&user.Role,
		&user.Active,
		&user.SiteID,
		&user.SiteName,
	)

	if err != nil {
		return nil, err
	}

	return &user, nil
}

// GetSiteTeams devolve, para cada obra, o nome de quem atua nela, em
// ordem alfabética. A chave do mapa é o ID da obra. Usuário removido
// (ativo = 0) não entra.
func GetSiteTeams() (map[int][]string, error) {
	rows, err := database.DB.Query(`
		SELECT uo.obra_id, u.nome
		FROM usuario_obras uo
		JOIN usuarios u ON u.id = uo.usuario_id
		WHERE u.ativo = 1
		ORDER BY u.nome COLLATE NOCASE
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	teams := map[int][]string{}
	for rows.Next() {
		var siteID int
		var name string
		if err := rows.Scan(&siteID, &name); err != nil {
			return nil, err
		}
		teams[siteID] = append(teams[siteID], name)
	}
	return teams, rows.Err()
}
