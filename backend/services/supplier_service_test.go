package services

import (
	"errors"
	"strings"
	"testing"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
)

// supplierIDByName devolve o ID do fornecedor com esse nome exato.
func supplierIDByName(t *testing.T, name string) int {
	t.Helper()
	var id int
	if err := database.DB.QueryRow(`SELECT id FROM fornecedores WHERE nome = ?`, name).Scan(&id); err != nil {
		t.Fatalf("ler ID de %s: %v", name, err)
	}
	return id
}

// assertSupplierInputError confere que err é um erro de digitação (e não
// do banco) e que a mensagem contém o trecho esperado.
func assertSupplierInputError(t *testing.T, err error, contains string) {
	t.Helper()
	var inputErr SupplierInputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("esperava SupplierInputError com %q, veio %v", contains, err)
	}
	if !strings.Contains(inputErr.Message, contains) {
		t.Errorf("mensagem = %q, esperado conter %q", inputErr.Message, contains)
	}
}

func TestNormalizeCNPJ(t *testing.T) {
	valid := []struct{ raw, want string }{
		{"", ""},
		{"11.222.333/0001-81", "11222333000181"},
		{" 11222333000181 ", "11222333000181"},
		// CNPJ alfanumérico (a partir de julho de 2026): exemplo da Receita,
		// e minúsculas viram maiúsculas.
		{"12.ABC.345/01DE-35", "12ABC34501DE35"},
		{"12.abc.345/01de-35", "12ABC34501DE35"},
	}
	for _, c := range valid {
		got, err := normalizeCNPJ(c.raw)
		if err != nil || got != c.want {
			t.Errorf("normalizeCNPJ(%q) = %q, %v; esperado %q", c.raw, got, err, c.want)
		}
	}

	invalid := []string{
		"11.222.333/0001-82", // dígito verificador errado
		"1122233300018",      // 13 caracteres
		"112223330001810",    // 15 caracteres
		"00000000000000",     // passa na conta, mas não é CNPJ
		"12.ABC.345/01DE-3A", // letra no dígito verificador
		"12.ABÇ.345/01DE-35", // letra fora de A-Z
	}
	for _, raw := range invalid {
		if _, err := normalizeCNPJ(raw); err == nil {
			t.Errorf("normalizeCNPJ(%q) deveria recusar", raw)
		}
	}

	if got := formatCNPJ("12ABC34501DE35"); got != "12.ABC.345/01DE-35" {
		t.Errorf("formatCNPJ = %q", got)
	}
}

func TestCreateSupplierWebNormalizesFields(t *testing.T) {
	setupTestDB(t)

	err := CreateSupplierWeb(models.Supplier{
		Name:    "  Casa do Construtor ",
		CNPJ:    "11.222.333/0001-81",
		Contact: " Joana ",
		Phone:   " (82) 99999-0000 ",
		Email:   " Vendas@CasaDoConstrutor.com.br ",
		City:    " Maceió ",
		Note:    " entrega em 2 dias ",
	})
	if err != nil {
		t.Fatalf("cadastro falhou: %v", err)
	}

	s, err := GetSupplierByID(supplierIDByName(t, "Casa do Construtor"))
	if err != nil {
		t.Fatalf("buscar fornecedor: %v", err)
	}
	if !s.Active {
		t.Error("fornecedor novo deveria nascer ativo")
	}
	if s.CNPJ != "11222333000181" || s.FormattedCNPJ != "11.222.333/0001-81" {
		t.Errorf("CNPJ = %q / %q", s.CNPJ, s.FormattedCNPJ)
	}
	if s.Email != "vendas@casadoconstrutor.com.br" {
		t.Errorf("email = %q, esperado em minúsculas e sem espaços", s.Email)
	}
	if s.Contact != "Joana" || s.Phone != "(82) 99999-0000" || s.City != "Maceió" || s.Note != "entrega em 2 dias" {
		t.Errorf("espaços não foram removidos: %+v", s)
	}
}

func TestCreateSupplierWebRejectsInvalidInput(t *testing.T) {
	setupTestDB(t)

	if err := CreateSupplierWeb(models.Supplier{Name: "Areal São Jorge", CNPJ: "11222333000181"}); err != nil {
		t.Fatalf("cadastro inicial falhou: %v", err)
	}

	cases := []struct {
		supplier models.Supplier
		want     string
	}{
		{models.Supplier{Name: "   "}, "informe o nome"},
		// Mesmo nome com outra caixa, inclusive na letra acentuada.
		{models.Supplier{Name: "AREAL SÃO JORGE"}, "já existe um fornecedor com esse nome"},
		{models.Supplier{Name: "Outro", CNPJ: "11.222.333/0001-81"}, "já existe um fornecedor com esse CNPJ"},
		{models.Supplier{Name: "Outro", CNPJ: "11.222.333/0001-80"}, "CNPJ inválido"},
		{models.Supplier{Name: strings.Repeat("a", maxSupplierNameLength+1)}, "no máximo"},
		{models.Supplier{Name: "Outro", Contact: strings.Repeat("ç", maxSupplierTextLength+1)}, "contato"},
		{models.Supplier{Name: "Outro", City: strings.Repeat("ã", maxSupplierTextLength+1)}, "cidade"},
		{models.Supplier{Name: "Outro", Note: strings.Repeat("é", maxSupplierNoteLength+1)}, "observação"},
		{models.Supplier{Name: "Outro", Phone: "82 9999-abcd"}, "telefone"},
		{models.Supplier{Name: "Outro", Phone: strings.Repeat("9", maxSupplierPhoneLength+1)}, "telefone"},
		{models.Supplier{Name: "Outro", Email: "vendas.fornecedor.com"}, "email"},
		{models.Supplier{Name: "Outro", Email: "vendas@fornecedor"}, "email"},
		{models.Supplier{Name: "Outro", Email: "a@b@c.com"}, "email"},
	}
	for _, c := range cases {
		assertSupplierInputError(t, CreateSupplierWeb(c.supplier), c.want)
	}

	// No limite exato ainda passa: o tamanho conta letras, não bytes.
	if err := CreateSupplierWeb(models.Supplier{Name: "Outro", City: strings.Repeat("ç", maxSupplierTextLength)}); err != nil {
		t.Errorf("cidade com %d letras deveria ser aceita: %v", maxSupplierTextLength, err)
	}
}

func TestUpdateSupplierWeb(t *testing.T) {
	setupTestDB(t)

	for _, name := range []string{"Fornecedor A", "Fornecedor B"} {
		if err := CreateSupplierWeb(models.Supplier{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	idA := supplierIDByName(t, "Fornecedor A")

	// Manter o próprio nome não é "repetido".
	if err := UpdateSupplierWeb(models.Supplier{ID: idA, Name: "Fornecedor A", City: "Arapiraca"}); err != nil {
		t.Fatalf("editar mantendo o nome falhou: %v", err)
	}
	if s, _ := GetSupplierByID(idA); s.City != "Arapiraca" {
		t.Errorf("cidade = %q, esperado Arapiraca", s.City)
	}

	assertSupplierInputError(t, UpdateSupplierWeb(models.Supplier{ID: idA, Name: "fornecedor b"}), "já existe")
	assertSupplierInputError(t, UpdateSupplierWeb(models.Supplier{ID: 999999, Name: "Qualquer"}), "não encontrado")
}

func TestSetSupplierActive(t *testing.T) {
	setupTestDB(t)

	if err := CreateSupplierWeb(models.Supplier{Name: "Cerâmica Norte", CNPJ: "11222333000181"}); err != nil {
		t.Fatal(err)
	}
	id := supplierIDByName(t, "Cerâmica Norte")

	assertSupplierInputError(t, SetSupplierActive(id, true), "já está ativo")

	if err := SetSupplierActive(id, false); err != nil {
		t.Fatalf("desativar falhou: %v", err)
	}
	if s, _ := GetSupplierByID(id); s.Active {
		t.Error("fornecedor deveria estar desativado")
	}
	assertSupplierInputError(t, SetSupplierActive(id, false), "já está desativado")

	// Desativado continua ocupando o nome e o CNPJ: o certo é reativar.
	assertSupplierInputError(t, CreateSupplierWeb(models.Supplier{Name: "CERÂMICA NORTE"}), "reative")
	assertSupplierInputError(t, CreateSupplierWeb(models.Supplier{Name: "Outra", CNPJ: "11222333000181"}), "reative")

	if err := SetSupplierActive(id, true); err != nil {
		t.Fatalf("reativar falhou: %v", err)
	}
	if s, _ := GetSupplierByID(id); !s.Active {
		t.Error("fornecedor deveria estar ativo de novo")
	}

	assertSupplierInputError(t, SetSupplierActive(999999, false), "não encontrado")
}

func TestPaginatedSuppliers(t *testing.T) {
	setupTestDB(t)

	suppliers := []models.Supplier{
		{Name: "Brita Forte", City: "Maceió", CNPJ: "11222333000181"},
		{Name: "areia fina", Contact: "Carlos"},
		{Name: "Cimento Sul", Email: "compras@cimentosul.com"},
	}
	for _, s := range suppliers {
		if err := CreateSupplierWeb(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetSupplierActive(supplierIDByName(t, "areia fina"), false); err != nil {
		t.Fatal(err)
	}

	names := func(list []models.Supplier) string {
		var out []string
		for _, s := range list {
			out = append(out, s.Name)
		}
		return strings.Join(out, ", ")
	}

	cases := []struct {
		search, status string
		want           string
	}{
		// Ativos primeiro, por nome sem diferenciar maiúsculas; depois os
		// desativados.
		{"", "", "Brita Forte, Cimento Sul, areia fina"},
		{"", SupplierFilterActive, "Brita Forte, Cimento Sul"},
		{"", SupplierFilterInactive, "areia fina"},
		{"maceió", "", "Brita Forte"},
		{"carlos", "", "areia fina"},
		{"cimentosul", "", "Cimento Sul"},
		// CNPJ casa com ou sem pontuação.
		{"11.222.333", "", "Brita Forte"},
		{"22233300", "", "Brita Forte"},
		{"nada disso", "", ""},
	}
	for _, c := range cases {
		list, total, err := PaginatedSuppliers(c.search, c.status, 1, 10)
		if err != nil {
			t.Fatal(err)
		}
		if got := names(list); got != c.want {
			t.Errorf("busca %q situação %q = [%s], esperado [%s]", c.search, c.status, got, c.want)
		}
		if total != len(list) {
			t.Errorf("busca %q: total %d, lista com %d", c.search, total, len(list))
		}
	}

	// Paginação: 2 por página, a segunda traz só o último.
	list, total, err := PaginatedSuppliers("", "", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || names(list) != "areia fina" {
		t.Errorf("página 2 = [%s] (total %d), esperado [areia fina] (total 3)", names(list), total)
	}
}
