package services

import (
	"fmt"
	"time"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
)

func GetMovementsWeb() ([]models.Movement, error) {
	rows, err := database.DB.Query(`
		SELECT
			m.id,
			m.produto_id,
			m.usuario_id,
			m.data,
			p.nome,
			u.nome,
			m.tipo,
			m.quantidade
		FROM movimentacoes m
		JOIN produtos p ON p.id = m.produto_id
		JOIN usuarios u ON u.id = m.usuario_id
		ORDER BY m.data DESC, m.id DESC
	`)
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
			&date,
			&movement.Material,
			&movement.User,
			&movement.Type,
			&movement.Quantity,
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

func registerMovement(materialID int, userID int, movementType string, quantity int) error {
	_, err := database.DB.Exec(`
		INSERT INTO movimentacoes
		(produto_id, usuario_id, tipo, quantidade)
		VALUES (?, ?, ?, ?)
	`, materialID, userID, movementType, quantity)

	return err
}

func ListMovements() {
	rows, err := database.DB.Query(`
		SELECT
			m.data,
			p.nome,
			u.nome,
			m.tipo,
			m.quantidade
		FROM movimentacoes m
		JOIN produtos p ON p.id = m.produto_id
		JOIN usuarios u ON u.id = m.usuario_id
		ORDER BY m.data DESC
	`)
	if err != nil {
		fmt.Println("Erro ao buscar movimentações:", err)
		return
	}
	defer rows.Close()

	found := false

	for rows.Next() {
		var date string
		var material string
		var user string
		var movementType string
		var quantity int

		if err := rows.Scan(&date, &material, &user, &movementType, &quantity); err != nil {
			fmt.Println("Erro ao ler movimentação:", err)
			return
		}

		formattedDate, err := parseMovementDate(date)
		if err != nil {
			fmt.Println("Erro ao formatar data:", err)
			return
		}

		fmt.Println("========== MOVIMENTAÇÃO ==========")
		fmt.Println("Material:", material)
		fmt.Println("Usuário:", user)
		fmt.Println("Tipo:", movementType)

		if movementType == "ATUALIZACAO" {
			fmt.Println("Quantidade: -")
		} else {
			fmt.Println("Quantidade:", quantity)
		}

		fmt.Println("Data:", formattedDate.Local().Format("02/01/2006 15:04:05"))
		fmt.Println("==================================")

		found = true
	}

	if err := rows.Err(); err != nil {
		fmt.Println("Erro ao percorrer movimentações:", err)
		return
	}

	if !found {
		fmt.Println("Nenhuma movimentação registrada.")
	}
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
// registradas hoje, para o cartão de resumo do turno.
//
// O 'localtime' nas duas datas é essencial: o SQLite grava CURRENT_TIMESTAMP
// em UTC, então comparar direto com date('now') faria o "hoje" virar à
// meia-noite de Londres — no Brasil o turno mudaria de dia às 21h.
func CountTodayMovements() (entries int, exits int, err error) {
	err = database.DB.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN tipo = 'ENTRADA' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN tipo = 'SAIDA'   THEN 1 ELSE 0 END), 0)
		FROM movimentacoes
		WHERE date(data, 'localtime') = date('now', 'localtime')
	`).Scan(&entries, &exits)
	return entries, exits, err
}

// GetMovementsFilteredWeb é o GetMovementsWeb com filtro opcional por
// tipo de movimentação ("ENTRADA", "SAIDA", "ATUALIZACAO"). Tipo vazio
// ou desconhecido devolve tudo.
func GetMovementsFilteredWeb(movementType string) ([]models.Movement, error) {
	valid := map[string]bool{"ENTRADA": true, "SAIDA": true, "ATUALIZACAO": true}
	if !valid[movementType] {
		return GetMovementsWeb()
	}

	all, err := GetMovementsWeb()
	if err != nil {
		return nil, err
	}

	filtered := make([]models.Movement, 0, len(all))
	for _, movement := range all {
		if movement.Type == movementType {
			filtered = append(filtered, movement)
		}
	}
	return filtered, nil
}
