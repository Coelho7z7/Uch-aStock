package services

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	database "uchoastock/backend/database"
)

func TestCreateMaterialWebValidatesInput(t *testing.T) {
	userID := setupTestDB(t)
	central := centralID(t)
	finished := createTestSite(t, "Obra entregue", SiteStatusFinished)

	cases := []struct {
		name     string
		quantity float64
		unit     string
		minimum  float64
		siteID   int
	}{
		{name: "", quantity: 1, unit: "saco", minimum: 10, siteID: central},
		{name: "   ", quantity: 1, unit: "saco", minimum: 10, siteID: central},
		{name: "Cimento", quantity: -1, unit: "saco", minimum: 10, siteID: central},
		{name: "Cimento", quantity: 1, unit: "sacos", minimum: 10, siteID: central},
		{name: "Cimento", quantity: 1, unit: "saco", minimum: -2, siteID: central},
		{name: "Cimento", quantity: 1, unit: "saco", minimum: 10, siteID: 9999},
		{name: "Cimento", quantity: 1, unit: "saco", minimum: 10, siteID: 0},
	}

	for _, c := range cases {
		if err := CreateMaterialWeb(c.name, c.quantity, c.unit, c.minimum, c.siteID, userID); err == nil {
			t.Errorf("cadastro %+v deveria falhar", c)
		}
	}
	if err := CreateMaterialWeb("Cimento", 1, "saco", 10, finished, userID); !errors.Is(err, ErrSiteFinished) {
		t.Errorf("cadastro em obra concluída: erro = %v, esperado ErrSiteFinished", err)
	}

	var total int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM produtos`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Errorf("%d materiais gravados, esperado 0", total)
	}
}

func TestCreateMaterialWebPutsInitialStockInSite(t *testing.T) {
	userID := setupTestDB(t)
	siteA := createTestSite(t, "Obra A", SiteStatusInProgress)

	if err := CreateMaterialWeb("Tijolo", 0, "milheiro", 1, siteA, userID); err != nil {
		t.Fatalf("cadastro: %v", err)
	}
	var id int
	if err := database.DB.QueryRow(`SELECT id FROM produtos WHERE nome = 'Tijolo'`).Scan(&id); err != nil {
		t.Fatal(err)
	}

	// Mesmo com zero, o material nasce com saldo na obra: é o que o faz
	// aparecer como zerado no controle dela.
	var rows int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM saldos WHERE produto_id = ? AND obra_id = ?`, id, siteA).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("linhas de saldo na obra A = %d, esperado 1", rows)
	}
	if got := balanceOf(t, id, centralID(t)); got != 0 {
		t.Errorf("central = %v, esperado 0", got)
	}
}

func TestLowStockUsesEachMaterialLimit(t *testing.T) {
	userID := setupTestDB(t)
	cement := createTestMaterial(t, userID, "Cimento", 50, 100) // abaixo do limite
	brick := createTestMaterial(t, userID, "Tijolo", 500, 200)  // folgado
	sand := createTestMaterial(t, userID, "Areia", 0, 2)        // zerado

	total, err := CountLowStockMaterials(0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("CountLowStockMaterials = %d, esperado 2", total)
	}

	low, err := GetLowStockMaterials(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(low) != 2 || low[0].Name != "Areia" || low[1].Name != "Cimento" {
		t.Errorf("lista de acabando = %+v, esperado [Areia, Cimento]", low)
	}
	if len(low) > 0 && (low[0].SiteName != "Almoxarifado central" || low[0].LastUser != "Teste") {
		t.Errorf("obra/responsável = %q/%q, esperado Almoxarifado central/Teste", low[0].SiteName, low[0].LastUser)
	}

	expected := map[int]string{cement: "low", brick: "normal", sand: "empty"}
	for id, status := range expected {
		material, err := GetMaterialByID(id)
		if err != nil {
			t.Fatal(err)
		}
		if material.StockStatus != status {
			t.Errorf("%s: situação = %q, esperado %q", material.Name, material.StockStatus, status)
		}
	}
}

// O alerta olha cada obra separadamente e ignora obra concluída.
func TestLowStockIsPerSite(t *testing.T) {
	userID := setupTestDB(t)
	central := centralID(t)
	siteA := createTestSite(t, "Obra A", SiteStatusInProgress)
	siteDone := createTestSite(t, "Obra entregue", SiteStatusInProgress)
	cement := createTestMaterial(t, userID, "Cimento", 500, 20)

	// Obra A com 5 sacos: acabando, mesmo com 500 no central.
	if err := AddStockWeb(cement, siteA, 5, userID, ""); err != nil {
		t.Fatal(err)
	}
	// A outra obra tinha 1 saco e foi concluída: não gera alerta.
	if err := AddStockWeb(cement, siteDone, 1, userID, ""); err != nil {
		t.Fatal(err)
	}
	// Direto no banco: hoje obra com saldo não pode ser encerrada, mas uma
	// obra encerrada antes dessa regra ainda pode ter sobra de material.
	if _, err := database.DB.Exec(`UPDATE obras SET situacao = ? WHERE id = ?`, SiteStatusFinished, siteDone); err != nil {
		t.Fatal(err)
	}

	count := func(siteID int) int {
		t.Helper()
		total, err := CountLowStockMaterials(siteID)
		if err != nil {
			t.Fatal(err)
		}
		return total
	}
	if got := count(0); got != 1 {
		t.Errorf("todas as obras = %d, esperado 1 (só a obra A)", got)
	}
	if got := count(siteA); got != 1 {
		t.Errorf("obra A = %d, esperado 1", got)
	}
	if got := count(central); got != 0 {
		t.Errorf("central = %d, esperado 0", got)
	}
	if got := count(siteDone); got != 0 {
		t.Errorf("obra concluída = %d, esperado 0", got)
	}

	low, err := GetLowStockMaterials(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(low) != 1 || low[0].SiteID != siteA || low[0].FormattedQuantity != "5" {
		t.Errorf("lista = %+v, esperado Cimento com 5 na obra A", low)
	}
}

func TestUpdateMaterialWebChangesUnitAndLimit(t *testing.T) {
	userID := setupTestDB(t)
	id := createTestMaterial(t, userID, "Areia", 3, 1)

	if err := UpdateMaterialWeb(id, "Areia média", "m³", 2.5, userID); err != nil {
		t.Fatalf("atualizar: %v", err)
	}

	material, err := GetMaterialByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if material.Name != "Areia média" || material.Unit != "m³" || material.MinimumStock != 2.5 {
		t.Errorf("material = %+v, esperado Areia média / m³ / 2.5", material)
	}
	// A quantidade não muda pela edição de cadastro.
	if material.Quantity != 3 {
		t.Errorf("quantidade = %v, esperado 3", material.Quantity)
	}
	if got := movementCount(t, "ATUALIZACAO"); got != 1 {
		t.Errorf("%d atualizações registradas, esperado 1", got)
	}

	// A atualização é do catálogo, não de uma obra.
	var withSite int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM movimentacoes WHERE tipo = 'ATUALIZACAO' AND obra_id IS NOT NULL`).Scan(&withSite); err != nil {
		t.Fatal(err)
	}
	if withSite != 0 {
		t.Errorf("%d atualizações com obra, esperado 0", withSite)
	}

	if err := UpdateMaterialWeb(id, "Areia", "caminhão", 1, userID); err == nil {
		t.Error("unidade fora da lista deveria falhar")
	}
}

func TestPaginatedMaterialsSearchesByNameOrID(t *testing.T) {
	userID := setupTestDB(t)
	cement := createTestMaterial(t, userID, "Cimento CP-II", 10, 5)
	createTestMaterial(t, userID, "Areia", 10, 5)

	byName, total, err := PaginatedMaterials("cimento", 1, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(byName) != 1 || byName[0].ID != cement {
		t.Errorf("busca por nome achou %+v (total %d)", byName, total)
	}

	byID, total, err := PaginatedMaterials(strconv.Itoa(cement), 1, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(byID) != 1 || byID[0].ID != cement {
		t.Errorf("busca por ID achou %+v (total %d)", byID, total)
	}
}

// A lista de materiais mostra o saldo da obra escolhida, ou a soma.
func TestPaginatedMaterialsQuantityFollowsSite(t *testing.T) {
	userID := setupTestDB(t)
	central := centralID(t)
	siteA := createTestSite(t, "Obra A", SiteStatusInProgress)
	siteB := createTestSite(t, "Obra B", SiteStatusInProgress)
	cement := createTestMaterial(t, userID, "Cimento", 100, 5)
	if err := AddStockWeb(cement, siteA, 12.5, userID, ""); err != nil {
		t.Fatal(err)
	}

	quantity := func(siteID int, order string) float64 {
		t.Helper()
		list, _, err := PaginatedSortedMaterials("", 1, 10, order, siteID)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 {
			t.Fatalf("lista com %d materiais, esperado 1", len(list))
		}
		return list[0].Quantity
	}

	cases := map[int]float64{0: 112.5, central: 100, siteA: 12.5, siteB: 0}
	for siteID, want := range cases {
		for _, order := range []string{"recentes", "nome", "estoque"} {
			if got := quantity(siteID, order); got != want {
				t.Errorf("obra %d, ordem %s: quantidade = %v, esperado %v", siteID, order, got, want)
			}
		}
	}
}

func TestDeleteMaterialWebBlockedWhileStocked(t *testing.T) {
	userID := setupTestDB(t)
	central := centralID(t)
	siteA := createTestSite(t, "Obra A", SiteStatusInProgress)
	cement := createTestMaterial(t, userID, "Cimento", 10, 5) // 10 no central
	if err := AddStockWeb(cement, siteA, 4, userID, ""); err != nil {
		t.Fatal(err)
	}

	isActive := func() bool {
		t.Helper()
		var active bool
		if err := database.DB.QueryRow(`SELECT ativo FROM produtos WHERE id = ?`, cement).Scan(&active); err != nil {
			t.Fatal(err)
		}
		return active
	}
	expectBlocked := func(want string) {
		t.Helper()
		err := DeleteMaterialWeb(cement)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("remover com saldo: erro = %v, esperado conter %q", err, want)
		}
		if !isActive() {
			t.Fatal("a remoção recusada desativou o material")
		}
	}

	expectBlocked("saldo em 2 obras")

	if err := RegisterStockExitWeb(cement, central, 10, userID, ""); err != nil {
		t.Fatal(err)
	}
	expectBlocked("saldo em 1 obra")

	// Saldo zerado em todas as obras (a linha continua em saldos, com 0).
	if err := RegisterStockExitWeb(cement, siteA, 4, userID, ""); err != nil {
		t.Fatal(err)
	}
	if err := DeleteMaterialWeb(cement); err != nil {
		t.Fatalf("remover com saldo zerado: %v", err)
	}
	if isActive() {
		t.Error("o material deveria ter sido removido")
	}
	if err := DeleteMaterialWeb(cement); err == nil || !strings.Contains(err.Error(), "não encontrado") {
		t.Errorf("remover de novo: erro = %v, esperado material não encontrado", err)
	}
}
