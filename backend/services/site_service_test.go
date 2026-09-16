package services

import (
	"errors"
	"strings"
	"testing"

	database "uchoastock/backend/database"
)

// siteIDByName devolve o ID da obra com esse nome exato.
func siteIDByName(t *testing.T, name string) int {
	t.Helper()
	var id int
	if err := database.DB.QueryRow(`SELECT id FROM obras WHERE nome = ?`, name).Scan(&id); err != nil {
		t.Fatalf("ler ID de %s: %v", name, err)
	}
	return id
}

// assertInputError confere que err é um erro de digitação (e não do
// banco) e que a mensagem contém o trecho esperado.
func assertInputError(t *testing.T, err error, contains string) {
	t.Helper()
	var inputErr SiteInputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("esperava SiteInputError com %q, veio %v", contains, err)
	}
	if !strings.Contains(inputErr.Message, contains) {
		t.Errorf("mensagem = %q, esperado conter %q", inputErr.Message, contains)
	}
}

func TestCreateSiteWebStartsInProgress(t *testing.T) {
	setupTestDB(t)

	if err := CreateSiteWeb("  Residencial Atlântico ", " Maceió ", " Rafael Lima "); err != nil {
		t.Fatalf("cadastro falhou: %v", err)
	}

	site, err := GetSiteByID(siteIDByName(t, "Residencial Atlântico"))
	if err != nil {
		t.Fatalf("buscar obra: %v", err)
	}
	if site.Type != SiteTypeProject || site.Status != SiteStatusInProgress {
		t.Errorf("tipo/situação = %s/%s, esperado OBRA/ANDAMENTO", site.Type, site.Status)
	}
	if site.City != "Maceió" || site.Manager != "Rafael Lima" {
		t.Errorf("espaços não foram removidos: cidade %q, responsável %q", site.City, site.Manager)
	}
	if site.FormattedStatus != "Em andamento" {
		t.Errorf("situação formatada = %q", site.FormattedStatus)
	}
}

func TestCreateSiteWebRejectsInvalidInput(t *testing.T) {
	setupTestDB(t)

	if err := CreateSiteWeb("Parque das Acácias", "", ""); err != nil {
		t.Fatalf("cadastro inicial falhou: %v", err)
	}

	cases := []struct {
		name, city, manager, want string
	}{
		{"   ", "", "", "informe o nome"},
		// Mesmo nome com outra caixa, inclusive na letra acentuada.
		{"PARQUE DAS ACÁCIAS", "", "", "já existe"},
		{"Almoxarifado Central", "", "", "já existe"},
		{strings.Repeat("a", maxSiteNameLength+1), "", "", "no máximo"},
		{"Obra nova", strings.Repeat("ç", maxSiteTextLength+1), "", "cidade"},
		{"Obra nova", "", strings.Repeat("ã", maxSiteTextLength+1), "responsável"},
	}
	for _, c := range cases {
		assertInputError(t, CreateSiteWeb(c.name, c.city, c.manager), c.want)
	}

	// No limite exato ainda passa: o tamanho conta letras, não bytes.
	if err := CreateSiteWeb("Obra nova", strings.Repeat("ç", maxSiteTextLength), ""); err != nil {
		t.Errorf("cidade com %d letras deveria ser aceita: %v", maxSiteTextLength, err)
	}
}

func TestUpdateSiteWebChangesFields(t *testing.T) {
	setupTestDB(t)
	if err := CreateSiteWeb("Centro Empresarial", "Rio Largo", "Bruno"); err != nil {
		t.Fatalf("cadastro falhou: %v", err)
	}
	id := siteIDByName(t, "Centro Empresarial")

	// Manter o próprio nome (mudando só a caixa) não conta como repetido.
	if err := UpdateSiteWeb(id, "Centro Empresarial NORTE", "Rio Largo", "Bruno Alves", SiteStatusFinished, true); err != nil {
		t.Fatalf("edição falhou: %v", err)
	}
	if err := UpdateSiteWeb(id, "centro empresarial norte", "Rio Largo", "Bruno Alves", SiteStatusFinished, true); err != nil {
		t.Fatalf("mudar só a caixa do próprio nome falhou: %v", err)
	}

	site, err := GetSiteByID(id)
	if err != nil {
		t.Fatalf("buscar obra: %v", err)
	}
	if site.Name != "centro empresarial norte" || site.Manager != "Bruno Alves" || site.Status != SiteStatusFinished {
		t.Errorf("obra = %+v", site)
	}
}

func TestUpdateSiteWebRejectsInvalidChanges(t *testing.T) {
	setupTestDB(t)
	for _, name := range []string{"Obra A", "Obra B"} {
		if err := CreateSiteWeb(name, "", ""); err != nil {
			t.Fatalf("cadastrar %s: %v", name, err)
		}
	}
	idA := siteIDByName(t, "Obra A")
	centralID := siteIDByName(t, "Almoxarifado central")

	assertInputError(t, UpdateSiteWeb(idA, "obra b", "", "", SiteStatusInProgress, true), "já existe")
	assertInputError(t, UpdateSiteWeb(idA, "Obra A", "", "", "DEMOLIDA", true), "situação inválida")
	assertInputError(t, UpdateSiteWeb(idA, "", "", "", SiteStatusInProgress, true), "informe o nome")
	assertInputError(t, UpdateSiteWeb(99999, "Fantasma", "", "", SiteStatusInProgress, true), "não encontrada")
	assertInputError(t, UpdateSiteWeb(centralID, "Almoxarifado central", "", "", SiteStatusFinished, true), "central")
	assertInputError(t, UpdateSiteWeb(centralID, "Almoxarifado central", "", "", SiteStatusPaused, true), "central")

	// O central pode ser renomeado, desde que continue em andamento.
	if err := UpdateSiteWeb(centralID, "Central Uchôa", "Maceió", "", SiteStatusInProgress, true); err != nil {
		t.Errorf("renomear o central falhou: %v", err)
	}

	// Nenhuma edição recusada pode ter gravado algo.
	site, err := GetSiteByID(idA)
	if err != nil {
		t.Fatalf("buscar obra: %v", err)
	}
	if site.Name != "Obra A" || site.Status != SiteStatusInProgress {
		t.Errorf("edição recusada alterou a obra: %+v", site)
	}
}

func TestGetSitesOrdersAndFilters(t *testing.T) {
	setupTestDB(t)
	sites := []struct{ name, city, status string }{
		{"Zeta", "Maceió", SiteStatusInProgress},
		{"Alfa", "Arapiraca", SiteStatusFinished},
		{"Beta", "Maceió", SiteStatusPaused},
		{"Gama", "Rio Largo", SiteStatusInProgress},
	}
	for _, s := range sites {
		if err := CreateSiteWeb(s.name, s.city, ""); err != nil {
			t.Fatalf("cadastrar %s: %v", s.name, err)
		}
		if err := UpdateSiteWeb(siteIDByName(t, s.name), s.name, s.city, "", s.status, true); err != nil {
			t.Fatalf("definir situação de %s: %v", s.name, err)
		}
	}

	names := func(search, status string) string {
		t.Helper()
		list, err := GetSites(search, status)
		if err != nil {
			t.Fatalf("GetSites(%q, %q): %v", search, status, err)
		}
		var out []string
		for _, s := range list {
			out = append(out, s.Name)
		}
		return strings.Join(out, ",")
	}

	// Central primeiro; depois andamento, paralisada e concluída.
	if got := names("", ""); got != "Almoxarifado central,Gama,Zeta,Beta,Alfa" {
		t.Errorf("ordem = %s", got)
	}
	if got := names("", SiteStatusInProgress); got != "Almoxarifado central,Gama,Zeta" {
		t.Errorf("filtro andamento = %s", got)
	}
	if got := names("maceió", ""); got != "Zeta,Beta" {
		t.Errorf("busca por cidade = %s", got)
	}
	// Situação desconhecida é ignorada, em vez de esconder tudo.
	if got := names("", "QUALQUER"); got != "Almoxarifado central,Gama,Zeta,Beta,Alfa" {
		t.Errorf("situação desconhecida = %s", got)
	}
}

func TestChangeSiteStatus(t *testing.T) {
	setupTestDB(t)
	if err := CreateSiteWeb("Residencial Sol", "Maceió", "Ana"); err != nil {
		t.Fatalf("cadastro falhou: %v", err)
	}
	id := siteIDByName(t, "Residencial Sol")
	centralID := siteIDByName(t, "Almoxarifado central")

	// Paralisar, retomar e encerrar, nessa ordem. Os outros campos não mudam.
	for _, status := range []string{SiteStatusPaused, SiteStatusInProgress, SiteStatusFinished} {
		if err := ChangeSiteStatus(id, status, true); err != nil {
			t.Fatalf("mudar para %s: %v", status, err)
		}
		site, err := GetSiteByID(id)
		if err != nil {
			t.Fatalf("buscar obra: %v", err)
		}
		if site.Status != status || site.Name != "Residencial Sol" || site.City != "Maceió" || site.Manager != "Ana" {
			t.Errorf("depois de %s, obra = %+v", status, site)
		}
	}

	assertInputError(t, ChangeSiteStatus(id, "DEMOLIDA", true), "situação inválida")
	assertInputError(t, ChangeSiteStatus(id, "", true), "situação inválida")
	assertInputError(t, ChangeSiteStatus(99999, SiteStatusPaused, true), "não encontrada")
	assertInputError(t, ChangeSiteStatus(0, SiteStatusPaused, true), "não encontrada")
	assertInputError(t, ChangeSiteStatus(centralID, SiteStatusPaused, true), "central")
	assertInputError(t, ChangeSiteStatus(centralID, SiteStatusFinished, true), "central")

	// A mudança recusada não gravou nada.
	if site, err := GetSiteByID(centralID); err != nil || site.Status != SiteStatusInProgress {
		t.Errorf("central = %+v, erro %v", site, err)
	}
}

// setSiteStatus põe a obra numa situação direto no banco, sem passar pelas
// regras, para montar o ponto de partida de cada caso.
func setSiteStatus(t *testing.T, id int, status string) {
	t.Helper()
	if _, err := database.DB.Exec(`UPDATE obras SET situacao = ? WHERE id = ?`, status, id); err != nil {
		t.Fatal(err)
	}
}

func siteStatus(t *testing.T, id int) string {
	t.Helper()
	site, err := GetSiteByID(id)
	if err != nil {
		t.Fatal(err)
	}
	return site.Status
}

// TestSiteStatusTransitions confere as 9 combinações de situação atual e
// pedida, com permissão de reabrir.
func TestSiteStatusTransitions(t *testing.T) {
	setupTestDB(t)
	id := createTestSite(t, "Obra T", SiteStatusInProgress)

	allowed := map[[2]string]bool{
		{SiteStatusInProgress, SiteStatusPaused}:   true,
		{SiteStatusInProgress, SiteStatusFinished}: true,
		{SiteStatusPaused, SiteStatusInProgress}:   true,
		{SiteStatusPaused, SiteStatusFinished}:     true,
		{SiteStatusFinished, SiteStatusInProgress}: true,
	}
	statuses := []string{SiteStatusInProgress, SiteStatusPaused, SiteStatusFinished}
	for _, from := range statuses {
		for _, to := range statuses {
			setSiteStatus(t, id, from)
			err := ChangeSiteStatus(id, to, true)

			switch {
			case from == to:
				assertInputError(t, err, "já está")
			case allowed[[2]string{from, to}]:
				if err != nil {
					t.Errorf("%s -> %s deveria passar: %v", from, to, err)
				}
			default:
				assertInputError(t, err, "não pode passar")
			}

			want := from
			if allowed[[2]string{from, to}] {
				want = to
			}
			if got := siteStatus(t, id); got != want {
				t.Errorf("%s -> %s: situação ficou %s, esperado %s", from, to, got, want)
			}
		}
	}

	// Na edição, manter a situação é permitido (é o caso de corrigir o
	// nome), mas uma transição proibida continua proibida.
	setSiteStatus(t, id, SiteStatusFinished)
	if err := UpdateSiteWeb(id, "Obra T entregue", "", "", SiteStatusFinished, false); err != nil {
		t.Errorf("editar obra concluída mantendo a situação: %v", err)
	}
	assertInputError(t, UpdateSiteWeb(id, "Obra T entregue", "", "", SiteStatusPaused, true), "não pode passar")
}

func TestReopenSiteRequiresPermission(t *testing.T) {
	setupTestDB(t)
	id := createTestSite(t, "Obra R", SiteStatusFinished)

	if err := ChangeSiteStatus(id, SiteStatusInProgress, false); !errors.Is(err, ErrReopenNotAllowed) {
		t.Errorf("reabrir sem permissão pelo botão: erro = %v, esperado ErrReopenNotAllowed", err)
	}
	if err := UpdateSiteWeb(id, "Obra R", "", "", SiteStatusInProgress, false); !errors.Is(err, ErrReopenNotAllowed) {
		t.Errorf("reabrir sem permissão pela edição: erro = %v, esperado ErrReopenNotAllowed", err)
	}
	if got := siteStatus(t, id); got != SiteStatusFinished {
		t.Fatalf("reabertura recusada mudou a situação para %s", got)
	}

	if err := ChangeSiteStatus(id, SiteStatusInProgress, true); err != nil {
		t.Errorf("reabrir com permissão: %v", err)
	}
	if got := siteStatus(t, id); got != SiteStatusInProgress {
		t.Errorf("situação depois de reabrir = %s", got)
	}
}

func TestFinishSiteBlockedWhileStocked(t *testing.T) {
	userID := setupTestDB(t)
	id := createTestSite(t, "Obra E", SiteStatusInProgress)
	cement := createTestMaterial(t, userID, "Cimento", 0, 1)
	sand := createTestMaterial(t, userID, "Areia", 0, 1)
	gravel := createTestMaterial(t, userID, "Brita", 0, 1)
	for _, m := range []int{cement, sand, gravel} {
		if err := AddStockWeb(m, id, 2, userID, ""); err != nil {
			t.Fatal(err)
		}
	}

	// Material removido do catálogo não trava o encerramento. Hoje não dá
	// para remover material com saldo, então o caso é o de dados antigos:
	// removido direto no banco.
	if _, err := database.DB.Exec(`UPDATE produtos SET ativo = 0 WHERE id = ?`, gravel); err != nil {
		t.Fatal(err)
	}

	assertInputError(t, ChangeSiteStatus(id, SiteStatusFinished, true), "2 materiais ainda têm saldo")
	assertInputError(t, UpdateSiteWeb(id, "Obra E", "", "", SiteStatusFinished, true), "2 materiais ainda têm saldo")

	// Paralisada também não encerra com saldo.
	if err := ChangeSiteStatus(id, SiteStatusPaused, true); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStockExitWeb(cement, id, 2, userID, ""); err != nil {
		t.Fatal(err)
	}
	assertInputError(t, ChangeSiteStatus(id, SiteStatusFinished, true), "1 material ainda tem saldo")
	if got := siteStatus(t, id); got != SiteStatusPaused {
		t.Fatalf("encerramento recusado mudou a situação para %s", got)
	}

	// Saldo zerado: agora encerra.
	if err := RegisterStockExitWeb(sand, id, 2, userID, ""); err != nil {
		t.Fatal(err)
	}
	if err := ChangeSiteStatus(id, SiteStatusFinished, true); err != nil {
		t.Errorf("encerrar com o saldo zerado: %v", err)
	}
}
