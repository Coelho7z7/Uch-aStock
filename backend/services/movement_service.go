package services

import (
	"fmt"
	"time"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
	"uchoastock/backend/utils"
)

// validMovementTypes são os valores aceitos em movimentacoes.tipo.
var validMovementTypes = map[string]bool{"ENTRADA": true, "SAIDA": true, "ATUALIZACAO": true}

// IsValidMovementType indica se o texto é um tipo de movimentação
// conhecido. O handler usa para ignorar um ?tipo= inventado na URL.
func IsValidMovementType(movementType string) bool {
	return validMovementTypes[movementType]
}

// MovementFilter reúne os filtros opcionais do histórico. Campo vazio
// significa "sem filtro" naquele critério. From e To vêm no formato
// AAAA-MM-DD e são inclusivos — quem chama já validou o formato.
//
// SiteID 0 traz o histórico de todas as obras, inclusive as atualizações
// de cadastro (que não têm obra). Com uma obra escolhida, só aparece o
// que aconteceu nela.
type MovementFilter struct {
	Type     string
	Material string
	From     string
	To       string
	SiteID   int
}

// GetMovementsFilteredWeb devolve o histórico aplicando os filtros no
// próprio SQL, do mais recente para o mais antigo. Só pedaços fixos de
// SQL são concatenados; todo valor digitado pelo usuário entra por
// placeholder "?".
//
// O JOIN com obras é LEFT porque a atualização de cadastro não tem obra:
// com JOIN comum, essas linhas sumiriam do histórico.
func GetMovementsFilteredWeb(filter MovementFilter) ([]models.Movement, error) {
	query := `
		SELECT
			m.id,
			m.produto_id,
			m.usuario_id,
			COALESCE(m.obra_id, 0),
			COALESCE(o.nome, ''),
			m.data,
			p.nome,
			p.unidade,
			u.nome,
			m.tipo,
			m.quantidade,
			m.observacao
		FROM movimentacoes m
		JOIN produtos p ON p.id = m.produto_id
		JOIN usuarios u ON u.id = m.usuario_id
		LEFT JOIN obras o ON o.id = m.obra_id
		WHERE 1 = 1
	`
	var args []any

	if filter.SiteID > 0 {
		query += ` AND m.obra_id = ?`
		args = append(args, filter.SiteID)
	}
	if validMovementTypes[filter.Type] {
		query += ` AND m.tipo = ?`
		args = append(args, filter.Type)
	}
	if filter.Material != "" {
		query += ` AND p.nome LIKE ?`
		args = append(args, "%"+filter.Material+"%")
	}
	// 'localtime' pelo mesmo motivo de CountTodayMovements: a data é
	// gravada em UTC, e o dia que importa é o do Brasil.
	if filter.From != "" {
		query += ` AND date(m.data, 'localtime') >= ?`
		args = append(args, filter.From)
	}
	if filter.To != "" {
		query += ` AND date(m.data, 'localtime') <= ?`
		args = append(args, filter.To)
	}

	query += ` ORDER BY m.data DESC, m.id DESC`

	rows, err := database.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var movements []models.Movement
	for rows.Next() {
		var movement models.Movement
		var date string

		if err := rows.Scan(
			&movement.ID,
			&movement.MaterialID,
			&movement.UserID,
			&movement.SiteID,
			&movement.SiteName,
			&date,
			&movement.Material,
			&movement.Unit,
			&movement.User,
			&movement.Type,
			&movement.Quantity,
			&movement.Note,
		); err != nil {
			return nil, err
		}

		formattedDate, err := parseMovementDate(date)
		if err != nil {
			return nil, err
		}

		movement.Date = formattedDate
		movement.FormattedDate = formattedDate.Local().Format("02/01/2006")
		movement.FormattedTime = formattedDate.Local().Format("15:04")
		movement.FormattedQuantity = utils.FormatQuantity(movement.Quantity)
		movement.FormattedType = map[string]string{
			"ENTRADA":     "Entrada",
			"SAIDA":       "Saída",
			"ATUALIZACAO": "Atualização",
		}[movement.Type]
		if movement.FormattedType == "" {
			movement.FormattedType = movement.Type
		}
		movements = append(movements, movement)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return movements, nil
}

func parseMovementDate(date string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
	}

	for _, format := range formats {
		if formattedDate, err := time.Parse(format, date); err == nil {
			return formattedDate, nil
		}
	}

	return time.Time{}, fmt.Errorf("data inválida: %s", date)
}

// CountTodayMovements conta quantas entradas e quantas saídas foram
// registradas hoje na obra (siteID 0 = todas), para o cartão de resumo
// do turno.
//
// O 'localtime' nas duas datas é essencial: o SQLite grava CURRENT_TIMESTAMP
// em UTC, então comparar direto com date('now') faria o "hoje" virar à
// meia-noite de Londres — no Brasil o turno mudaria de dia às 21h.
func CountTodayMovements(siteID int) (entries int, exits int, err error) {
	err = database.DB.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN tipo = 'ENTRADA' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN tipo = 'SAIDA'   THEN 1 ELSE 0 END), 0)
		FROM movimentacoes
		WHERE date(data, 'localtime') = date('now', 'localtime')
			AND (? = 0 OR obra_id = ?)
	`, siteID, siteID).Scan(&entries, &exits)
	return entries, exits, err
}
