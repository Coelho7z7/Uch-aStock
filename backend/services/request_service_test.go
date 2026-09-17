package services

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	database "uchoastock/backend/database"
)

// requestFixture é um banco com duas obras (A e B), pessoas da obra A em
// cada papel, um gestor da obra B, um admin e materiais com saldo na obra A.
// Os actors têm as flags que o handler monta com can() para cada cargo.
type requestFixture struct {
	siteA, siteB   int
	cement, sand   int // saldo na obra A: 20 sacos de cimento e 10 de areia
	admin          RequestActor
	superadmin     RequestActor
	manager        RequestActor // gestor da obra A
	storekeeper    RequestActor // almoxarife da obra A
	requester      RequestActor // solicitante da obra A
	otherRequester RequestActor // outro solicitante da obra A
	managerB       RequestActor // gestor da obra B
}

func setupRequests(t *testing.T) requestFixture {
	t.Helper()
	adminID := setupTestDB(t)
	f := requestFixture{
		siteA: createTestSite(t, "Obra A", SiteStatusInProgress),
		siteB: createTestSite(t, "Obra B", SiteStatusInProgress),
	}

	user := func(name, role string, site int) int {
		email := strings.ToLower(strings.ReplaceAll(name, " ", ".")) + "@empresa.com"
		if err := CreateUserWeb(name, email, "senha!123", role, site); err != nil {
			t.Fatalf("criar %s: %v", name, err)
		}
		return userIDByEmail(t, email)
	}

	approver := RequestActor{ViewAll: true, CanCreate: true, CanApprove: true, CanServe: true}

	f.admin = approver
	f.admin.UserID, f.admin.AllSites = adminID, true

	f.superadmin = f.admin
	f.superadmin.UserID, f.superadmin.ApproveOwn = user("Super Teste", "admin", 0), true

	f.manager = approver
	f.manager.UserID, f.manager.SiteID = user("Gestor A", "gestor", f.siteA), f.siteA

	f.managerB = approver
	f.managerB.UserID, f.managerB.SiteID = user("Gestor B", "gestor", f.siteB), f.siteB

	f.storekeeper = RequestActor{UserID: user("Almox A", "almoxarife", f.siteA), SiteID: f.siteA, ViewAll: true, CanCreate: true, CanServe: true}
	f.requester = RequestActor{UserID: user("Solic A", "solicitante", f.siteA), SiteID: f.siteA, CanCreate: true}
	f.otherRequester = RequestActor{UserID: user("Solic Dois", "solicitante", f.siteA), SiteID: f.siteA, CanCreate: true}

	f.cement = createTestMaterial(t, adminID, "Cimento", 0, 1)
	f.sand = createTestMaterial(t, adminID, "Areia", 0, 1)
	for _, s := range []struct {
		material int
		quantity float64
	}{{f.cement, 20}, {f.sand, 10}} {
		if err := AddStockWeb(s.material, f.siteA, s.quantity, adminID, ""); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// newRequest cria uma requisição que precisa dar certo.
func newRequest(t *testing.T, actor RequestActor, siteID int, items ...RequestItemInput) int {
	t.Helper()
	id, err := CreateRequest(actor, siteID, "Bloco B - laje", items)
	if err != nil {
		t.Fatalf("criar requisição: %v", err)
	}
	return id
}

func requestStatusOf(t *testing.T, id int) string {
	t.Helper()
	var status string
	if err := database.DB.QueryRow(`SELECT status FROM requisicoes WHERE id = ?`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var count int
	if err := database.DB.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return count
}

func fulfilledOf(t *testing.T, requestID, materialID int) float64 {
	t.Helper()
	var quantity float64
	if err := database.DB.QueryRow(`
		SELECT quantidade_atendida FROM requisicao_itens WHERE requisicao_id = ? AND produto_id = ?
	`, requestID, materialID).Scan(&quantity); err != nil {
		t.Fatal(err)
	}
	return quantity
}

// itemIDOf devolve o ID do item do material na requisição.
func itemIDOf(t *testing.T, requestID, materialID int) int {
	t.Helper()
	var id int
	if err := database.DB.QueryRow(`
		SELECT id FROM requisicao_itens WHERE requisicao_id = ? AND produto_id = ?
	`, requestID, materialID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func eventActions(t *testing.T, requestID int) string {
	t.Helper()
	rows, err := database.DB.Query(`SELECT acao FROM requisicao_eventos WHERE requisicao_id = ? ORDER BY id`, requestID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	return strings.Join(actions, ",")
}

func assertRequestInputError(t *testing.T, err error, contains string) {
	t.Helper()
	var inputErr RequestInputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("esperava RequestInputError com %q, veio %v", contains, err)
	}
	if !strings.Contains(inputErr.Message, contains) {
		t.Errorf("mensagem = %q, esperado conter %q", inputErr.Message, contains)
	}
}

// ---------------------------------------------------------------------

func TestCanTransitionTable(t *testing.T) {
	statuses := []string{RequestPending, RequestApproved, RequestRejected, RequestPartial, RequestFulfilled, RequestCanceled}
	allowed := map[[2]string]bool{
		{RequestPending, RequestApproved}:   true,
		{RequestPending, RequestRejected}:   true,
		{RequestPending, RequestCanceled}:   true,
		{RequestApproved, RequestPartial}:   true,
		{RequestApproved, RequestFulfilled}: true,
		{RequestApproved, RequestCanceled}:  true,
		{RequestPartial, RequestFulfilled}:  true,
		{RequestPartial, RequestCanceled}:   true,
	}
	for _, from := range statuses {
		for _, to := range statuses {
			want := allowed[[2]string{from, to}]
			if got := canTransition(from, to); got != want {
				t.Errorf("canTransition(%s, %s) = %v, esperado %v", from, to, got, want)
			}
		}
	}
	// Situação desconhecida nunca transita.
	if canTransition("QUALQUER", RequestApproved) || canTransition(RequestPending, "QUALQUER") {
		t.Error("situação desconhecida não deveria transitar")
	}
}

func TestCreateRequestValidation(t *testing.T) {
	f := setupRequests(t)
	cement := RequestItemInput{MaterialID: f.cement, Quantity: 5}

	_, err := CreateRequest(f.requester, f.siteA, "", nil)
	assertRequestInputError(t, err, "pelo menos um material")

	_, err = CreateRequest(f.requester, f.siteA, "", []RequestItemInput{cement, {MaterialID: f.cement, Quantity: 1}})
	assertRequestInputError(t, err, "Cimento aparece mais de uma vez")

	for _, quantity := range []float64{0, -3, 0.0001} {
		_, err = CreateRequest(f.requester, f.siteA, "", []RequestItemInput{{MaterialID: f.cement, Quantity: quantity}})
		assertRequestInputError(t, err, "maior que zero")
	}

	_, err = CreateRequest(f.requester, f.siteA, "", []RequestItemInput{{MaterialID: 99999, Quantity: 1}})
	assertRequestInputError(t, err, "material não encontrado")

	removed := createTestMaterial(t, f.admin.UserID, "Cal antiga", 0, 1)
	if err := DeleteMaterialWeb(removed); err != nil {
		t.Fatal(err)
	}
	_, err = CreateRequest(f.requester, f.siteA, "", []RequestItemInput{{MaterialID: removed, Quantity: 1}})
	assertRequestInputError(t, err, "Cal antiga foi removido do catálogo")

	var many []RequestItemInput
	for i := 0; i <= MaxRequestItems; i++ {
		many = append(many, RequestItemInput{MaterialID: createTestMaterial(t, f.admin.UserID, fmt.Sprintf("Item %d", i), 0, 1), Quantity: 1})
	}
	_, err = CreateRequest(f.requester, f.siteA, "", many)
	assertRequestInputError(t, err, "no máximo 30 itens")
	if _, err := CreateRequest(f.requester, f.siteA, "", many[:MaxRequestItems]); err != nil {
		t.Errorf("30 itens deveria passar: %v", err)
	}

	_, err = CreateRequest(f.requester, f.siteA, strings.Repeat("ã", maxRequestNoteLength+1), []RequestItemInput{cement})
	assertRequestInputError(t, err, "observação")

	// Obra: a do solicitante, nunca outra nem o central; em andamento.
	if _, err := CreateRequest(f.requester, f.siteB, "", []RequestItemInput{cement}); !errors.Is(err, ErrRequestForbidden) {
		t.Errorf("solicitante da obra A pedindo na obra B: erro = %v, esperado ErrRequestForbidden", err)
	}
	_, err = CreateRequest(f.admin, centralID(t), "", []RequestItemInput{cement})
	assertRequestInputError(t, err, "almoxarifado central")
	_, err = CreateRequest(f.admin, 0, "", []RequestItemInput{cement})
	assertRequestInputError(t, err, "selecione uma obra")
	noSite := f.requester
	noSite.SiteID = 0
	_, err = CreateRequest(noSite, 0, "", []RequestItemInput{cement})
	assertRequestInputError(t, err, "não está vinculado")
	if _, err := CreateRequest(RequestActor{UserID: f.requester.UserID, SiteID: f.siteA}, f.siteA, "", []RequestItemInput{cement}); !errors.Is(err, ErrRequestForbidden) {
		t.Errorf("sem requisicao.criar: erro = %v, esperado ErrRequestForbidden", err)
	}

	for _, status := range []string{SiteStatusPaused, SiteStatusFinished} {
		setSiteStatus(t, f.siteA, status)
		_, err = CreateRequest(f.requester, f.siteA, "", []RequestItemInput{cement})
		assertRequestInputError(t, err, "não pode receber requisição nova")
	}
	setSiteStatus(t, f.siteA, SiteStatusInProgress)

	// Nenhuma tentativa recusada gravou nada (só a de 30 itens passou).
	if got := countRows(t, `SELECT COUNT(*) FROM requisicoes`); got != 1 {
		t.Errorf("%d requisições gravadas, esperado 1", got)
	}

	// A que dá certo grava itens e o evento CRIADA.
	id := newRequest(t, f.requester, f.siteA, cement, RequestItemInput{MaterialID: f.sand, Quantity: 2.5})
	if status := requestStatusOf(t, id); status != RequestPending {
		t.Errorf("situação inicial = %s", status)
	}
	if got := countRows(t, `SELECT COUNT(*) FROM requisicao_itens WHERE requisicao_id = ?`, id); got != 2 {
		t.Errorf("%d itens, esperado 2", got)
	}
	if got := eventActions(t, id); got != "CRIADA" {
		t.Errorf("eventos = %s, esperado CRIADA", got)
	}
}

func TestNobodyDecidesOwnRequestExceptSuperadmin(t *testing.T) {
	f := setupRequests(t)
	item := RequestItemInput{MaterialID: f.cement, Quantity: 2}

	own := newRequest(t, f.manager, f.siteA, item)
	assertRequestInputError(t, ApproveRequest(f.manager, own), "não pode aprovar a própria")
	assertRequestInputError(t, RejectRequest(f.manager, own, "não precisa mais"), "não pode rejeitar a própria")
	if status := requestStatusOf(t, own); status != RequestPending {
		t.Fatalf("a própria requisição mudou para %s", status)
	}

	// O admin comum também não decide a própria.
	adminOwn := newRequest(t, f.admin, f.siteA, item)
	assertRequestInputError(t, ApproveRequest(f.admin, adminOwn), "não pode aprovar a própria")

	// O superadmin pode.
	superOwn := newRequest(t, f.superadmin, f.siteA, item)
	if err := ApproveRequest(f.superadmin, superOwn); err != nil {
		t.Errorf("superadmin aprovando a própria: %v", err)
	}

	// Outra pessoa com permissão aprova a do gestor, e fica registrado quem.
	if err := ApproveRequest(f.admin, own); err != nil {
		t.Fatal(err)
	}
	var approvedBy int
	if err := database.DB.QueryRow(`SELECT aprovado_por FROM requisicoes WHERE id = ? AND aprovado_em IS NOT NULL`, own).Scan(&approvedBy); err != nil {
		t.Fatal(err)
	}
	if approvedBy != f.admin.UserID || eventActions(t, own) != "CRIADA,APROVADA" {
		t.Errorf("aprovado_por = %d e eventos %s", approvedBy, eventActions(t, own))
	}

	// Almoxarife vê, mas não aprova.
	other := newRequest(t, f.requester, f.siteA, item)
	if err := ApproveRequest(f.storekeeper, other); !errors.Is(err, ErrRequestForbidden) {
		t.Errorf("almoxarife aprovando: erro = %v, esperado ErrRequestForbidden", err)
	}
	// Aprovar de novo uma aprovada é transição proibida.
	assertRequestInputError(t, ApproveRequest(f.admin, own), "não pode ser aprovada")
}

func TestRejectRequiresReason(t *testing.T) {
	f := setupRequests(t)
	id := newRequest(t, f.requester, f.siteA, RequestItemInput{MaterialID: f.cement, Quantity: 2})

	for _, reason := range []string{"", "   ", "\n\t"} {
		assertRequestInputError(t, RejectRequest(f.manager, id, reason), "informe o motivo")
	}
	if status := requestStatusOf(t, id); status != RequestPending {
		t.Fatalf("rejeição sem motivo mudou a situação para %s", status)
	}

	if err := RejectRequest(f.manager, id, "  Pedido duplicado  "); err != nil {
		t.Fatal(err)
	}
	var reason string
	if err := database.DB.QueryRow(`SELECT motivo_rejeicao FROM requisicoes WHERE id = ?`, id).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if requestStatusOf(t, id) != RequestRejected || reason != "Pedido duplicado" || eventActions(t, id) != "CRIADA,REJEITADA" {
		t.Errorf("depois de rejeitar: %s, motivo %q, eventos %s", requestStatusOf(t, id), reason, eventActions(t, id))
	}
}

func TestCancelRules(t *testing.T) {
	f := setupRequests(t)
	item := RequestItemInput{MaterialID: f.cement, Quantity: 2}

	// O autor cancela a própria pendente.
	own := newRequest(t, f.requester, f.siteA, item)
	if err := CancelRequest(f.requester, own); err != nil {
		t.Errorf("autor cancelando a própria pendente: %v", err)
	}

	// Outro solicitante nem enxerga; o almoxarife enxerga, mas não cancela.
	pending := newRequest(t, f.requester, f.siteA, item)
	if err := CancelRequest(f.otherRequester, pending); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("outro solicitante cancelando: erro = %v, esperado ErrRequestNotFound", err)
	}
	if err := CancelRequest(f.storekeeper, pending); !errors.Is(err, ErrRequestForbidden) {
		t.Errorf("almoxarife cancelando pendente alheia: erro = %v, esperado ErrRequestForbidden", err)
	}

	// Aprovada: o autor solicitante não cancela mais; quem aprova, sim.
	if err := ApproveRequest(f.manager, pending); err != nil {
		t.Fatal(err)
	}
	if err := CancelRequest(f.requester, pending); !errors.Is(err, ErrRequestForbidden) {
		t.Errorf("solicitante cancelando aprovada: erro = %v, esperado ErrRequestForbidden", err)
	}
	if requestStatusOf(t, pending) != RequestApproved {
		t.Fatal("a tentativa recusada mudou a situação")
	}
	if err := CancelRequest(f.manager, pending); err != nil {
		t.Errorf("gestor cancelando aprovada: %v", err)
	}

	// Final não cancela.
	assertRequestInputError(t, CancelRequest(f.manager, pending), "cancelada não pode ser cancelada")
	rejected := newRequest(t, f.requester, f.siteA, item)
	if err := RejectRequest(f.manager, rejected, "sem verba"); err != nil {
		t.Fatal(err)
	}
	assertRequestInputError(t, CancelRequest(f.manager, rejected), "rejeitada não pode ser cancelada")
}

func TestServePartialThenTotal(t *testing.T) {
	f := setupRequests(t)
	id := newRequest(t, f.requester, f.siteA,
		RequestItemInput{MaterialID: f.cement, Quantity: 10},
		RequestItemInput{MaterialID: f.sand, Quantity: 4})
	if err := ApproveRequest(f.manager, id); err != nil {
		t.Fatal(err)
	}
	cementItem, sandItem := itemIDOf(t, id, f.cement), itemIDOf(t, id, f.sand)

	// 1º atendimento: 6 de cimento, areia fica para depois (0 é ignorado).
	status, err := ServeRequest(f.storekeeper, id, []RequestDelivery{{cementItem, 6}, {sandItem, 0}})
	if err != nil {
		t.Fatal(err)
	}
	if status != RequestPartial || requestStatusOf(t, id) != RequestPartial {
		t.Errorf("depois do 1º atendimento: %s", status)
	}
	if balanceOf(t, f.cement, f.siteA) != 14 || fulfilledOf(t, id, f.cement) != 6 || fulfilledOf(t, id, f.sand) != 0 {
		t.Errorf("saldo cimento %v, atendido cimento %v, areia %v", balanceOf(t, f.cement, f.siteA), fulfilledOf(t, id, f.cement), fulfilledOf(t, id, f.sand))
	}

	// 2º atendimento: o resto dos dois.
	status, err = ServeRequest(f.storekeeper, id, []RequestDelivery{{cementItem, 4}, {sandItem, 4}})
	if err != nil {
		t.Fatal(err)
	}
	if status != RequestFulfilled || requestStatusOf(t, id) != RequestFulfilled {
		t.Errorf("depois do 2º atendimento: %s", status)
	}
	if balanceOf(t, f.cement, f.siteA) != 10 || balanceOf(t, f.sand, f.siteA) != 6 {
		t.Errorf("saldos finais: cimento %v, areia %v", balanceOf(t, f.cement, f.siteA), balanceOf(t, f.sand, f.siteA))
	}
	if fulfilledOf(t, id, f.cement) != 10 || fulfilledOf(t, id, f.sand) != 4 {
		t.Error("quantidade atendida final errada")
	}

	// Três saídas, todas ligadas à requisição, na obra A, com o número na observação.
	if got := countRows(t, `
		SELECT COUNT(*) FROM movimentacoes
		WHERE requisicao_id = ? AND tipo = 'SAIDA' AND obra_id = ? AND usuario_id = ? AND observacao = ?
	`, id, f.siteA, f.storekeeper.UserID, fmt.Sprintf("Requisição #%d", id)); got != 3 {
		t.Errorf("%d saídas ligadas à requisição, esperado 3", got)
	}
	if got := eventActions(t, id); got != "CRIADA,APROVADA,ATENDIMENTO,ATENDIMENTO" {
		t.Errorf("eventos = %s", got)
	}
	var lastDetail string
	if err := database.DB.QueryRow(`SELECT detalhe FROM requisicao_eventos WHERE requisicao_id = ? ORDER BY id DESC LIMIT 1`, id).Scan(&lastDetail); err != nil {
		t.Fatal(err)
	}
	if lastDetail != "Cimento: 4 saco; Areia: 4 saco" {
		t.Errorf("resumo do atendimento = %q", lastDetail)
	}

	// Cancelar uma atendida não é possível; e numa parcial as saídas ficam.
	partial := newRequest(t, f.requester, f.siteA, RequestItemInput{MaterialID: f.cement, Quantity: 5})
	if err := ApproveRequest(f.manager, partial); err != nil {
		t.Fatal(err)
	}
	if _, err := ServeRequest(f.storekeeper, partial, []RequestDelivery{{itemIDOf(t, partial, f.cement), 2}}); err != nil {
		t.Fatal(err)
	}
	if err := CancelRequest(f.manager, partial); err != nil {
		t.Fatal(err)
	}
	if balanceOf(t, f.cement, f.siteA) != 8 || countRows(t, `SELECT COUNT(*) FROM movimentacoes WHERE requisicao_id = ?`, partial) != 1 {
		t.Error("cancelar a parcial desfez a saída já feita")
	}
}

func TestServeInsufficientStockRollsBackEverything(t *testing.T) {
	f := setupRequests(t)
	// Areia: pede 12 com saldo 10. Cimento vem antes e teria saldo.
	id := newRequest(t, f.requester, f.siteA,
		RequestItemInput{MaterialID: f.cement, Quantity: 5},
		RequestItemInput{MaterialID: f.sand, Quantity: 12})
	if err := ApproveRequest(f.manager, id); err != nil {
		t.Fatal(err)
	}
	movementsBefore := countRows(t, `SELECT COUNT(*) FROM movimentacoes`)

	_, err := ServeRequest(f.storekeeper, id, []RequestDelivery{{itemIDOf(t, id, f.cement), 5}, {itemIDOf(t, id, f.sand), 12}})
	assertRequestInputError(t, err, "Areia: estoque insuficiente")

	if got := countRows(t, `SELECT COUNT(*) FROM movimentacoes`); got != movementsBefore {
		t.Errorf("%d movimentações novas depois da falha, esperado 0", got-movementsBefore)
	}
	if balanceOf(t, f.cement, f.siteA) != 20 || balanceOf(t, f.sand, f.siteA) != 10 {
		t.Errorf("saldos mudaram: cimento %v, areia %v", balanceOf(t, f.cement, f.siteA), balanceOf(t, f.sand, f.siteA))
	}
	if fulfilledOf(t, id, f.cement) != 0 || requestStatusOf(t, id) != RequestApproved || eventActions(t, id) != "CRIADA,APROVADA" {
		t.Errorf("requisição mudou: atendido %v, situação %s, eventos %s", fulfilledOf(t, id, f.cement), requestStatusOf(t, id), eventActions(t, id))
	}
}

func TestServeRejectsInvalidDeliveries(t *testing.T) {
	f := setupRequests(t)
	id := newRequest(t, f.requester, f.siteA, RequestItemInput{MaterialID: f.cement, Quantity: 3}, RequestItemInput{MaterialID: f.sand, Quantity: 2})
	if err := ApproveRequest(f.manager, id); err != nil {
		t.Fatal(err)
	}
	cementItem := itemIDOf(t, id, f.cement)
	other := newRequest(t, f.requester, f.siteA, RequestItemInput{MaterialID: f.cement, Quantity: 1})

	cases := []struct {
		name       string
		deliveries []RequestDelivery
		want       string
	}{
		{"acima do que falta", []RequestDelivery{{cementItem, 4}}, "passa do que falta"},
		{"tudo zero", []RequestDelivery{{cementItem, 0}}, "pelo menos um item"},
		{"nenhum item", nil, "pelo menos um item"},
		{"negativo", []RequestDelivery{{cementItem, -1}}, "não pode ser negativa"},
		{"item repetido", []RequestDelivery{{cementItem, 1}, {cementItem, 1}}, "mais de uma vez"},
		{"item de outra requisição", []RequestDelivery{{itemIDOf(t, other, f.cement), 1}}, "não pertence"},
	}
	for _, c := range cases {
		_, err := ServeRequest(f.storekeeper, id, c.deliveries)
		assertRequestInputError(t, err, c.want)
	}

	// Material removido depois da criação: a requisição continua, mas o
	// item não é atendido (a areia, que continua ativa, é).
	if _, err := database.DB.Exec(`UPDATE produtos SET ativo = 0 WHERE id = ?`, f.cement); err != nil {
		t.Fatal(err)
	}
	_, err := ServeRequest(f.storekeeper, id, []RequestDelivery{{cementItem, 1}})
	assertRequestInputError(t, err, "Cimento foi removido do catálogo e não pode ser atendido")
	if _, err := ServeRequest(f.storekeeper, id, []RequestDelivery{{itemIDOf(t, id, f.sand), 2}}); err != nil {
		t.Errorf("item ativo da mesma requisição: %v", err)
	}
	request, err := GetRequest(f.storekeeper, id)
	if err != nil {
		t.Fatal(err)
	}
	if request.Status != RequestPartial || request.Items[0].MaterialActive || request.Items[0].SuggestedDelivery != "0" {
		t.Errorf("requisição com material removido: situação %s, item %+v", request.Status, request.Items[0])
	}

	if got := countRows(t, `SELECT COUNT(*) FROM movimentacoes WHERE requisicao_id = ?`, id); got != 1 {
		t.Errorf("%d saídas gravadas, esperado só a da areia", got)
	}
}

func TestServeRequiresApprovedOrPartial(t *testing.T) {
	f := setupRequests(t)
	item := RequestItemInput{MaterialID: f.cement, Quantity: 1}

	pending := newRequest(t, f.requester, f.siteA, item)
	rejected := newRequest(t, f.requester, f.siteA, item)
	if err := RejectRequest(f.manager, rejected, "não precisa"); err != nil {
		t.Fatal(err)
	}
	canceled := newRequest(t, f.requester, f.siteA, item)
	if err := CancelRequest(f.requester, canceled); err != nil {
		t.Fatal(err)
	}

	for name, id := range map[string]int{"pendente": pending, "rejeitada": rejected, "cancelada": canceled} {
		_, err := ServeRequest(f.storekeeper, id, []RequestDelivery{{itemIDOf(t, id, f.cement), 1}})
		assertRequestInputError(t, err, "só requisição aprovada ou parcial")
		if got := countRows(t, `SELECT COUNT(*) FROM movimentacoes WHERE requisicao_id = ?`, id); got != 0 {
			t.Errorf("%s: %d saídas gravadas", name, got)
		}
	}

	// Solicitante não atende.
	if err := ApproveRequest(f.manager, pending); err != nil {
		t.Fatal(err)
	}
	if _, err := ServeRequest(f.requester, pending, []RequestDelivery{{itemIDOf(t, pending, f.cement), 1}}); !errors.Is(err, ErrRequestForbidden) {
		t.Errorf("solicitante atendendo a própria: erro = %v, esperado ErrRequestForbidden", err)
	}
}

// Dois atendimentos montados com a mesma tela (a requisição ainda
// aprovada, faltando tudo): o primeiro atende, o segundo é recusado e não
// grava nada. Depois, o mesmo com duas goroutines ao mesmo tempo.
func TestServeTwiceWithStaleState(t *testing.T) {
	f := setupRequests(t)
	id := newRequest(t, f.requester, f.siteA, RequestItemInput{MaterialID: f.cement, Quantity: 5})
	if err := ApproveRequest(f.manager, id); err != nil {
		t.Fatal(err)
	}
	delivery := []RequestDelivery{{itemIDOf(t, id, f.cement), 5}}

	if _, err := ServeRequest(f.storekeeper, id, delivery); err != nil {
		t.Fatal(err)
	}
	_, err := ServeRequest(f.manager, id, delivery)
	assertRequestInputError(t, err, "só requisição aprovada ou parcial")
	if balanceOf(t, f.cement, f.siteA) != 15 || countRows(t, `SELECT COUNT(*) FROM movimentacoes WHERE requisicao_id = ?`, id) != 1 {
		t.Error("o segundo atendimento gravou alguma coisa")
	}

	concurrent := newRequest(t, f.requester, f.siteA, RequestItemInput{MaterialID: f.cement, Quantity: 5})
	if err := ApproveRequest(f.manager, concurrent); err != nil {
		t.Fatal(err)
	}
	delivery = []RequestDelivery{{itemIDOf(t, concurrent, f.cement), 5}}
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i, actor := range []RequestActor{f.storekeeper, f.manager} {
		wg.Add(1)
		go func(i int, actor RequestActor) {
			defer wg.Done()
			_, results[i] = ServeRequest(actor, concurrent, delivery)
		}(i, actor)
	}
	wg.Wait()
	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Errorf("%d atendimentos simultâneos passaram (%v), esperado 1", successes, results)
	}
	if balanceOf(t, f.cement, f.siteA) != 10 || countRows(t, `SELECT COUNT(*) FROM movimentacoes WHERE requisicao_id = ?`, concurrent) != 1 {
		t.Error("atendimentos simultâneos gravaram mais de uma saída")
	}
}

// changeRequestStatusTx só muda se a situação ainda for a esperada: com
// uma situação "velha", nenhuma linha muda e volta ErrRequestChanged.
func TestChangeRequestStatusGuardsAgainstStaleStatus(t *testing.T) {
	f := setupRequests(t)
	id := newRequest(t, f.requester, f.siteA, RequestItemInput{MaterialID: f.cement, Quantity: 1})

	tx, err := database.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Quem leu a requisição como aprovada tenta marcá-la como atendida,
	// mas no banco ela ainda está pendente.
	if err := changeRequestStatusTx(tx, id, []string{RequestApproved, RequestPartial}, RequestFulfilled); !errors.Is(err, ErrRequestChanged) {
		t.Errorf("situação velha: erro = %v, esperado ErrRequestChanged", err)
	}
	if err := changeRequestStatusTx(tx, id, []string{RequestPending}, RequestApproved); err != nil {
		t.Errorf("situação certa: %v", err)
	}
}

func TestPausedSiteBlocksApproveAndServe(t *testing.T) {
	f := setupRequests(t)
	item := RequestItemInput{MaterialID: f.cement, Quantity: 1}
	toApprove := newRequest(t, f.requester, f.siteA, item)
	toServe := newRequest(t, f.requester, f.siteA, item)
	toReject := newRequest(t, f.requester, f.siteA, item)
	if err := ApproveRequest(f.manager, toServe); err != nil {
		t.Fatal(err)
	}

	setSiteStatus(t, f.siteA, SiteStatusPaused)
	assertRequestInputError(t, ApproveRequest(f.manager, toApprove), "paralisada não pode aprovar")
	_, err := ServeRequest(f.storekeeper, toServe, []RequestDelivery{{itemIDOf(t, toServe, f.cement), 1}})
	assertRequestInputError(t, err, "paralisada não pode atender")

	// Rejeitar e cancelar continuam possíveis, para limpar a obra.
	if err := RejectRequest(f.manager, toReject, "obra parada"); err != nil {
		t.Errorf("rejeitar em obra paralisada: %v", err)
	}
	if err := CancelRequest(f.manager, toServe); err != nil {
		t.Errorf("cancelar em obra paralisada: %v", err)
	}
}

func TestFinishSiteBlockedByOpenRequests(t *testing.T) {
	f := setupRequests(t)
	empty := createTestSite(t, "Obra vazia", SiteStatusInProgress)
	emptyManager := f.manager
	emptyManager.SiteID = empty

	pending := newRequest(t, emptyManager, empty, RequestItemInput{MaterialID: f.cement, Quantity: 1})
	approved := newRequest(t, f.admin, empty, RequestItemInput{MaterialID: f.cement, Quantity: 1})
	if err := ApproveRequest(emptyManager, approved); err != nil {
		t.Fatal(err)
	}

	assertInputError(t, ChangeSiteStatus(empty, SiteStatusFinished, true), "2 requisições ainda estão em aberto")
	if err := CancelRequest(f.admin, pending); err != nil {
		t.Fatal(err)
	}
	assertInputError(t, ChangeSiteStatus(empty, SiteStatusFinished, true), "1 requisição ainda está em aberto")
	if err := CancelRequest(f.admin, approved); err != nil {
		t.Fatal(err)
	}
	if err := ChangeSiteStatus(empty, SiteStatusFinished, true); err != nil {
		t.Errorf("sem requisição em aberto e sem saldo, deveria encerrar: %v", err)
	}

	// Saldo e requisição juntos: a mensagem fala dos dois.
	newRequest(t, f.manager, f.siteA, RequestItemInput{MaterialID: f.cement, Quantity: 1})
	err := ChangeSiteStatus(f.siteA, SiteStatusFinished, true)
	assertInputError(t, err, "2 materiais ainda têm saldo")
	assertInputError(t, err, "1 requisição ainda está em aberto")
}

func TestRequestVisibility(t *testing.T) {
	f := setupRequests(t)
	item := RequestItemInput{MaterialID: f.cement, Quantity: 1}

	mine := newRequest(t, f.requester, f.siteA, item)
	colleague := newRequest(t, f.otherRequester, f.siteA, item)
	inB := newRequest(t, f.managerB, f.siteB, item)

	ids := func(actor RequestActor, selected int) string {
		t.Helper()
		requests, total, err := ListRequests(actor.VisibleFilter(selected), 1, 50)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range requests {
			out = append(out, fmt.Sprint(r.ID))
		}
		if total != len(requests) {
			t.Errorf("total %d diferente de %d linhas", total, len(requests))
		}
		return strings.Join(out, ",")
	}

	want := func(list ...int) string {
		var out []string
		for _, id := range list {
			out = append(out, fmt.Sprint(id))
		}
		return strings.Join(out, ",")
	}

	if got := ids(f.requester, 0); got != want(mine) {
		t.Errorf("solicitante vê %s, esperado só a dele (%d)", got, mine)
	}
	if got := ids(f.manager, f.siteB); got != want(colleague, mine) {
		t.Errorf("gestor A (mesmo escolhendo B no seletor) vê %s", got)
	}
	if got := ids(f.admin, 0); got != want(inB, colleague, mine) {
		t.Errorf("admin em Todas as obras vê %s", got)
	}
	if got := ids(f.admin, f.siteB); got != want(inB) {
		t.Errorf("admin com a obra B selecionada vê %s", got)
	}
	noSite := RequestActor{UserID: f.requester.UserID, ViewAll: true}
	if got := ids(noSite, f.siteA); got != "" {
		t.Errorf("usuário sem obra vê %s", got)
	}

	for name, c := range map[string]struct {
		actor RequestActor
		id    int
	}{
		"outro solicitante da mesma obra": {f.otherRequester, mine},
		"gestor da obra B":                {f.managerB, mine},
		"gestor da obra A na obra B":      {f.manager, inB},
	} {
		if _, err := GetRequest(c.actor, c.id); !errors.Is(err, ErrRequestNotFound) {
			t.Errorf("%s: erro = %v, esperado ErrRequestNotFound", name, err)
		}
	}
	if _, err := GetRequest(f.manager, 99999); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("requisição inexistente: %v", err)
	}

	// Filtros da tela e contadores.
	filter := f.admin.VisibleFilter(0)
	filter.Search = fmt.Sprintf("#%d", inB)
	if requests, _, _ := ListRequests(filter, 1, 10); len(requests) != 1 || requests[0].ID != inB {
		t.Errorf("busca pelo número: %+v", requests)
	}
	filter.Search = "Solic Dois"
	if requests, _, _ := ListRequests(filter, 1, 10); len(requests) != 1 || requests[0].ID != colleague {
		t.Errorf("busca pelo solicitante: %+v", requests)
	}
	filter.Search = "laje"
	if requests, _, _ := ListRequests(filter, 1, 10); len(requests) != 3 {
		t.Errorf("busca pela observação: %d", len(requests))
	}

	if err := ApproveRequest(f.manager, mine); err != nil {
		t.Fatal(err)
	}
	pendingA := f.manager.VisibleFilter(0)
	pendingA.Statuses = []string{RequestPending}
	if count, _ := CountRequests(pendingA); count != 1 {
		t.Errorf("pendentes na obra A = %d, esperado 1", count)
	}
	oldest, err := OldestOpenRequests(f.admin.VisibleFilter(0), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(oldest) != 2 || oldest[0].ID != mine || oldest[1].ID != colleague {
		t.Errorf("mais antigas em aberto: %+v", oldest)
	}
}

func TestGetRequestItemsAndHistory(t *testing.T) {
	f := setupRequests(t)
	id := newRequest(t, f.requester, f.siteA,
		RequestItemInput{MaterialID: f.cement, Quantity: 25}, // saldo 20: não cobre
		RequestItemInput{MaterialID: f.sand, Quantity: 2.5})
	if err := ApproveRequest(f.manager, id); err != nil {
		t.Fatal(err)
	}

	request, err := GetRequest(f.storekeeper, id)
	if err != nil {
		t.Fatal(err)
	}
	if request.RequesterName != "Solic A" || request.SiteName != "Obra A" || request.FormattedStatus != "Aprovada" || request.ApprovedByName != "Gestor A" || request.ItemCount != 2 {
		t.Errorf("requisição = %+v", request)
	}
	cement, sand := request.Items[0], request.Items[1]
	if cement.FormattedMissing != "25" || cement.FormattedBalance != "20" || !cement.LowBalance || cement.SuggestedDelivery != "20" {
		t.Errorf("item cimento = %+v", cement)
	}
	if sand.LowBalance || sand.SuggestedDelivery != "2,5" {
		t.Errorf("item areia = %+v", sand)
	}
	if len(request.Events) != 2 || request.Events[0].FormattedAction != "Criou" || request.Events[1].UserName != "Gestor A" {
		t.Errorf("histórico = %+v", request.Events)
	}
}

// Material em requisição em aberto não pode ser removido do catálogo; em
// requisição rejeitada, atendida ou cancelada, pode.
func TestDeleteMaterialBlockedByOpenRequests(t *testing.T) {
	f := setupRequests(t)
	// Sem saldo em lugar nenhum, para testar só a regra das requisições.
	nail := createTestMaterial(t, f.admin.UserID, "Prego", 0, 1)
	item := RequestItemInput{MaterialID: nail, Quantity: 3}
	isActive := func() bool {
		t.Helper()
		return countRows(t, `SELECT COUNT(*) FROM produtos WHERE id = ? AND ativo = 1`, nail) == 1
	}

	pending := newRequest(t, f.requester, f.siteA, item)
	err := DeleteMaterialWeb(nail)
	if err == nil || !strings.Contains(err.Error(), "está em 1 requisição em aberto") {
		t.Errorf("com 1 requisição pendente: erro = %v", err)
	}

	approved := newRequest(t, f.manager, f.siteA, item)
	if err := ApproveRequest(f.admin, approved); err != nil {
		t.Fatal(err)
	}
	err = DeleteMaterialWeb(nail)
	if err == nil || !strings.Contains(err.Error(), "está em 2 requisições em aberto") {
		t.Errorf("com pendente e aprovada: erro = %v", err)
	}
	if !isActive() {
		t.Fatal("a remoção recusada desativou o material")
	}

	// Saldo e requisição juntos: a mensagem fala dos dois.
	if err := AddStockWeb(nail, f.siteA, 1, f.admin.UserID, ""); err != nil {
		t.Fatal(err)
	}
	err = DeleteMaterialWeb(nail)
	if err == nil || !strings.Contains(err.Error(), "saldo em 1 obra") || !strings.Contains(err.Error(), "2 requisições em aberto") {
		t.Errorf("com saldo e requisições: erro = %v", err)
	}
	if _, err := ServeRequest(f.storekeeper, approved, []RequestDelivery{{itemIDOf(t, approved, nail), 1}}); err != nil {
		t.Fatal(err)
	}
	// Parcial continua contando.
	err = DeleteMaterialWeb(nail)
	if err == nil || !strings.Contains(err.Error(), "2 requisições em aberto") || strings.Contains(err.Error(), "saldo") {
		t.Errorf("com parcial e pendente, sem saldo: erro = %v", err)
	}

	// Rejeitada e cancelada não contam mais.
	if err := RejectRequest(f.manager, pending, "não precisa"); err != nil {
		t.Fatal(err)
	}
	if err := CancelRequest(f.manager, approved); err != nil {
		t.Fatal(err)
	}
	if err := DeleteMaterialWeb(nail); err != nil {
		t.Errorf("sem requisição em aberto e sem saldo, deveria remover: %v", err)
	}
	if isActive() {
		t.Error("o material deveria ter sido removido")
	}
}
