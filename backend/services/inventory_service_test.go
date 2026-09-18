package services

import (
	"errors"
	"strings"
	"testing"

	database "uchoastock/backend/database"
)

// inventoryFixture reaproveita o banco de setupRequests (obras A e B,
// cimento 20 e areia 10 na obra A) e monta os actors de inventário com as
// flags que o handler monta com can() para cada cargo.
type inventoryFixture struct {
	requestFixture
	brita       int // material do catálogo sem saldo na obra A
	adminID     int
	iAdmin      InventoryActor
	iSuperadmin InventoryActor
	iManager    InventoryActor // gestor da obra A
	iCounter    InventoryActor // almoxarife da obra A
	iManagerB   InventoryActor // gestor da obra B
}

func setupInventory(t *testing.T) inventoryFixture {
	t.Helper()
	f := inventoryFixture{requestFixture: setupRequests(t)}
	f.adminID = f.admin.UserID
	f.brita = createTestMaterial(t, f.adminID, "Brita", 0, 1)

	approver := InventoryActor{CanView: true, CanCount: true, CanApprove: true}
	f.iAdmin = approver
	f.iAdmin.UserID, f.iAdmin.AllSites = f.admin.UserID, true
	f.iSuperadmin = f.iAdmin
	f.iSuperadmin.UserID, f.iSuperadmin.ApproveOwn = f.superadmin.UserID, true
	f.iManager = approver
	f.iManager.UserID, f.iManager.SiteID = f.manager.UserID, f.siteA
	f.iManagerB = approver
	f.iManagerB.UserID, f.iManagerB.SiteID = f.managerB.UserID, f.siteB
	f.iCounter = InventoryActor{UserID: f.storekeeper.UserID, SiteID: f.siteA, CanView: true, CanCount: true}
	return f
}

// itemIDs devolve o ID de cada item do inventário pelo nome do material.
func itemIDs(t *testing.T, actor InventoryActor, inventoryID int) map[string]int {
	t.Helper()
	inventory, err := GetInventory(actor, inventoryID)
	if err != nil {
		t.Fatalf("ler inventário: %v", err)
	}
	ids := map[string]int{}
	for _, item := range inventory.Items {
		ids[item.MaterialName] = item.ID
	}
	return ids
}

func quantity(value float64) *float64 {
	return &value
}

func assertInventoryInputError(t *testing.T, err error, contains string) {
	t.Helper()
	var inputErr InventoryInputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("esperava InventoryInputError com %q, veio %v", contains, err)
	}
	if !strings.Contains(inputErr.Message, contains) {
		t.Errorf("mensagem = %q, esperado conter %q", inputErr.Message, contains)
	}
}

func inventoryStatusOf(t *testing.T, id int) string {
	t.Helper()
	var status string
	if err := database.DB.QueryRow(`SELECT status FROM inventarios WHERE id = ?`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

// TestInventoryFullFlow segue o caminho inteiro: iniciar congela o saldo e
// bloqueia a obra, a contagem é salva, o envio confere tudo, a aprovação
// gera os ajustes e libera a obra.
func TestInventoryFullFlow(t *testing.T) {
	f := setupInventory(t)

	id, err := StartInventory(f.iCounter, f.siteA)
	if err != nil {
		t.Fatalf("iniciar: %v", err)
	}
	ids := itemIDs(t, f.iCounter, id)
	if len(ids) != 2 || ids["Cimento"] == 0 || ids["Areia"] == 0 {
		t.Fatalf("itens = %v, esperado só Cimento e Areia (os com saldo na obra)", ids)
	}

	// Obra bloqueada: entrada, saída e um segundo inventário.
	if err := AddStockWeb(f.cement, f.siteA, 1, f.adminID, ""); !errors.Is(err, ErrSiteInInventory) {
		t.Errorf("entrada durante o inventário: %v, esperado ErrSiteInInventory", err)
	}
	if err := RegisterStockExitWeb(f.cement, f.siteA, 1, f.adminID, ""); !errors.Is(err, ErrSiteInInventory) {
		t.Errorf("saída durante o inventário: %v, esperado ErrSiteInInventory", err)
	}
	if _, err := StartInventory(f.iManager, f.siteA); err == nil {
		t.Error("segundo inventário aberto na mesma obra deveria ser recusado")
	}
	// A obra B continua livre.
	if err := AddStockWeb(f.cement, f.siteB, 1, f.adminID, ""); err != nil {
		t.Errorf("entrada na obra B: %v", err)
	}

	// Material achado sem saldo no sistema.
	if err := AddInventoryItem(f.iCounter, id, f.brita); err != nil {
		t.Fatalf("acrescentar brita: %v", err)
	}
	assertInventoryInputError(t, AddInventoryItem(f.iCounter, id, f.brita), "já está na contagem")
	ids = itemIDs(t, f.iCounter, id)

	// Envio com item sem contagem: recusado, e nada fica gravado.
	err = SaveInventoryCounts(f.iCounter, id, []InventoryCount{
		{ItemID: ids["Cimento"], Counted: quantity(18), Justification: "2 sacos rasgados"},
		{ItemID: ids["Areia"], Counted: quantity(10)},
	}, true)
	assertInventoryInputError(t, err, "falta contar Brita")
	if inv, _ := GetInventory(f.iCounter, id); inv.CountedCount != 0 {
		t.Errorf("envio recusado não deveria gravar contagem (contados: %d)", inv.CountedCount)
	}

	// Salvar sem enviar grava parcial.
	if err := SaveInventoryCounts(f.iCounter, id, []InventoryCount{
		{ItemID: ids["Cimento"], Counted: quantity(18)},
		{ItemID: ids["Areia"], Counted: quantity(10)},
		{ItemID: ids["Brita"], Counted: quantity(2.5)},
	}, false); err != nil {
		t.Fatalf("salvar: %v", err)
	}
	assertInventoryInputError(t, SaveInventoryCounts(f.iCounter, id, nil, true), "justifique")
	assertInventoryInputError(t, SaveInventoryCounts(f.iCounter, id, []InventoryCount{
		{ItemID: ids["Areia"], Counted: quantity(-1)},
	}, false), "negativa")

	if err := SaveInventoryCounts(f.iCounter, id, []InventoryCount{
		{ItemID: ids["Cimento"], Counted: quantity(18), Justification: "2 sacos rasgados"},
		{ItemID: ids["Brita"], Counted: quantity(2.5), Justification: "sobra da obra B"},
	}, true); err != nil {
		t.Fatalf("enviar: %v", err)
	}
	if got := inventoryStatusOf(t, id); got != InventoryAwaitingApproval {
		t.Fatalf("situação = %s, esperado %s", got, InventoryAwaitingApproval)
	}
	// Enviado, a contagem não muda mais.
	assertInventoryInputError(t, SaveInventoryCounts(f.iCounter, id, nil, false), "só pode ser alterada em contagem")

	// Almoxarife não aprova; gestor de outra obra nem enxerga.
	if err := ApproveInventory(f.iCounter, id); !errors.Is(err, ErrInventoryForbidden) {
		t.Errorf("almoxarife aprovando: %v, esperado ErrInventoryForbidden", err)
	}
	if err := ApproveInventory(f.iManagerB, id); !errors.Is(err, ErrInventoryNotFound) {
		t.Errorf("gestor da obra B aprovando: %v, esperado ErrInventoryNotFound", err)
	}

	if err := ApproveInventory(f.iManager, id); err != nil {
		t.Fatalf("aprovar: %v", err)
	}
	if got := inventoryStatusOf(t, id); got != InventoryApproved {
		t.Errorf("situação = %s, esperado %s", got, InventoryApproved)
	}

	for _, c := range []struct {
		material int
		want     float64
	}{{f.cement, 18}, {f.sand, 10}, {f.brita, 2.5}} {
		if got := balanceOf(t, c.material, f.siteA); got != c.want {
			t.Errorf("saldo do material %d = %v, esperado %v", c.material, got, c.want)
		}
	}

	// Um AJUSTE por diferença, com sinal, apontando para o inventário. A
	// areia bateu: nenhum ajuste.
	rows, err := database.DB.Query(`
		SELECT produto_id, quantidade, inventario_id FROM movimentacoes WHERE tipo = 'AJUSTE' ORDER BY produto_id
	`)
	if err != nil {
		t.Fatal(err)
	}
	adjustments := map[int]float64{}
	for rows.Next() {
		var material, inventory int
		var qty float64
		if err := rows.Scan(&material, &qty, &inventory); err != nil {
			t.Fatal(err)
		}
		if inventory != id {
			t.Errorf("ajuste do material %d aponta para o inventário %d, esperado %d", material, inventory, id)
		}
		adjustments[material] = qty
	}
	rows.Close()
	if len(adjustments) != 2 || adjustments[f.cement] != -2 || adjustments[f.brita] != 2.5 {
		t.Errorf("ajustes = %v, esperado cimento -2 e brita +2,5", adjustments)
	}

	// Obra liberada.
	if err := AddStockWeb(f.cement, f.siteA, 1, f.adminID, ""); err != nil {
		t.Errorf("entrada depois da aprovação: %v", err)
	}
}

// TestInventoryParticipantCannotApprove: quem abriu, enviou ou contou não
// aprova o ajuste. O superadmin (ApproveOwn) pode.
func TestInventoryParticipantCannotApprove(t *testing.T) {
	f := setupInventory(t)

	id, err := StartInventory(f.iCounter, f.siteA)
	if err != nil {
		t.Fatal(err)
	}
	ids := itemIDs(t, f.iCounter, id)
	// O gestor conta o cimento: passa a ser participante.
	if err := SaveInventoryCounts(f.iManager, id, []InventoryCount{{ItemID: ids["Cimento"], Counted: quantity(20)}}, false); err != nil {
		t.Fatal(err)
	}
	if err := SaveInventoryCounts(f.iCounter, id, []InventoryCount{{ItemID: ids["Areia"], Counted: quantity(10)}}, true); err != nil {
		t.Fatal(err)
	}

	assertInventoryInputError(t, ApproveInventory(f.iManager, id), "participou desta contagem")

	// O admin não abriu, não contou e não enviou: aprova.
	if err := ApproveInventory(f.iAdmin, id); err != nil {
		t.Errorf("admin que não participou deveria aprovar: %v", err)
	}

	// Superadmin aprova a contagem que ele mesmo fez.
	id2, err := StartInventory(f.iSuperadmin, f.siteA)
	if err != nil {
		t.Fatal(err)
	}
	ids = itemIDs(t, f.iSuperadmin, id2)
	if err := SaveInventoryCounts(f.iSuperadmin, id2, []InventoryCount{
		{ItemID: ids["Cimento"], Counted: quantity(20)},
		{ItemID: ids["Areia"], Counted: quantity(10)},
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := ApproveInventory(f.iSuperadmin, id2); err != nil {
		t.Errorf("superadmin deveria aprovar a própria contagem: %v", err)
	}
}

func TestInventoryRejectAndCancel(t *testing.T) {
	f := setupInventory(t)

	id, err := StartInventory(f.iCounter, f.siteA)
	if err != nil {
		t.Fatal(err)
	}
	ids := itemIDs(t, f.iCounter, id)
	if err := SaveInventoryCounts(f.iCounter, id, []InventoryCount{
		{ItemID: ids["Cimento"], Counted: quantity(15), Justification: "?"},
		{ItemID: ids["Areia"], Counted: quantity(10)},
	}, true); err != nil {
		t.Fatal(err)
	}

	assertInventoryInputError(t, RejectInventory(f.iManager, id, "  "), "motivo")
	if err := RejectInventory(f.iManager, id, "recontar o cimento"); err != nil {
		t.Fatalf("rejeitar: %v", err)
	}
	inv, err := GetInventory(f.iCounter, id)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Status != InventoryCounting || inv.RejectionReason != "recontar o cimento" {
		t.Errorf("depois de rejeitar: situação %s, motivo %q", inv.Status, inv.RejectionReason)
	}
	// Rejeitar mantém a contagem e a obra bloqueada.
	if inv.CountedCount != 2 {
		t.Errorf("contados depois de rejeitar = %d, esperado 2", inv.CountedCount)
	}
	if err := AddStockWeb(f.cement, f.siteA, 1, f.adminID, ""); !errors.Is(err, ErrSiteInInventory) {
		t.Errorf("obra deveria continuar bloqueada depois de rejeitar: %v", err)
	}

	// Quem abriu cancela enquanto está em contagem; nada de saldo muda.
	if err := CancelInventory(f.iCounter, id); err != nil {
		t.Fatalf("cancelar: %v", err)
	}
	if got := balanceOf(t, f.cement, f.siteA); got != 20 {
		t.Errorf("saldo do cimento depois de cancelar = %v, esperado 20", got)
	}
	if err := AddStockWeb(f.cement, f.siteA, 1, f.adminID, ""); err != nil {
		t.Errorf("obra deveria voltar a aceitar entrada depois de cancelar: %v", err)
	}
	assertInventoryInputError(t, CancelInventory(f.iManager, id), "não pode ser cancelado")

	// Almoxarife que não abriu não cancela; aguardando aprovação, nem quem
	// abriu (só quem aprova).
	id2, err := StartInventory(f.iManager, f.siteA)
	if err != nil {
		t.Fatal(err)
	}
	if err := CancelInventory(f.iCounter, id2); !errors.Is(err, ErrInventoryForbidden) {
		t.Errorf("almoxarife cancelando inventário de outro: %v, esperado ErrInventoryForbidden", err)
	}
}

func TestInventoryScopeAndBlockers(t *testing.T) {
	f := setupInventory(t)

	// Gestor da obra B não abre inventário na obra A nem vê o de lá.
	if _, err := StartInventory(f.iManagerB, f.siteA); !errors.Is(err, ErrInventoryForbidden) {
		t.Errorf("gestor B abrindo na obra A: %v, esperado ErrInventoryForbidden", err)
	}
	id, err := StartInventory(f.iCounter, f.siteA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetInventory(f.iManagerB, id); !errors.Is(err, ErrInventoryNotFound) {
		t.Errorf("gestor B vendo inventário da obra A: %v, esperado ErrInventoryNotFound", err)
	}
	if list, _, _ := ListInventories(f.iManagerB.VisibleFilter(0), 1, 10); len(list) != 0 {
		t.Errorf("gestor B listou %d inventário(s) da obra A", len(list))
	}
	if list, _, _ := ListInventories(f.iAdmin.VisibleFilter(0), 1, 10); len(list) != 1 {
		t.Errorf("admin deveria ver 1 inventário, viu %d", len(list))
	}
	// Sem permissão de ver (solicitante), a lista vem vazia.
	if list, _, _ := ListInventories(InventoryActor{UserID: f.requester.UserID, SiteID: f.siteA}.VisibleFilter(0), 1, 10); len(list) != 0 {
		t.Errorf("sem inventario.ver a lista deveria vir vazia, veio %d", len(list))
	}

	// Com inventário aberto: não encerra a obra, não remove material da
	// contagem e não atende solicitação.
	if err := ChangeSiteStatus(f.siteA, SiteStatusFinished, true); err == nil || !strings.Contains(err.Error(), "inventário") {
		t.Errorf("encerrar obra com inventário aberto: %v", err)
	}
	tijolo := createTestMaterial(t, f.adminID, "Tijolo", 0, 1)
	if err := AddInventoryItem(f.iCounter, id, tijolo); err != nil {
		t.Fatal(err)
	}
	if err := DeleteMaterialWeb(tijolo); err == nil || !strings.Contains(err.Error(), "inventário aberto") {
		t.Errorf("remover material que está na contagem: %v, esperado recusa", err)
	}
	if err := DeleteMaterialWeb(f.brita); err != nil {
		t.Errorf("brita não está na contagem e deveria poder ser removida: %v", err)
	}
}

// TestServeRequestBlockedDuringInventory: atender solicitação é uma saída
// de estoque, então também para durante o inventário, com mensagem na tela
// (e não erro 500).
func TestServeRequestBlockedDuringInventory(t *testing.T) {
	f := setupInventory(t)

	requestID := newRequest(t, f.requester, f.siteA, RequestItemInput{MaterialID: f.cement, Quantity: 2})
	if err := ApproveRequest(f.manager, requestID); err != nil {
		t.Fatal(err)
	}
	if _, err := StartInventory(f.iCounter, f.siteA); err != nil {
		t.Fatal(err)
	}

	_, err := ServeRequest(f.storekeeper, requestID, []RequestDelivery{{ItemID: itemIDOf(t, requestID, f.cement), Quantity: 2}})
	var inputErr RequestInputError
	if !errors.As(err, &inputErr) || !strings.Contains(inputErr.Message, "inventário") {
		t.Errorf("atender durante o inventário: %v, esperado RequestInputError sobre o inventário", err)
	}
	if got := balanceOf(t, f.cement, f.siteA); got != 20 {
		t.Errorf("saldo do cimento = %v, esperado 20 (nada saiu)", got)
	}
}
