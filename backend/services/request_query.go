package services

import (
	"errors"
	"strconv"
	"strings"

	"database/sql"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
	"uchoastock/backend/utils"
)

// RequestFilter é o recorte de uma consulta de requisições. Quem chama
// monta a parte de visibilidade com RequestActor.VisibleFilter e acrescenta
// os filtros da tela (situação e busca).
type RequestFilter struct {
	// SiteID 0 traz todas as obras.
	SiteID int
	// RequesterID 0 traz todos os solicitantes.
	RequesterID int
	// None marca um recorte vazio (usuário sem obra): não traz nada.
	None bool
	// Statuses vazio traz todas as situações.
	Statuses []string
	// Search procura no número (com ou sem "#"), no nome do solicitante e
	// na observação.
	Search string
}

// requestColumns são as colunas da listagem, na ordem de scanRequest. As
// datas saem já no horário local.
const requestColumns = `
	r.id,
	r.obra_id,
	o.nome,
	o.situacao,
	r.solicitante_id,
	u.nome,
	r.status,
	r.observacao,
	COALESCE(a.nome, ''),
	COALESCE(strftime('%d/%m/%Y %H:%M', r.aprovado_em, 'localtime'), ''),
	r.motivo_rejeicao,
	strftime('%d/%m/%Y', r.criado_em, 'localtime'),
	strftime('%d/%m/%Y %H:%M', r.criado_em, 'localtime'),
	strftime('%d/%m/%Y %H:%M', r.atualizado_em, 'localtime'),
	(SELECT COUNT(*) FROM requisicao_itens i WHERE i.requisicao_id = r.id)
`

const requestJoins = `
	FROM requisicoes r
	JOIN obras o ON o.id = r.obra_id
	JOIN usuarios u ON u.id = r.solicitante_id
	LEFT JOIN usuarios a ON a.id = r.aprovado_por
`

func scanRequest(row rowScanner) (models.Request, error) {
	var r models.Request
	err := row.Scan(
		&r.ID, &r.SiteID, &r.SiteName, &r.SiteStatus,
		&r.RequesterID, &r.RequesterName,
		&r.Status, &r.Note,
		&r.ApprovedByName, &r.ApprovedAt, &r.RejectionReason,
		&r.CreatedDate, &r.CreatedAt, &r.UpdatedAt,
		&r.ItemCount,
	)
	r.FormattedStatus = requestStatusLabel(r.Status)
	return r, err
}

// requestWhere monta o WHERE do filtro. Só pedaços fixos de SQL são
// concatenados; todo valor vai por placeholder.
func requestWhere(filter RequestFilter) (string, []any) {
	where := ` WHERE 1 = 1`
	var args []any

	if filter.SiteID > 0 {
		where += ` AND r.obra_id = ?`
		args = append(args, filter.SiteID)
	}
	if filter.RequesterID > 0 {
		where += ` AND r.solicitante_id = ?`
		args = append(args, filter.RequesterID)
	}

	var statuses []string
	for _, status := range filter.Statuses {
		if isValidRequestStatus(status) {
			statuses = append(statuses, status)
		}
	}
	if len(statuses) > 0 {
		where += ` AND r.status IN (` + strings.TrimSuffix(strings.Repeat("?, ", len(statuses)), ", ") + `)`
		for _, status := range statuses {
			args = append(args, status)
		}
	}

	if search := strings.TrimSpace(filter.Search); search != "" {
		number := -1
		if n, err := strconv.Atoi(strings.TrimPrefix(search, "#")); err == nil {
			number = n
		}
		pattern := "%" + search + "%"
		where += ` AND (r.id = ? OR u.nome LIKE ? OR r.observacao LIKE ?)`
		args = append(args, number, pattern, pattern)
	}
	return where, args
}

// ListRequests devolve uma página de requisições, da mais nova para a mais
// antiga, e o total do filtro.
func ListRequests(filter RequestFilter, page, perPage int) ([]models.Request, int, error) {
	if filter.None {
		return nil, 0, nil
	}
	if page < 1 {
		page = 1
	}
	where, args := requestWhere(filter)

	var total int
	if err := database.DB.QueryRow(`SELECT COUNT(*) `+requestJoins+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	requests, err := queryRequests(`SELECT `+requestColumns+requestJoins+where+` ORDER BY r.id DESC LIMIT ? OFFSET ?`,
		append(args, perPage, (page-1)*perPage)...)
	return requests, total, err
}

// OldestOpenRequests devolve as requisições em aberto (pendente, aprovada
// ou parcial) mais antigas do recorte, para o cartão do dashboard.
func OldestOpenRequests(filter RequestFilter, limit int) ([]models.Request, error) {
	if filter.None {
		return nil, nil
	}
	filter.Statuses = OpenRequestStatuses
	where, args := requestWhere(filter)
	return queryRequests(`SELECT `+requestColumns+requestJoins+where+` ORDER BY r.criado_em ASC, r.id ASC LIMIT ?`,
		append(args, limit)...)
}

// CountRequests conta as requisições do recorte (para os contadores da
// barra lateral).
func CountRequests(filter RequestFilter) (int, error) {
	if filter.None {
		return 0, nil
	}
	where, args := requestWhere(filter)
	var total int
	err := database.DB.QueryRow(`SELECT COUNT(*) `+requestJoins+where, args...).Scan(&total)
	return total, err
}

func queryRequests(query string, args ...any) ([]models.Request, error) {
	rows, err := database.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []models.Request
	for rows.Next() {
		request, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

// GetRequest devolve a requisição com itens (e o saldo atual de cada
// material na obra) e histórico. Fora do alcance do actor é
// ErrRequestNotFound, igual a não existir.
func GetRequest(actor RequestActor, requestID int) (*models.Request, error) {
	request, err := scanRequest(database.DB.QueryRow(`SELECT `+requestColumns+requestJoins+` WHERE r.id = ?`, requestID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRequestNotFound
	}
	if err != nil {
		return nil, err
	}
	if !actor.CanSee(request.SiteID, request.RequesterID) {
		return nil, ErrRequestNotFound
	}

	items, err := database.DB.Query(`
		SELECT
			i.id, i.produto_id, p.nome, p.unidade, p.ativo,
			i.quantidade_solicitada, i.quantidade_atendida,
			COALESCE((SELECT s.quantidade FROM saldos s WHERE s.produto_id = i.produto_id AND s.obra_id = ?), 0)
		FROM requisicao_itens i
		JOIN produtos p ON p.id = i.produto_id
		WHERE i.requisicao_id = ?
		ORDER BY i.id
	`, request.SiteID, requestID)
	if err != nil {
		return nil, err
	}
	defer items.Close()
	for items.Next() {
		var item models.RequestItem
		if err := items.Scan(&item.ID, &item.MaterialID, &item.MaterialName, &item.Unit, &item.MaterialActive,
			&item.Requested, &item.Fulfilled, &item.Balance); err != nil {
			return nil, err
		}
		fillRequestItem(&item)
		request.Items = append(request.Items, item)
	}
	if err := items.Err(); err != nil {
		return nil, err
	}

	events, err := database.DB.Query(`
		SELECT u.nome, e.acao, e.detalhe, strftime('%d/%m/%Y %H:%M', e.criado_em, 'localtime')
		FROM requisicao_eventos e
		JOIN usuarios u ON u.id = e.usuario_id
		WHERE e.requisicao_id = ?
		ORDER BY e.id
	`, requestID)
	if err != nil {
		return nil, err
	}
	defer events.Close()
	for events.Next() {
		var event models.RequestEvent
		if err := events.Scan(&event.UserName, &event.Action, &event.Detail, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.FormattedAction = map[string]string{
			RequestEventCreated:  "Criou",
			RequestEventApproved: "Aprovou",
			RequestEventRejected: "Rejeitou",
			RequestEventServed:   "Atendeu",
			RequestEventCanceled: "Cancelou",
		}[event.Action]
		request.Events = append(request.Events, event)
	}
	return &request, events.Err()
}

// fillRequestItem calcula o que falta, a sugestão de entrega (o que falta,
// limitado pelo saldo) e os textos da tela.
func fillRequestItem(item *models.RequestItem) {
	item.Requested = utils.RoundQuantity(item.Requested)
	item.Fulfilled = utils.RoundQuantity(item.Fulfilled)
	item.Balance = utils.RoundQuantity(item.Balance)
	item.Missing = utils.RoundQuantity(item.Requested - item.Fulfilled)
	if item.Missing < 0 {
		item.Missing = 0
	}
	item.Complete = item.Missing == 0
	item.LowBalance = !item.Complete && item.Balance < item.Missing

	suggestion := item.Missing
	if item.Balance < suggestion {
		suggestion = item.Balance
	}
	if suggestion < 0 || !item.MaterialActive {
		suggestion = 0
	}
	item.SuggestedDelivery = utils.FormatQuantityInput(suggestion)

	item.FormattedRequested = utils.FormatQuantity(item.Requested)
	item.FormattedFulfilled = utils.FormatQuantity(item.Fulfilled)
	item.FormattedMissing = utils.FormatQuantity(item.Missing)
	item.FormattedBalance = utils.FormatQuantity(item.Balance)
}

// ActiveMaterialOption é um material do catálogo para o formulário de
// requisição, com o saldo na obra.
type ActiveMaterialOption struct {
	ID               int
	Name             string
	Unit             string
	FormattedBalance string
}

// ListActiveMaterialOptions lista o catálogo ativo em ordem alfabética,
// com o saldo de cada material na obra siteID.
func ListActiveMaterialOptions(siteID int) ([]ActiveMaterialOption, error) {
	rows, err := database.DB.Query(`
		SELECT p.id, p.nome, p.unidade,
			COALESCE((SELECT s.quantidade FROM saldos s WHERE s.produto_id = p.id AND s.obra_id = ?), 0)
		FROM produtos p
		WHERE p.ativo = 1
		ORDER BY p.nome COLLATE NOCASE
	`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var options []ActiveMaterialOption
	for rows.Next() {
		var option ActiveMaterialOption
		var balance float64
		if err := rows.Scan(&option.ID, &option.Name, &option.Unit, &balance); err != nil {
			return nil, err
		}
		option.FormattedBalance = utils.FormatQuantity(balance)
		options = append(options, option)
	}
	return options, rows.Err()
}
