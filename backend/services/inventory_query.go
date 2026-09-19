package services

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
	"uchoastock/backend/utils"
)

// InventoryFilter é o recorte de uma consulta de inventários. Quem chama
// monta a visibilidade com InventoryActor.VisibleFilter e acrescenta os
// filtros da tela.
type InventoryFilter struct {
	// SiteID 0 traz todas as obras.
	SiteID int
	// None marca um recorte vazio (usuário sem obra ou sem permissão).
	None bool
	// Status vazio ou desconhecido traz todas as situações.
	Status string
	// Search procura no número ("INV-0007", "7"), na obra e em quem abriu.
	Search string
}

// inventoryColumns são as colunas da listagem, na ordem de scanInventory.
// As datas vêm como estão gravadas (UTC) e scanInventory as passa para o
// horário local, como em requestColumns.
const inventoryColumns = `
	v.id,
	v.obra_id,
	o.nome,
	v.status,
	v.aberto_por,
	u.nome,
	v.aberto_em,
	COALESCE(e.nome, ''),
	v.enviado_em,
	COALESCE(d.nome, ''),
	v.decidido_em,
	v.motivo_rejeicao,
	(SELECT COUNT(*) FROM inventario_itens i WHERE i.inventario_id = v.id),
	(SELECT COUNT(*) FROM inventario_itens i WHERE i.inventario_id = v.id AND i.quantidade_contada IS NOT NULL),
	(SELECT COUNT(*) FROM inventario_itens i WHERE i.inventario_id = v.id AND i.quantidade_contada IS NOT NULL
		AND ROUND(i.quantidade_contada - i.saldo_esperado, 3) <> 0)
`

const inventoryJoins = `
	FROM inventarios v
	JOIN obras o ON o.id = v.obra_id
	JOIN usuarios u ON u.id = v.aberto_por
	LEFT JOIN usuarios e ON e.id = v.enviado_por
	LEFT JOIN usuarios d ON d.id = v.decidido_por
`

func scanInventory(row rowScanner) (models.Inventory, error) {
	var v models.Inventory
	var openedAt, sentAt, decidedAt sql.NullString
	err := row.Scan(
		&v.ID, &v.SiteID, &v.SiteName, &v.Status,
		&v.OpenedByID, &v.OpenedByName, &openedAt,
		&v.SentByName, &sentAt,
		&v.DecidedByName, &decidedAt,
		&v.RejectionReason,
		&v.ItemCount, &v.CountedCount, &v.DifferenceCount,
	)
	v.OpenedDate = formatDBTime(openedAt, "02/01/2006")
	v.OpenedAt = formatDBTime(openedAt, "02/01/2006 15:04")
	v.SentAt = formatDBTime(sentAt, "02/01/2006 15:04")
	v.DecidedAt = formatDBTime(decidedAt, "02/01/2006 15:04")
	v.Code = InventoryCode(v.ID)
	v.FormattedStatus = inventoryStatusLabel(v.Status)
	return v, err
}

// inventoryWhere monta o WHERE do filtro. Só pedaços fixos de SQL são
// concatenados; todo valor vai por placeholder.
func inventoryWhere(filter InventoryFilter) (string, []any) {
	where := ` WHERE 1 = 1`
	var args []any

	if filter.SiteID > 0 {
		where += ` AND v.obra_id = ?`
		args = append(args, filter.SiteID)
	}
	if isValidInventoryStatus(filter.Status) {
		where += ` AND v.status = ?`
		args = append(args, filter.Status)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		// "INV-0007", "inv-7" e "7" acham o mesmo inventário.
		number := -1
		digits := strings.TrimPrefix(strings.TrimPrefix(strings.ToUpper(search), "INV"), "-")
		if n, err := strconv.Atoi(digits); err == nil {
			number = n
		}
		pattern := "%" + search + "%"
		where += ` AND (v.id = ? OR o.nome LIKE ? OR u.nome LIKE ?)`
		args = append(args, number, pattern, pattern)
	}
	return where, args
}

// ListInventories devolve uma página de inventários, do mais novo para o
// mais antigo, e o total do filtro.
func ListInventories(filter InventoryFilter, page, perPage int) ([]models.Inventory, int, error) {
	if filter.None {
		return nil, 0, nil
	}
	if page < 1 {
		page = 1
	}
	where, args := inventoryWhere(filter)

	var total int
	if err := database.DB.QueryRow(`SELECT COUNT(*) `+inventoryJoins+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := database.DB.Query(`SELECT `+inventoryColumns+inventoryJoins+where+` ORDER BY v.id DESC LIMIT ? OFFSET ?`,
		append(args, perPage, (page-1)*perPage)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var inventories []models.Inventory
	for rows.Next() {
		v, err := scanInventory(rows)
		if err != nil {
			return nil, 0, err
		}
		inventories = append(inventories, v)
	}
	return inventories, total, rows.Err()
}

// CountInventories conta os inventários do recorte (para o contador da
// barra lateral).
func CountInventories(filter InventoryFilter) (int, error) {
	if filter.None {
		return 0, nil
	}
	where, args := inventoryWhere(filter)
	var total int
	err := database.DB.QueryRow(`SELECT COUNT(*) `+inventoryJoins+where, args...).Scan(&total)
	return total, err
}

// GetInventory devolve o inventário com os itens, se estiver ao alcance do
// actor. Fora do alcance é ErrInventoryNotFound, igual a não existir.
func GetInventory(actor InventoryActor, id int) (*models.Inventory, error) {
	v, err := scanInventory(database.DB.QueryRow(`SELECT `+inventoryColumns+inventoryJoins+` WHERE v.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInventoryNotFound
	}
	if err != nil {
		return nil, err
	}
	if !actor.CanSee(v.SiteID) {
		return nil, ErrInventoryNotFound
	}

	rows, err := database.DB.Query(`
		SELECT i.id, i.produto_id, p.nome, p.unidade, i.saldo_esperado, i.quantidade_contada, i.justificativa, COALESCE(c.nome, '')
		FROM inventario_itens i
		JOIN produtos p ON p.id = i.produto_id
		LEFT JOIN usuarios c ON c.id = i.contado_por
		WHERE i.inventario_id = ?
		ORDER BY p.nome COLLATE NOCASE
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var item models.InventoryItem
		var counted sql.NullFloat64
		if err := rows.Scan(&item.ID, &item.MaterialID, &item.MaterialName, &item.Unit,
			&item.Expected, &counted, &item.Justification, &item.CountedByName); err != nil {
			return nil, err
		}
		item.Expected = utils.RoundQuantity(item.Expected)
		item.FormattedExpected = utils.FormatQuantity(item.Expected)
		if counted.Valid {
			item.IsCounted = true
			item.Counted = utils.RoundQuantity(counted.Float64)
			item.FormattedCounted = utils.FormatQuantity(item.Counted)
			item.CountedInput = utils.FormatQuantityInput(item.Counted)
			item.Difference = utils.RoundQuantity(item.Counted - item.Expected)
			item.HasDifference = item.Difference != 0
			item.FormattedDifference = utils.FormatQuantity(item.Difference)
			if item.Difference > 0 {
				item.FormattedDifference = "+" + item.FormattedDifference
			}
		}
		v.Items = append(v.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// A mesma regra de ApproveInventory, aqui só para a tela saber se
	// mostra o botão de aprovar. O UNION já tira os repetidos.
	participants, err := database.DB.Query(`
		SELECT aberto_por FROM inventarios WHERE id = ?
		UNION SELECT enviado_por FROM inventarios WHERE id = ? AND enviado_por IS NOT NULL
		UNION SELECT contado_por FROM inventario_itens WHERE inventario_id = ? AND contado_por IS NOT NULL
	`, id, id, id)
	if err != nil {
		return nil, err
	}
	defer participants.Close()
	for participants.Next() {
		var userID int
		if err := participants.Scan(&userID); err != nil {
			return nil, err
		}
		v.ParticipantIDs = append(v.ParticipantIDs, userID)
	}
	return &v, participants.Err()
}

// InventoryMaterialOptions lista os materiais ativos que ainda não estão
// no inventário, para o campo "acrescentar material".
func InventoryMaterialOptions(inventoryID int) ([]models.Material, error) {
	rows, err := database.DB.Query(`
		SELECT p.id, p.nome, p.unidade
		FROM produtos p
		WHERE p.ativo = 1
		  AND p.id NOT IN (SELECT produto_id FROM inventario_itens WHERE inventario_id = ?)
		ORDER BY p.nome COLLATE NOCASE
	`, inventoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var materials []models.Material
	for rows.Next() {
		var m models.Material
		if err := rows.Scan(&m.ID, &m.Name, &m.Unit); err != nil {
			return nil, err
		}
		materials = append(materials, m)
	}
	return materials, rows.Err()
}
