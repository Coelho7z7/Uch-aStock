package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	database "uchoastock/backend/database"
	"uchoastock/backend/services"
)

// isolationFixture é um banco com duas obras (A e B), um gestor da obra A
// e dados na obra B que ele não pode alterar.
type isolationFixture struct {
	siteA, siteB       int
	materialA          int // estoque só na obra A
	materialB          int // estoque na obra B
	storekeeperA       int // almoxarife da obra A
	storekeeperB       int // almoxarife da obra B
	managerToken       string
	managerSessionHash string
}

// setupIsolation monta o banco de teste e a sessão do gestor da obra A.
// Os handlers leem os templates em frontend/html, por isso o teste roda
// na raiz do projeto (e volta para a pasta original no fim).
func setupIsolation(t *testing.T) isolationFixture {
	t.Helper()

	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareProjectDirectory(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(original) })

	t.Setenv("DB_PATH", filepath.Join(t.TempDir(), "teste.db"))
	if err := database.Connect(); err != nil {
		t.Fatalf("conectar ao banco de teste: %v", err)
	}
	t.Cleanup(func() { database.DB.Close() })
	if err := database.CreateTables(); err != nil {
		t.Fatalf("criar tabelas: %v", err)
	}

	var f isolationFixture
	for _, name := range []string{"Obra A", "Obra B"} {
		if err := services.CreateSiteWeb(name, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	f.siteA = queryID(t, `SELECT id FROM obras WHERE nome = 'Obra A'`)
	f.siteB = queryID(t, `SELECT id FROM obras WHERE nome = 'Obra B'`)

	users := []struct {
		name, email, role string
		site              int
	}{
		{"Admin", "admin.teste@empresa.com", "admin", 0},
		{"Gestor A", "gestor.a@empresa.com", "gestor", f.siteA},
		{"Almoxarife A", "almox.a@empresa.com", "almoxarife", f.siteA},
		{"Almoxarife B", "almox.b@empresa.com", "almoxarife", f.siteB},
	}
	for _, u := range users {
		if err := services.CreateUserWeb(u.name, u.email, "senha!123", u.role, u.site); err != nil {
			t.Fatalf("criar %s: %v", u.email, err)
		}
	}
	adminID := queryID(t, `SELECT id FROM usuarios WHERE email = 'admin.teste@empresa.com'`)
	managerID := queryID(t, `SELECT id FROM usuarios WHERE email = 'gestor.a@empresa.com'`)
	f.storekeeperA = queryID(t, `SELECT id FROM usuarios WHERE email = 'almox.a@empresa.com'`)
	f.storekeeperB = queryID(t, `SELECT id FROM usuarios WHERE email = 'almox.b@empresa.com'`)

	if err := services.CreateMaterialWeb("Areia A", 3, "m³", 1, f.siteA, adminID); err != nil {
		t.Fatal(err)
	}
	if err := services.CreateMaterialWeb("Cimento B", 10, "saco", 5, f.siteB, adminID); err != nil {
		t.Fatal(err)
	}
	f.materialA = queryID(t, `SELECT id FROM produtos WHERE nome = 'Areia A'`)
	f.materialB = queryID(t, `SELECT id FROM produtos WHERE nome = 'Cimento B'`)

	if f.managerToken, err = createSession(managerID); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(f.managerToken))
	f.managerSessionHash = hex.EncodeToString(hash[:])
	return f
}

func queryID(t *testing.T, query string) int {
	t.Helper()
	var id int
	if err := database.DB.QueryRow(query).Scan(&id); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return id
}

// snapshot copia, como texto, tudo o que um ataque poderia alterar:
// catálogo, saldos, movimentações, usuários (cargo, senha, ativo), vínculos
// e obras. Se o texto não muda, nada foi gravado.
func snapshot(t *testing.T) string {
	t.Helper()
	queries := []string{
		`SELECT id, nome, unidade, limite_minimo, ativo FROM produtos ORDER BY id`,
		`SELECT produto_id, obra_id, quantidade FROM saldos ORDER BY produto_id, obra_id`,
		`SELECT id, produto_id, usuario_id, COALESCE(obra_id, 0), tipo, quantidade FROM movimentacoes ORDER BY id`,
		`SELECT id, email, role, senha, ativo FROM usuarios ORDER BY id`,
		`SELECT usuario_id, obra_id FROM usuario_obras ORDER BY usuario_id`,
		`SELECT id, nome, cidade, responsavel, situacao FROM obras ORDER BY id`,
	}
	var out strings.Builder
	for _, query := range queries {
		rows, err := database.DB.Query(query)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(&out, values...)
		}
		rows.Close()
	}
	return out.String()
}

// post envia um formulário como o gestor da obra A, passando pelo mesmo
// middleware das rotas de verdade.
func (f isolationFixture) post(handler authenticatedHandler, path string, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "sessao", Value: f.managerToken})
	recorder := httptest.NewRecorder()
	withUser(handler).ServeHTTP(recorder, request)
	return recorder
}

func (f isolationFixture) selectSite(t *testing.T, siteID int) {
	t.Helper()
	if err := services.SetSessionSite(f.managerSessionHash, siteID); err != nil {
		t.Fatal(err)
	}
}

// TestManagerCannotTouchAnotherSite tenta, como gestor da obra A, alterar
// estoque, material, usuário e dados da obra B mandando o ID direto no
// POST — inclusive trocando a obra da sessão para B, o que o seletor
// permite (para consulta). Nenhuma tentativa pode gravar nada.
func TestManagerCannotTouchAnotherSite(t *testing.T) {
	f := setupIsolation(t)
	id := func(n int) string { return fmt.Sprint(n) }

	attacks := []struct {
		name      string
		sessionAt int
		handler   authenticatedHandler
		path      string
		form      url.Values
	}{
		// Estoque.
		{"entrada na obra B com a sessão na obra A", f.siteA, stockHandler, "/estoque",
			url.Values{"acao": {"entrada"}, "material_id": {id(f.materialB)}, "quantidade": {"5"}, "obra_id": {id(f.siteB)}}},
		{"entrada na obra B com a sessão na obra B", f.siteB, stockHandler, "/estoque",
			url.Values{"acao": {"entrada"}, "material_id": {id(f.materialB)}, "quantidade": {"5"}, "obra_id": {id(f.siteB)}}},
		{"saída na obra B com a sessão na obra B", f.siteB, stockHandler, "/estoque",
			url.Values{"acao": {"saida"}, "material_id": {id(f.materialB)}, "quantidade": {"5"}, "obra_id": {id(f.siteB)}}},

		// Material.
		{"cadastrar material com estoque inicial na obra B", f.siteB, materialHandler, "/materiais",
			url.Values{"nome": {"Invasor"}, "quantidade": {"5"}, "unidade": {"un"}, "limite_minimo": {"1"}, "obra_id": {id(f.siteB)}}},
		{"editar material com estoque na obra B", f.siteA, editMaterialHandler, "/alterar-material",
			url.Values{"acao": {"atualizar"}, "material_id": {id(f.materialB)}, "nome": {"Alterado"}, "unidade": {"kg"}, "limite_minimo": {"0"}}},
		{"remover material com estoque na obra B", f.siteA, editMaterialHandler, "/alterar-material",
			url.Values{"acao": {"remover"}, "material_id": {id(f.materialB)}}},

		// Usuários.
		{"mudar o cargo do almoxarife da obra B", f.siteA, userHandler, "/usuarios",
			url.Values{"acao": {"alterar_permissao"}, "usuario_id": {id(f.storekeeperB)}, "role": {"solicitante"}, "obra_id": {id(f.siteB)}}},
		{"trazer o almoxarife da obra B para a obra A", f.siteA, userHandler, "/usuarios",
			url.Values{"acao": {"alterar_permissao"}, "usuario_id": {id(f.storekeeperB)}, "role": {"almoxarife"}, "obra_id": {id(f.siteA)}}},
		{"trocar a senha do almoxarife da obra B", f.siteA, userHandler, "/usuarios",
			url.Values{"acao": {"redefinir_senha"}, "usuario_id": {id(f.storekeeperB)}, "senha": {"invasao!1"}}},
		{"remover o almoxarife da obra B", f.siteA, userHandler, "/usuarios",
			url.Values{"acao": {"remover"}, "usuario_id": {id(f.storekeeperB)}}},
		{"criar usuário na obra B", f.siteA, userHandler, "/usuarios",
			url.Values{"acao": {"criar"}, "nome": {"Invasor"}, "email": {"invasor@empresa.com"}, "senha": {"senha!123"}, "role": {"almoxarife"}, "obra_id": {id(f.siteB)}}},
		{"mandar o almoxarife da obra A para a obra B", f.siteA, userHandler, "/usuarios",
			url.Values{"acao": {"alterar_permissao"}, "usuario_id": {id(f.storekeeperA)}, "role": {"almoxarife"}, "obra_id": {id(f.siteB)}}},

		// Dados e situação da obra.
		{"editar os dados da obra B", f.siteB, siteHandler, "/obras",
			url.Values{"acao": {"atualizar"}, "obra_id": {id(f.siteB)}, "nome": {"Obra B"}, "cidade": {"Invadida"}, "situacao": {"ANDAMENTO"}}},
		{"paralisar a obra B", f.siteB, siteHandler, "/obras",
			url.Values{"acao": {"situacao"}, "obra_id": {id(f.siteB)}, "situacao": {"PARALISADA"}}},
		{"encerrar a obra B", f.siteA, siteHandler, "/obras",
			url.Values{"acao": {"situacao"}, "obra_id": {id(f.siteB)}, "situacao": {"CONCLUIDA"}}},
	}

	for _, a := range attacks {
		f.selectSite(t, a.sessionAt)
		before := snapshot(t)
		response := f.post(a.handler, a.path, a.form)
		if response.Code == http.StatusSeeOther {
			t.Errorf("%s: o servidor aceitou (303 para %s)", a.name, response.Header().Get("Location"))
		}
		if after := snapshot(t); after != before {
			t.Errorf("%s: o banco mudou (status %d)", a.name, response.Code)
		}
	}

	// Controle: as mesmas ações na obra A funcionam. Sem isto, o teste
	// passaria até com o gestor bloqueado em tudo por um erro de montagem.
	f.selectSite(t, f.siteA)
	controls := []struct {
		name    string
		handler authenticatedHandler
		path    string
		form    url.Values
	}{
		{"entrada na obra A", stockHandler, "/estoque",
			url.Values{"acao": {"entrada"}, "material_id": {id(f.materialA)}, "quantidade": {"2"}, "obra_id": {id(f.siteA)}}},
		{"editar material só da obra A", editMaterialHandler, "/alterar-material",
			url.Values{"acao": {"atualizar"}, "material_id": {id(f.materialA)}, "nome": {"Areia fina A"}, "unidade": {"m³"}, "limite_minimo": {"1"}}},
		{"mudar o cargo do almoxarife da obra A", userHandler, "/usuarios",
			url.Values{"acao": {"alterar_permissao"}, "usuario_id": {id(f.storekeeperA)}, "role": {"solicitante"}, "obra_id": {id(f.siteA)}}},
		{"paralisar a obra A", siteHandler, "/obras",
			url.Values{"acao": {"situacao"}, "obra_id": {id(f.siteA)}, "situacao": {"PARALISADA"}}},
	}
	for _, c := range controls {
		if response := f.post(c.handler, c.path, c.form); response.Code != http.StatusSeeOther {
			t.Errorf("controle %s: status %d, esperado 303", c.name, response.Code)
		}
	}
}
