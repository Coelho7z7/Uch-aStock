package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
	"uchoastock/backend/services"
)

// suppliersSnapshot copia a tabela de fornecedores como texto: se ele não
// muda, nada foi gravado.
func suppliersSnapshot(t *testing.T) string {
	t.Helper()
	rows, err := database.DB.Query(`SELECT id, nome, cnpj, contato, telefone, email, cidade, observacao, ativo FROM fornecedores ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out strings.Builder
	for rows.Next() {
		var id, active int
		var name, cnpj, contact, phone, email, city, note string
		if err := rows.Scan(&id, &name, &cnpj, &contact, &phone, &email, &city, &note, &active); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(&out, id, name, cnpj, contact, phone, email, city, note, active)
	}
	return out.String()
}

// TestOnlyAdminManagesSuppliers manda, como gestor e como almoxarife, cada
// ação de fornecedor direto no POST. Nenhuma pode gravar. O admin consegue
// todas, e os outros cargos continuam vendo a lista (sem os botões).
func TestOnlyAdminManagesSuppliers(t *testing.T) {
	f := setupIsolation(t)

	if err := services.CreateSupplierWeb(models.Supplier{Name: "Areal Existente"}); err != nil {
		t.Fatal(err)
	}
	existing := queryID(t, `SELECT id FROM fornecedores WHERE nome = 'Areal Existente'`)
	id := fmt.Sprint(existing)

	forms := map[string]url.Values{
		"cadastrar": {"acao": {"cadastrar"}, "nome": {"Fornecedor Intruso"}},
		"atualizar": {"acao": {"atualizar"}, "fornecedor_id": {id}, "nome": {"Nome Trocado"}},
		"desativar": {"acao": {"desativar"}, "fornecedor_id": {id}},
	}

	for _, who := range []struct{ role, token string }{
		{"gestor", f.managerToken},
		{"almoxarife", f.storekeeperToken},
	} {
		for action, form := range forms {
			before := suppliersSnapshot(t)
			response := post(who.token, supplierHandler, "/fornecedores", form)
			if response.Code != http.StatusForbidden {
				t.Errorf("%s ao %s: status %d, esperado 403", who.role, action, response.Code)
			}
			if after := suppliersSnapshot(t); after != before {
				t.Errorf("%s ao %s: a tabela de fornecedores mudou", who.role, action)
			}
		}

		page := get(who.token, supplierHandler, "/fornecedores")
		if !strings.Contains(page, "Areal Existente") {
			t.Errorf("%s deveria ver a lista de fornecedores", who.role)
		}
		for _, button := range []string{`id="open-create-supplier"`, `id="supplier-modal"`, `value="desativar"`, `href="/fornecedores?editar=`} {
			if strings.Contains(page, button) {
				t.Errorf("%s não deveria ver %q", who.role, button)
			}
		}
	}

	// O admin cadastra, edita e desativa.
	for _, action := range []string{"cadastrar", "atualizar", "desativar"} {
		response := post(f.adminToken, supplierHandler, "/fornecedores", forms[action])
		if response.Code != http.StatusSeeOther {
			t.Errorf("admin ao %s: status %d, esperado 303", action, response.Code)
		}
	}
	supplier, err := services.GetSupplierByID(existing)
	if err != nil {
		t.Fatal(err)
	}
	if supplier.Name != "Nome Trocado" || supplier.Active {
		t.Errorf("depois do admin: nome %q, ativo %v; esperado Nome Trocado, desativado", supplier.Name, supplier.Active)
	}
	queryID(t, `SELECT id FROM fornecedores WHERE nome = 'Fornecedor Intruso'`)

	// Erro de digitação volta 400 com a mensagem na tela, sem gravar.
	before := suppliersSnapshot(t)
	response := post(f.adminToken, supplierHandler, "/fornecedores", url.Values{"acao": {"cadastrar"}, "nome": {"Outro"}, "cnpj": {"11.222.333/0001-80"}})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "CNPJ inválido") {
		t.Errorf("CNPJ inválido: status %d, esperado 400 com a mensagem", response.Code)
	}
	if suppliersSnapshot(t) != before {
		t.Error("CNPJ inválido não deveria gravar nada")
	}
}
