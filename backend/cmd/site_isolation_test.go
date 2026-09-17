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

// isolationFixture é um banco com duas obras (A e B), um gestor e um
// almoxarife da obra A, um almoxarife da obra B e um administrador.
type isolationFixture struct {
	siteA, siteB int
	materialA    int // estoque só na obra A
	materialB    int // estoque na obra B
	storekeeperA int // ID do almoxarife da obra A
	storekeeperB int // ID do almoxarife da obra B

	// Tokens de sessão de cada pessoa que envia os POSTs.
	adminToken       string
	managerToken     string
	storekeeperToken string
}

// setupIsolation monta o banco de teste e as sessões. Os handlers leem os
// templates em frontend/html, por isso o teste roda na raiz do projeto (e
// volta para a pasta original no fim).
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

	for _, s := range []struct {
		userID int
		token  *string
	}{
		{adminID, &f.adminToken},
		{managerID, &f.managerToken},
		{f.storekeeperA, &f.storekeeperToken},
	} {
		if *s.token, err = createSession(s.userID); err != nil {
			t.Fatal(err)
		}
	}
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
// catálogo, saldos, movimentações, usuários (cargo, senha, ativo), vínculos,
// obras e requisições (com itens e histórico). Se o texto não muda, nada foi gravado.
func snapshot(t *testing.T) string {
	t.Helper()
	queries := []string{
		`SELECT id, nome, unidade, limite_minimo, ativo FROM produtos ORDER BY id`,
		`SELECT produto_id, obra_id, quantidade FROM saldos ORDER BY produto_id, obra_id`,
		`SELECT id, produto_id, usuario_id, COALESCE(obra_id, 0), tipo, quantidade, COALESCE(requisicao_id, 0) FROM movimentacoes ORDER BY id`,
		`SELECT id, obra_id, solicitante_id, status, observacao, COALESCE(aprovado_por, 0), COALESCE(aprovado_em, ''), motivo_rejeicao, atualizado_em FROM requisicoes ORDER BY id`,
		`SELECT id, requisicao_id, produto_id, quantidade_solicitada, quantidade_atendida FROM requisicao_itens ORDER BY id`,
		`SELECT id, requisicao_id, usuario_id, acao, detalhe FROM requisicao_eventos ORDER BY id`,
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

// post envia um formulário com a sessão do token, passando pelo mesmo
// middleware das rotas de verdade.
func post(token string, handler authenticatedHandler, path string, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "sessao", Value: token})
	recorder := httptest.NewRecorder()
	withUser(handler).ServeHTTP(recorder, request)
	return recorder
}

// selectSite troca a obra da sessão do token, como o seletor do topo faz
// (ele deixa qualquer um escolher qualquer obra, para consulta).
func selectSite(t *testing.T, token string, siteID int) {
	t.Helper()
	hash := sha256.Sum256([]byte(token))
	if err := services.SetSessionSite(hex.EncodeToString(hash[:]), siteID); err != nil {
		t.Fatal(err)
	}
}

type attack struct {
	name      string
	token     string
	sessionAt int
	handler   authenticatedHandler
	path      string
	form      url.Values
}

// runAttacks envia cada POST e confere que o servidor não aceitou e que o
// banco ficou exatamente igual.
func runAttacks(t *testing.T, attacks []attack) {
	t.Helper()
	for _, a := range attacks {
		selectSite(t, a.token, a.sessionAt)
		before := snapshot(t)
		response := post(a.token, a.handler, a.path, a.form)
		if response.Code == http.StatusSeeOther {
			t.Errorf("%s: o servidor aceitou (303 para %s)", a.name, response.Header().Get("Location"))
		}
		if after := snapshot(t); after != before {
			t.Errorf("%s: o banco mudou (status %d)", a.name, response.Code)
		}
	}
}

// TestManagerCannotTouchAnotherSite tenta, como gestor da obra A, alterar
// estoque, usuário e dados da obra B mandando o ID direto no POST —
// inclusive trocando a obra da sessão para B. Nenhuma tentativa pode
// gravar nada.
func TestManagerCannotTouchAnotherSite(t *testing.T) {
	f := setupIsolation(t)
	id := func(n int) string { return fmt.Sprint(n) }
	g := f.managerToken

	runAttacks(t, []attack{
		// Estoque.
		{"entrada na obra B com a sessão na obra A", g, f.siteA, stockHandler, "/estoque",
			url.Values{"acao": {"entrada"}, "material_id": {id(f.materialB)}, "quantidade": {"5"}, "obra_id": {id(f.siteB)}}},
		{"entrada na obra B com a sessão na obra B", g, f.siteB, stockHandler, "/estoque",
			url.Values{"acao": {"entrada"}, "material_id": {id(f.materialB)}, "quantidade": {"5"}, "obra_id": {id(f.siteB)}}},
		{"saída na obra B com a sessão na obra B", g, f.siteB, stockHandler, "/estoque",
			url.Values{"acao": {"saida"}, "material_id": {id(f.materialB)}, "quantidade": {"5"}, "obra_id": {id(f.siteB)}}},

		// Usuários.
		{"mudar o cargo do almoxarife da obra B", g, f.siteA, userHandler, "/usuarios",
			url.Values{"acao": {"alterar_permissao"}, "usuario_id": {id(f.storekeeperB)}, "role": {"solicitante"}, "obra_id": {id(f.siteB)}}},
		{"trazer o almoxarife da obra B para a obra A", g, f.siteA, userHandler, "/usuarios",
			url.Values{"acao": {"alterar_permissao"}, "usuario_id": {id(f.storekeeperB)}, "role": {"almoxarife"}, "obra_id": {id(f.siteA)}}},
		{"trocar a senha do almoxarife da obra B", g, f.siteA, userHandler, "/usuarios",
			url.Values{"acao": {"redefinir_senha"}, "usuario_id": {id(f.storekeeperB)}, "senha": {"invasao!1"}}},
		{"remover o almoxarife da obra B", g, f.siteA, userHandler, "/usuarios",
			url.Values{"acao": {"remover"}, "usuario_id": {id(f.storekeeperB)}}},
		{"criar usuário na obra B", g, f.siteA, userHandler, "/usuarios",
			url.Values{"acao": {"criar"}, "nome": {"Invasor"}, "email": {"invasor@empresa.com"}, "senha": {"senha!123"}, "role": {"almoxarife"}, "obra_id": {id(f.siteB)}}},
		{"mandar o almoxarife da obra A para a obra B", g, f.siteA, userHandler, "/usuarios",
			url.Values{"acao": {"alterar_permissao"}, "usuario_id": {id(f.storekeeperA)}, "role": {"almoxarife"}, "obra_id": {id(f.siteB)}}},

		// Dados e situação da obra.
		{"editar os dados da obra B", g, f.siteB, siteHandler, "/obras",
			url.Values{"acao": {"atualizar"}, "obra_id": {id(f.siteB)}, "nome": {"Obra B"}, "cidade": {"Invadida"}, "situacao": {"ANDAMENTO"}}},
		{"paralisar a obra B", g, f.siteB, siteHandler, "/obras",
			url.Values{"acao": {"situacao"}, "obra_id": {id(f.siteB)}, "situacao": {"PARALISADA"}}},
		{"encerrar a obra B", g, f.siteA, siteHandler, "/obras",
			url.Values{"acao": {"situacao"}, "obra_id": {id(f.siteB)}, "situacao": {"CONCLUIDA"}}},
	})

	// Controle: as mesmas ações na obra A funcionam. Sem isto, o teste
	// passaria até com o gestor bloqueado em tudo por um erro de montagem.
	selectSite(t, g, f.siteA)
	controls := []struct {
		name    string
		handler authenticatedHandler
		path    string
		form    url.Values
	}{
		{"entrada na obra A", stockHandler, "/estoque",
			url.Values{"acao": {"entrada"}, "material_id": {id(f.materialA)}, "quantidade": {"2"}, "obra_id": {id(f.siteA)}}},
		{"mudar o cargo do almoxarife da obra A", userHandler, "/usuarios",
			url.Values{"acao": {"alterar_permissao"}, "usuario_id": {id(f.storekeeperA)}, "role": {"solicitante"}, "obra_id": {id(f.siteA)}}},
		{"paralisar a obra A", siteHandler, "/obras",
			url.Values{"acao": {"situacao"}, "obra_id": {id(f.siteA)}, "situacao": {"PARALISADA"}}},
	}
	for _, c := range controls {
		if response := post(g, c.handler, c.path, c.form); response.Code != http.StatusSeeOther {
			t.Errorf("controle %s: status %d, esperado 303", c.name, response.Code)
		}
	}
}

// TestOnlyAdminChangesMaterialCatalog confere que o catálogo de materiais
// é exclusivo do administrador: gestor e almoxarife não cadastram, não
// editam e não removem material de obra nenhuma — nem da própria — mesmo
// mandando o POST direto. Movimentar estoque na própria obra continua
// liberado para os dois.
func TestOnlyAdminChangesMaterialCatalog(t *testing.T) {
	f := setupIsolation(t)
	id := func(n int) string { return fmt.Sprint(n) }

	var attacks []attack
	for _, who := range []struct{ role, token string }{
		{"gestor", f.managerToken},
		{"almoxarife", f.storekeeperToken},
	} {
		for _, m := range []struct {
			label string
			id    int
		}{
			{"da própria obra", f.materialA},
			{"da obra B", f.materialB},
		} {
			attacks = append(attacks,
				attack{who.role + ": editar material " + m.label, who.token, f.siteA, editMaterialHandler, "/alterar-material",
					url.Values{"acao": {"atualizar"}, "material_id": {id(m.id)}, "nome": {"Alterado"}, "unidade": {"kg"}, "limite_minimo": {"0"}}},
				attack{who.role + ": mudar só o limite mínimo do material " + m.label, who.token, f.siteA, editMaterialHandler, "/alterar-material",
					url.Values{"acao": {"atualizar"}, "material_id": {id(m.id)}, "nome": {"Areia A"}, "unidade": {"m³"}, "limite_minimo": {"50"}}},
				attack{who.role + ": remover material " + m.label, who.token, f.siteA, editMaterialHandler, "/alterar-material",
					url.Values{"acao": {"remover"}, "material_id": {id(m.id)}}},
			)
		}
		attacks = append(attacks,
			attack{who.role + ": cadastrar material sem estoque", who.token, f.siteA, materialHandler, "/materiais",
				url.Values{"nome": {"Novo"}, "quantidade": {"0"}, "unidade": {"un"}, "limite_minimo": {"1"}, "obra_id": {id(f.siteA)}}},
			attack{who.role + ": cadastrar material com estoque na própria obra", who.token, f.siteA, materialHandler, "/materiais",
				url.Values{"nome": {"Novo"}, "quantidade": {"5"}, "unidade": {"un"}, "limite_minimo": {"1"}, "obra_id": {id(f.siteA)}}},
		)
	}
	runAttacks(t, attacks)

	// Controle: os dois movimentam estoque na própria obra, e o
	// administrador edita, cadastra e remove material.
	for _, token := range []string{f.managerToken, f.storekeeperToken} {
		selectSite(t, token, f.siteA)
		response := post(token, stockHandler, "/estoque",
			url.Values{"acao": {"entrada"}, "material_id": {id(f.materialA)}, "quantidade": {"1"}, "obra_id": {id(f.siteA)}})
		if response.Code != http.StatusSeeOther {
			t.Errorf("controle entrada na própria obra: status %d, esperado 303", response.Code)
		}
	}

	selectSite(t, f.adminToken, f.siteB)
	adminControls := []struct {
		name    string
		handler authenticatedHandler
		path    string
		form    url.Values
	}{
		{"admin edita material da obra B", editMaterialHandler, "/alterar-material",
			url.Values{"acao": {"atualizar"}, "material_id": {id(f.materialB)}, "nome": {"Cimento CP-II"}, "unidade": {"saco"}, "limite_minimo": {"8"}}},
		{"admin cadastra material com estoque na obra B", materialHandler, "/materiais",
			url.Values{"nome": {"Brita"}, "quantidade": {"4"}, "unidade": {"m³"}, "limite_minimo": {"1"}, "obra_id": {id(f.siteB)}}},
		{"admin cadastra material sem estoque", materialHandler, "/materiais",
			url.Values{"nome": {"Prego"}, "quantidade": {"0"}, "unidade": {"kg"}, "limite_minimo": {"1"}, "obra_id": {id(f.siteB)}}},
	}
	for _, c := range adminControls {
		if response := post(f.adminToken, c.handler, c.path, c.form); response.Code != http.StatusSeeOther {
			t.Errorf("controle %s: status %d, esperado 303", c.name, response.Code)
		}
	}

	// Nem o admin remove material com saldo; sem saldo, remove.
	before := snapshot(t)
	response := post(f.adminToken, editMaterialHandler, "/alterar-material",
		url.Values{"acao": {"remover"}, "material_id": {id(f.materialB)}})
	if response.Code == http.StatusSeeOther || !strings.Contains(response.Body.String(), "saldo em 1 obra") {
		t.Errorf("admin removendo material com saldo: status %d, sem a mensagem esperada", response.Code)
	}
	if snapshot(t) != before {
		t.Error("a remoção recusada mudou o banco")
	}
	nail := queryID(t, `SELECT id FROM produtos WHERE nome = 'Prego'`)
	if response := post(f.adminToken, editMaterialHandler, "/alterar-material",
		url.Values{"acao": {"remover"}, "material_id": {id(nail)}}); response.Code != http.StatusSeeOther {
		t.Errorf("admin removendo material sem saldo: status %d, esperado 303", response.Code)
	}
}

// get abre a página com a sessão do token e devolve o HTML.
func get(token string, handler authenticatedHandler, path string) string {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.AddCookie(&http.Cookie{Name: "sessao", Value: token})
	recorder := httptest.NewRecorder()
	withUser(handler).ServeHTTP(recorder, request)
	return recorder.Body.String()
}

func siteStatusOf(t *testing.T, siteID int) string {
	t.Helper()
	var status string
	if err := database.DB.QueryRow(`SELECT situacao FROM obras WHERE id = ?`, siteID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

// TestSiteStatusRulesOverHTTP confere, pelas rotas, as regras de situação:
// só o admin reabre obra concluída (nem pelo botão nem pela edição o
// gestor consegue), obra com saldo não encerra, pedir a mesma situação é
// recusado, e a tela mostra Reabrir só para o admin e Retomar com
// confirmação.
func TestSiteStatusRulesOverHTTP(t *testing.T) {
	f := setupIsolation(t)
	id := func(n int) string { return fmt.Sprint(n) }
	g := f.managerToken

	// A obra A (do gestor) concluída, direto no banco.
	if _, err := database.DB.Exec(`UPDATE obras SET situacao = 'CONCLUIDA' WHERE id = ?`, f.siteA); err != nil {
		t.Fatal(err)
	}

	runAttacks(t, []attack{
		{"gestor reabre a própria obra pelo botão", g, f.siteA, siteHandler, "/obras",
			url.Values{"acao": {"situacao"}, "obra_id": {id(f.siteA)}, "situacao": {"ANDAMENTO"}}},
		{"gestor reabre a própria obra pela edição", g, f.siteA, siteHandler, "/obras",
			url.Values{"acao": {"atualizar"}, "obra_id": {id(f.siteA)}, "nome": {"Obra A"}, "situacao": {"ANDAMENTO"}}},
		{"gestor paralisa a obra concluída", g, f.siteA, siteHandler, "/obras",
			url.Values{"acao": {"situacao"}, "obra_id": {id(f.siteA)}, "situacao": {"PARALISADA"}}},
	})
	if response := post(g, siteHandler, "/obras", url.Values{"acao": {"situacao"}, "obra_id": {id(f.siteA)}, "situacao": {"ANDAMENTO"}}); response.Code != http.StatusForbidden {
		t.Errorf("gestor reabrindo: status %d, esperado 403", response.Code)
	}

	// No modal de edição da obra concluída, "Em andamento" fica escondida
	// para o gestor e aparece para o admin.
	reopenOption := func(token string) string {
		page := get(token, siteHandler, "/obras?editar="+id(f.siteA))
		// Procura dentro do seletor do modal: o filtro do topo da página
		// também tem uma opção ANDAMENTO.
		modal := strings.Index(page, `id="site-status"`)
		if modal < 0 {
			t.Fatal("seletor de situação do modal não encontrado")
		}
		page = page[modal:]
		start := strings.Index(page, `<option value="ANDAMENTO"`)
		if start < 0 {
			t.Fatal("opção Em andamento não encontrada no modal")
		}
		return page[start : start+strings.Index(page[start:], ">")+1]
	}
	if option := reopenOption(g); !strings.Contains(option, "hidden disabled") {
		t.Errorf("gestor: opção Em andamento deveria estar escondida: %s", option)
	}
	if option := reopenOption(f.adminToken); strings.Contains(option, "hidden") {
		t.Errorf("admin: opção Em andamento deveria aparecer: %s", option)
	}

	if strings.Contains(get(g, siteHandler, "/obras"), `aria-label="Reabrir Obra A"`) {
		t.Error("o gestor não deveria ver o botão Reabrir")
	}
	if !strings.Contains(get(f.adminToken, siteHandler, "/obras"), `aria-label="Reabrir Obra A"`) {
		t.Error("o admin deveria ver o botão Reabrir")
	}

	// O admin reabre.
	if response := post(f.adminToken, siteHandler, "/obras", url.Values{"acao": {"situacao"}, "obra_id": {id(f.siteA)}, "situacao": {"ANDAMENTO"}}); response.Code != http.StatusSeeOther {
		t.Fatalf("admin reabrindo: status %d, esperado 303", response.Code)
	}
	if got := siteStatusOf(t, f.siteA); got != "ANDAMENTO" {
		t.Fatalf("depois de reabrir, situação = %s", got)
	}

	// O gestor paralisa, e aí aparece Retomar com confirmação.
	if response := post(g, siteHandler, "/obras", url.Values{"acao": {"situacao"}, "obra_id": {id(f.siteA)}, "situacao": {"PARALISADA"}}); response.Code != http.StatusSeeOther {
		t.Fatalf("gestor paralisando: status %d, esperado 303", response.Code)
	}
	page := get(g, siteHandler, "/obras")
	retomar := page[strings.Index(page, `aria-label="Retomar Obra A"`):]
	retomar = retomar[:strings.Index(retomar, ">")]
	if !strings.Contains(retomar, `data-confirm="Retomar a obra`) || !strings.Contains(retomar, `data-confirm-title="Retomar obra"`) {
		t.Errorf("botão Retomar sem confirmação: %s", retomar)
	}

	// Mesma situação: recusado.
	selectSite(t, g, f.siteA)
	before := snapshot(t)
	response := post(g, siteHandler, "/obras", url.Values{"acao": {"situacao"}, "obra_id": {id(f.siteA)}, "situacao": {"PARALISADA"}})
	if response.Code == http.StatusSeeOther || !strings.Contains(response.Body.String(), "já está paralisada") {
		t.Errorf("paralisar obra já paralisada: status %d, sem a mensagem esperada", response.Code)
	}

	// Encerrar com 3 m³ de areia na obra: recusado, com a contagem.
	response = post(g, siteHandler, "/obras", url.Values{"acao": {"situacao"}, "obra_id": {id(f.siteA)}, "situacao": {"CONCLUIDA"}})
	if response.Code == http.StatusSeeOther || !strings.Contains(response.Body.String(), "1 material ainda tem saldo") {
		t.Errorf("encerrar obra com saldo: status %d, sem a mensagem esperada", response.Code)
	}
	if after := snapshot(t); after != before {
		t.Error("as tentativas recusadas mudaram o banco")
	}
}
