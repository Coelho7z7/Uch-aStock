package services

import (
	"strconv"
	"testing"

	database "uchoastock/backend/database"
)

func TestCreateMaterialWebValidatesInput(t *testing.T) {
	userID := setupTestDB(t)

	cases := []struct {
		name     string
		quantity float64
		unit     string
		minimum  float64
	}{
		{name: "", quantity: 1, unit: "saco", minimum: 10},
		{name: "   ", quantity: 1, unit: "saco", minimum: 10},
		{name: "Cimento", quantity: -1, unit: "saco", minimum: 10},
		{name: "Cimento", quantity: 1, unit: "sacos", minimum: 10},
		{name: "Cimento", quantity: 1, unit: "saco", minimum: -2},
	}

	for _, c := range cases {
		if err := CreateMaterialWeb(c.name, c.quantity, c.unit, c.minimum, userID); err == nil {
			t.Errorf("cadastro %+v deveria falhar", c)
		}
	}

	var total int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM produtos`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Errorf("%d materiais gravados, esperado 0", total)
	}
}

func TestLowStockUsesEachMaterialLimit(t *testing.T) {
	userID := setupTestDB(t)
	cement := createTestMaterial(t, userID, "Cimento", 50, 100) // abaixo do limite
	brick := createTestMaterial(t, userID, "Tijolo", 500, 200)  // folgado
	sand := createTestMaterial(t, userID, "Areia", 0, 2)        // zerado

	total, err := CountLowStockMaterials()
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("CountLowStockMaterials = %d, esperado 2", total)
	}

	low, err := GetLowStockMaterials(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(low) != 2 || low[0].Name != "Areia" || low[1].Name != "Cimento" {
		t.Errorf("lista de acabando = %+v, esperado [Areia, Cimento]", low)
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

	if err := UpdateMaterialWeb(id, "Areia", "caminhão", 1, userID); err == nil {
		t.Error("unidade fora da lista deveria falhar")
	}
}

func TestPaginatedMaterialsSearchesByNameOrID(t *testing.T) {
	userID := setupTestDB(t)
	cement := createTestMaterial(t, userID, "Cimento CP-II", 10, 5)
	createTestMaterial(t, userID, "Areia", 10, 5)

	byName, total, err := PaginatedMaterials("cimento", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(byName) != 1 || byName[0].ID != cement {
		t.Errorf("busca por nome achou %+v (total %d)", byName, total)
	}

	byID, total, err := PaginatedMaterials(strconv.Itoa(cement), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(byID) != 1 || byID[0].ID != cement {
		t.Errorf("busca por ID achou %+v (total %d)", byID, total)
	}
}
