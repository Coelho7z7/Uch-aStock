package services

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	database "uchoastock/backend/database"
	"uchoastock/backend/utils"
)

// Situações da solicitação, como ficam gravadas em solicitacoes.status.
const (
	RequestPending   = "PENDENTE"
	RequestApproved  = "APROVADA"
	RequestRejected  = "REJEITADA"
	RequestPartial   = "PARCIAL"
	RequestFulfilled = "ATENDIDA"
	RequestCanceled  = "CANCELADA"
)

// RequestStatusLabels liga cada situação ao texto da tela, na ordem do
// filtro.
var RequestStatusLabels = []struct{ Value, Label string }{
	{RequestPending, "Pendente"},
	{RequestApproved, "Aprovada"},
	{RequestPartial, "Parcial"},
	{RequestFulfilled, "Atendida"},
	{RequestRejected, "Rejeitada"},
	{RequestCanceled, "Cancelada"},
}

// OpenRequestStatuses são as situações de solicitação ainda em aberto:
// alguém ainda precisa agir nela.
var OpenRequestStatuses = []string{RequestPending, RequestApproved, RequestPartial}

// Ações do histórico, como ficam gravadas em solicitacao_eventos.acao.
const (
	RequestEventCreated  = "CRIADA"
	RequestEventApproved = "APROVADA"
	RequestEventRejected = "REJEITADA"
	RequestEventServed   = "ATENDIMENTO"
	RequestEventCanceled = "CANCELADA"
)

// Limites da solicitação.
const (
	MaxRequestItems          = 30
	maxRequestNoteLength     = 200
	maxRejectionReasonLength = 300
)

// requestTransitions diz para quais situações uma solicitação pode ir a
// partir de cada uma. REJEITADA, ATENDIDA e CANCELADA são finais: não
// aparecem como origem.
var requestTransitions = map[string][]string{
	RequestPending:  {RequestApproved, RequestRejected, RequestCanceled},
	RequestApproved: {RequestPartial, RequestFulfilled, RequestCanceled},
	RequestPartial:  {RequestFulfilled, RequestCanceled},
}

// canTransition indica se a solicitação pode passar de from para to. Ficar
// na mesma situação não é transição e dá false.
func canTransition(from, to string) bool {
	for _, allowed := range requestTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

func requestStatusLabel(status string) string {
	for _, s := range RequestStatusLabels {
		if s.Value == status {
			return s.Label
		}
	}
	return status
}

func isValidRequestStatus(status string) bool {
	for _, s := range RequestStatusLabels {
		if s.Value == status {
			return true
		}
	}
	return false
}

// RequestInputError é um erro que pode ir para a tela como está (regra de
// negócio ou dado digitado). Erro do banco nunca vem neste tipo, para não
// vazar detalhe interno.
type RequestInputError struct {
	Message string
}

func (e RequestInputError) Error() string {
	return e.Message
}

var (
	// ErrRequestNotFound vale tanto para solicitação que não existe quanto
	// para a que existe fora do alcance de quem pede: a resposta é a mesma,
	// para não revelar que ela existe.
	ErrRequestNotFound = errors.New("solicitação não encontrada")

	// ErrRequestForbidden é a pessoa ver a solicitação, mas não ter
	// permissão para a ação.
	ErrRequestForbidden = errors.New("sem permissão para esta ação na solicitação")

	// ErrRequestChanged é a situação ter mudado entre a leitura e a
	// gravação (outra pessoa agiu antes).
	ErrRequestChanged = RequestInputError{"a solicitação mudou enquanto você agia: outra pessoa já aprovou, atendeu, rejeitou ou cancelou. Confira a situação atual."}
)

// RequestActor é quem age numa solicitação, com as permissões já
// resolvidas por quem chama (o handler, com can()). O service confere as
// regras com esses dados e nunca olha nome de cargo.
type RequestActor struct {
	UserID int
	// SiteID é a obra vinculada ao usuário (0 = nenhuma).
	SiteID int
	// AllSites: age em qualquer obra (obras.todas).
	AllSites bool
	// ViewAll: vê solicitações de todos os solicitantes
	// (solicitacao.ver_todas). Sem ela, só as próprias.
	ViewAll    bool
	CanCreate  bool
	CanApprove bool
	CanServe   bool
	// ApproveOwn: pode aprovar ou rejeitar a própria solicitação
	// (solicitacao.aprovar_propria, que só o superadmin tem).
	ApproveOwn bool
}

// CanSee indica se a solicitação da obra siteID, criada por requesterID,
// está ao alcance do actor: da obra dele (ou qualquer uma, com AllSites) e
// dele mesmo (ou de qualquer solicitante, com ViewAll). Quem não passa
// aqui não vê nem age: recebe "não encontrada".
func (a RequestActor) CanSee(siteID, requesterID int) bool {
	if !a.AllSites && (a.SiteID == 0 || siteID != a.SiteID) {
		return false
	}
	return a.ViewAll || requesterID == a.UserID
}

// VisibleFilter devolve o recorte de solicitações que o actor pode ver.
// selectedSiteID é a obra do seletor do topo e só vale para quem tem
// AllSites (0 = todas); os outros ficam sempre na própria obra.
func (a RequestActor) VisibleFilter(selectedSiteID int) RequestFilter {
	filter := RequestFilter{SiteID: selectedSiteID}
	if !a.AllSites {
		filter.SiteID = a.SiteID
		if a.SiteID == 0 {
			filter.None = true
		}
	}
	if !a.ViewAll {
		filter.RequesterID = a.UserID
	}
	return filter
}

// RequestItemInput é um item pedido na criação.
type RequestItemInput struct {
	MaterialID int
	Quantity   float64
}

// RequestDelivery é quanto de um item está sendo entregue agora.
type RequestDelivery struct {
	ItemID   int
	Quantity float64
}

// CreateRequest cria a solicitação na obra siteID, com os itens e o evento
// CRIADA, numa transação. Quem chama passa a obra do usuário (ou a do
// seletor, para quem tem AllSites); aqui se confere de novo se ela está ao
// alcance, se é uma obra (não o central) e se está em andamento.
func CreateRequest(actor RequestActor, siteID int, note string, items []RequestItemInput) (int, error) {
	if !actor.CanCreate {
		return 0, ErrRequestForbidden
	}
	if siteID <= 0 {
		if actor.AllSites {
			return 0, RequestInputError{"selecione uma obra no topo para criar a solicitação"}
		}
		return 0, RequestInputError{"você não está vinculado a nenhuma obra: peça a um administrador para definir a sua obra"}
	}
	if !actor.AllSites && siteID != actor.SiteID {
		return 0, ErrRequestForbidden
	}

	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > maxRequestNoteLength {
		return 0, RequestInputError{fmt.Sprintf("a observação pode ter no máximo %d caracteres", maxRequestNoteLength)}
	}
	if len(items) == 0 {
		return 0, RequestInputError{"adicione pelo menos um material à solicitação"}
	}
	if len(items) > MaxRequestItems {
		return 0, RequestInputError{fmt.Sprintf("a solicitação pode ter no máximo %d itens", MaxRequestItems)}
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if err := requireRequestSiteTx(tx, siteID, "receber solicitação nova"); err != nil {
		return 0, err
	}

	seen := map[int]bool{}
	for i := range items {
		var name string
		var active bool
		err := tx.QueryRow(`SELECT nome, ativo FROM produtos WHERE id = ?`, items[i].MaterialID).Scan(&name, &active)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, RequestInputError{"material não encontrado no catálogo"}
		}
		if err != nil {
			return 0, err
		}
		if !active {
			return 0, RequestInputError{fmt.Sprintf("%s foi removido do catálogo e não pode ser pedido", name)}
		}
		if seen[items[i].MaterialID] {
			return 0, RequestInputError{fmt.Sprintf("%s aparece mais de uma vez: junte as quantidades numa linha só", name)}
		}
		seen[items[i].MaterialID] = true

		items[i].Quantity = utils.RoundQuantity(items[i].Quantity)
		if items[i].Quantity <= 0 {
			return 0, RequestInputError{fmt.Sprintf("%s: a quantidade deve ser maior que zero", name)}
		}
	}

	result, err := tx.Exec(`
		INSERT INTO solicitacoes (obra_id, solicitante_id, status, observacao)
		VALUES (?, ?, ?, ?)
	`, siteID, actor.UserID, RequestPending, note)
	if err != nil {
		return 0, err
	}
	requestID64, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	requestID := int(requestID64)

	for _, item := range items {
		if _, err := tx.Exec(`
			INSERT INTO solicitacao_itens (solicitacao_id, produto_id, quantidade_solicitada)
			VALUES (?, ?, ?)
		`, requestID, item.MaterialID, item.Quantity); err != nil {
			return 0, err
		}
	}

	detail := "1 item"
	if len(items) > 1 {
		detail = fmt.Sprintf("%d itens", len(items))
	}
	if err := registerRequestEventTx(tx, requestID, actor.UserID, RequestEventCreated, detail); err != nil {
		return 0, err
	}

	return requestID, tx.Commit()
}

// ApproveRequest aprova uma solicitação pendente. Não reserva estoque: o
// saldo só é conferido (e debitado) no atendimento.
func ApproveRequest(actor RequestActor, requestID int) error {
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	state, err := loadRequestTx(tx, actor, requestID)
	if err != nil {
		return err
	}
	if err := checkDecision(actor, state, RequestApproved); err != nil {
		return err
	}
	if err := requireRequestSiteTx(tx, state.SiteID, "aprovar solicitação"); err != nil {
		return err
	}

	if err := changeRequestStatusTx(tx, requestID, []string{RequestPending}, RequestApproved); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE solicitacoes SET aprovado_por = ?, aprovado_em = CURRENT_TIMESTAMP WHERE id = ?
	`, actor.UserID, requestID); err != nil {
		return err
	}
	if err := registerRequestEventTx(tx, requestID, actor.UserID, RequestEventApproved, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// RejectRequest rejeita uma solicitação pendente. O motivo é obrigatório.
// Obra paralisada ou concluída aceita rejeição: é assim que se limpa o que
// ficou pendente antes de encerrar.
func RejectRequest(actor RequestActor, requestID int, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return RequestInputError{"informe o motivo da rejeição"}
	}
	if utf8.RuneCountInString(reason) > maxRejectionReasonLength {
		return RequestInputError{fmt.Sprintf("o motivo pode ter no máximo %d caracteres", maxRejectionReasonLength)}
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	state, err := loadRequestTx(tx, actor, requestID)
	if err != nil {
		return err
	}
	if err := checkDecision(actor, state, RequestRejected); err != nil {
		return err
	}

	if err := changeRequestStatusTx(tx, requestID, []string{RequestPending}, RequestRejected); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE solicitacoes SET motivo_rejeicao = ? WHERE id = ?`, reason, requestID); err != nil {
		return err
	}
	if err := registerRequestEventTx(tx, requestID, actor.UserID, RequestEventRejected, reason); err != nil {
		return err
	}
	return tx.Commit()
}

// checkDecision reúne as regras de aprovar e rejeitar: permissão, ninguém
// decide a própria solicitação (a não ser com ApproveOwn) e a transição.
func checkDecision(actor RequestActor, state requestState, to string) error {
	if !actor.CanApprove {
		return ErrRequestForbidden
	}
	if state.RequesterID == actor.UserID && !actor.ApproveOwn {
		if to == RequestRejected {
			return RequestInputError{"você não pode rejeitar a própria solicitação: outra pessoa com permissão de aprovar precisa decidir"}
		}
		return RequestInputError{"você não pode aprovar a própria solicitação: outra pessoa com permissão de aprovar precisa decidir"}
	}
	if !canTransition(state.Status, to) {
		return RequestInputError{fmt.Sprintf("uma solicitação %s não pode ser %s",
			strings.ToLower(requestStatusLabel(state.Status)), strings.ToLower(requestStatusLabel(to)))}
	}
	return nil
}

// CancelRequest cancela a solicitação. Pendente: o autor ou quem aprova.
// Aprovada ou parcial: só quem aprova. Cancelar uma parcial não desfaz as
// saídas já feitas: o que foi entregue continua entregue.
func CancelRequest(actor RequestActor, requestID int) error {
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	state, err := loadRequestTx(tx, actor, requestID)
	if err != nil {
		return err
	}
	if !canTransition(state.Status, RequestCanceled) {
		return RequestInputError{fmt.Sprintf("uma solicitação %s não pode ser cancelada",
			strings.ToLower(requestStatusLabel(state.Status)))}
	}
	isAuthor := state.RequesterID == actor.UserID
	if !actor.CanApprove && !(state.Status == RequestPending && isAuthor) {
		return ErrRequestForbidden
	}

	if err := changeRequestStatusTx(tx, requestID, OpenRequestStatuses, RequestCanceled); err != nil {
		return err
	}
	detail := ""
	if state.Status == RequestPartial {
		detail = "o que já tinha sido entregue continua entregue"
	}
	if err := registerRequestEventTx(tx, requestID, actor.UserID, RequestEventCanceled, detail); err != nil {
		return err
	}
	return tx.Commit()
}

// ServeRequest registra a entrega de material de uma solicitação aprovada
// ou parcial, tudo numa transação: confere cada item (pertence à
// solicitação, material ativo, não passa do que falta), faz a saída de
// estoque na obra da solicitação pela mesma regra da tela de estoque
// (registerExitTx), atualiza o atendido, recalcula a situação (ATENDIDA
// quando todos os itens completaram, senão PARCIAL) e grava o evento.
// Qualquer falha desfaz tudo, inclusive as saídas dos itens anteriores.
// Devolve a nova situação.
func ServeRequest(actor RequestActor, requestID int, deliveries []RequestDelivery) (string, error) {
	tx, err := database.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	state, err := loadRequestTx(tx, actor, requestID)
	if err != nil {
		return "", err
	}
	if !actor.CanServe {
		return "", ErrRequestForbidden
	}
	if state.Status != RequestApproved && state.Status != RequestPartial {
		return "", RequestInputError{fmt.Sprintf("só solicitação aprovada ou parcial pode ser atendida (esta está %s)",
			strings.ToLower(requestStatusLabel(state.Status)))}
	}
	if err := requireRequestSiteTx(tx, state.SiteID, "atender solicitação"); err != nil {
		return "", err
	}

	items, err := loadRequestItemsTx(tx, requestID)
	if err != nil {
		return "", err
	}
	byID := map[int]*requestItemState{}
	for i := range items {
		byID[items[i].ID] = &items[i]
	}

	// Lê as quantidades entregues antes de mexer em qualquer coisa.
	delivered := map[int]float64{}
	for _, d := range deliveries {
		item, ok := byID[d.ItemID]
		if !ok {
			return "", RequestInputError{"um dos itens informados não pertence a esta solicitação"}
		}
		if _, repeated := delivered[d.ItemID]; repeated {
			return "", RequestInputError{fmt.Sprintf("%s foi informado mais de uma vez", item.Name)}
		}
		quantity := utils.RoundQuantity(d.Quantity)
		if quantity < 0 {
			return "", RequestInputError{fmt.Sprintf("%s: a quantidade entregue não pode ser negativa", item.Name)}
		}
		delivered[d.ItemID] = quantity
	}

	var summary []string
	for i := range items {
		item := &items[i]
		quantity := delivered[item.ID]
		if quantity == 0 {
			continue
		}
		if !item.Active {
			return "", RequestInputError{fmt.Sprintf("%s foi removido do catálogo e não pode ser atendido", item.Name)}
		}
		missing := utils.RoundQuantity(item.Requested - item.Fulfilled)
		if quantity > missing {
			return "", RequestInputError{fmt.Sprintf("%s: a entrega de %s %s passa do que falta (%s %s)",
				item.Name, utils.FormatQuantity(quantity), item.Unit, utils.FormatQuantity(missing), item.Unit)}
		}

		note := fmt.Sprintf("Solicitação #%d", requestID)
		if err := registerExitTx(tx, item.MaterialID, state.SiteID, quantity, actor.UserID, note, requestID); err != nil {
			if errors.Is(err, ErrInsufficientStock) {
				return "", RequestInputError{fmt.Sprintf("%s: %v", item.Name, err)}
			}
			if errors.Is(err, ErrSiteInInventory) {
				return "", RequestInputError{err.Error()}
			}
			return "", err
		}
		if _, err := tx.Exec(`
			UPDATE solicitacao_itens SET quantidade_atendida = ROUND(quantidade_atendida + ?, 3) WHERE id = ?
		`, quantity, item.ID); err != nil {
			return "", err
		}
		item.Fulfilled = utils.RoundQuantity(item.Fulfilled + quantity)
		summary = append(summary, fmt.Sprintf("%s: %s %s", item.Name, utils.FormatQuantity(quantity), item.Unit))
	}
	if len(summary) == 0 {
		return "", RequestInputError{"informe a quantidade entregue de pelo menos um item"}
	}

	newStatus := RequestFulfilled
	for _, item := range items {
		if item.Fulfilled < item.Requested {
			newStatus = RequestPartial
			break
		}
	}
	// Um atendimento parcial de uma solicitação que já estava parcial não
	// muda a situação, então não é transição. Qualquer mudança passa por
	// canTransition.
	if newStatus != state.Status && !canTransition(state.Status, newStatus) {
		return "", RequestInputError{fmt.Sprintf("uma solicitação %s não pode ficar %s",
			strings.ToLower(requestStatusLabel(state.Status)), strings.ToLower(requestStatusLabel(newStatus)))}
	}
	if err := changeRequestStatusTx(tx, requestID, []string{RequestApproved, RequestPartial}, newStatus); err != nil {
		return "", err
	}
	if err := registerRequestEventTx(tx, requestID, actor.UserID, RequestEventServed, strings.Join(summary, "; ")); err != nil {
		return "", err
	}
	return newStatus, tx.Commit()
}

// requestState é o que as ações precisam saber da solicitação.
type requestState struct {
	ID          int
	SiteID      int
	RequesterID int
	Status      string
}

// loadRequestTx relê a solicitação dentro da transação e confere se ela
// está ao alcance do actor. Fora do alcance é ErrRequestNotFound, igual a
// não existir.
func loadRequestTx(tx *sql.Tx, actor RequestActor, requestID int) (requestState, error) {
	state := requestState{ID: requestID}
	err := tx.QueryRow(`
		SELECT obra_id, solicitante_id, status FROM solicitacoes WHERE id = ?
	`, requestID).Scan(&state.SiteID, &state.RequesterID, &state.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return state, ErrRequestNotFound
	}
	if err != nil {
		return state, err
	}
	if !actor.CanSee(state.SiteID, state.RequesterID) {
		return state, ErrRequestNotFound
	}
	return state, nil
}

// requireRequestSiteTx confere que a obra existe, é uma obra (não o
// almoxarifado central) e está em andamento. action entra na mensagem
// ("obra paralisada não pode aprovar solicitação").
func requireRequestSiteTx(tx *sql.Tx, siteID int, action string) error {
	var siteType, status string
	err := tx.QueryRow(`SELECT tipo, situacao FROM obras WHERE id = ? AND ativo = 1`, siteID).Scan(&siteType, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return RequestInputError{"obra não encontrada"}
	}
	if err != nil {
		return err
	}
	if siteType == SiteTypeCentral {
		return RequestInputError{"solicitação é feita para uma obra, não para o almoxarifado central"}
	}
	if status != SiteStatusInProgress {
		return RequestInputError{fmt.Sprintf("obra %s não pode %s", strings.ToLower(siteStatusLabel(status)), action)}
	}
	return nil
}

// requestItemState é um item da solicitação lido dentro da transação.
type requestItemState struct {
	ID         int
	MaterialID int
	Name       string
	Unit       string
	Active     bool
	Requested  float64
	Fulfilled  float64
}

func loadRequestItemsTx(tx *sql.Tx, requestID int) ([]requestItemState, error) {
	rows, err := tx.Query(`
		SELECT i.id, i.produto_id, p.nome, p.unidade, p.ativo, i.quantidade_solicitada, i.quantidade_atendida
		FROM solicitacao_itens i
		JOIN produtos p ON p.id = i.produto_id
		WHERE i.solicitacao_id = ?
		ORDER BY i.id
	`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []requestItemState
	for rows.Next() {
		var item requestItemState
		if err := rows.Scan(&item.ID, &item.MaterialID, &item.Name, &item.Unit, &item.Active, &item.Requested, &item.Fulfilled); err != nil {
			return nil, err
		}
		item.Requested = utils.RoundQuantity(item.Requested)
		item.Fulfilled = utils.RoundQuantity(item.Fulfilled)
		items = append(items, item)
	}
	return items, rows.Err()
}

// changeRequestStatusTx muda a situação só se ela ainda for uma das
// esperadas (from). A condição está no próprio UPDATE: se outra pessoa
// mudou a solicitação entre a leitura e a gravação, nenhuma linha muda e a
// ação é recusada com ErrRequestChanged, em vez de sobrescrever.
func changeRequestStatusTx(tx *sql.Tx, requestID int, from []string, to string) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(from)), ", ")
	args := []any{to, requestID}
	for _, status := range from {
		args = append(args, status)
	}
	result, err := tx.Exec(`
		UPDATE solicitacoes
		SET status = ?, atualizado_em = CURRENT_TIMESTAMP
		WHERE id = ? AND status IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrRequestChanged
	}
	return nil
}

func registerRequestEventTx(tx *sql.Tx, requestID, userID int, action, detail string) error {
	_, err := tx.Exec(`
		INSERT INTO solicitacao_eventos (solicitacao_id, usuario_id, acao, detalhe)
		VALUES (?, ?, ?, ?)
	`, requestID, userID, action, detail)
	return err
}

// countOpenRequestsTx conta as solicitações em aberto da obra (para o
// bloqueio de encerramento).
func countOpenRequestsTx(tx *sql.Tx, siteID int) (int, error) {
	var count int
	err := tx.QueryRow(`
		SELECT COUNT(*) FROM solicitacoes WHERE obra_id = ? AND status IN (?, ?, ?)
	`, siteID, RequestPending, RequestApproved, RequestPartial).Scan(&count)
	return count, err
}
