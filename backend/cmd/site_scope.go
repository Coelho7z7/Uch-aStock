package main

import (
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"uchoastock/backend/models"
	"uchoastock/backend/services"
)

// siteScope é a obra em que a pessoa está trabalhando agora, escolhida
// no seletor do topo. Toda tela interna recebe um, para montar o
// seletor e para filtrar o que mostra.
type siteScope struct {
	// Sites são as opções do seletor.
	Sites []models.Site
	// Current é a obra escolhida. nil significa "Todas as obras".
	Current *models.Site
	// CanSeeAll indica se a pessoa pode usar "Todas as obras" (só admin
	// e superadmin).
	CanSeeAll bool
	// ReturnTo é a página atual, para o seletor voltar para ela depois
	// de trocar de obra.
	ReturnTo string
	// CanManage indica se a pessoa pode movimentar e editar a obra
	// escolhida (ver canManageSite).
	CanManage bool
	// IsManager indica se a pessoa é gerente, para a tela explicar por
	// que uma obra que não é a dela está só em consulta.
	IsManager bool
	// OwnSiteName é o nome da obra em que a pessoa atua ("" se nenhuma).
	OwnSiteName string
}

// SiteID é o ID da obra escolhida, ou 0 para "Todas as obras" — o
// formato que os services esperam.
func (s siteScope) SiteID() int {
	if s.Current == nil {
		return 0
	}
	return s.Current.ID
}

// CanMove indica se a obra escolhida aceita entrada e saída: precisa ser
// uma obra específica e não pode estar concluída.
func (s siteScope) CanMove() bool {
	return s.Current != nil && s.Current.Status != services.SiteStatusFinished
}

// Central devolve o almoxarifado central entre as opções.
func (s siteScope) Central() *models.Site {
	for i := range s.Sites {
		if s.Sites[i].Type == services.SiteTypeCentral {
			return &s.Sites[i]
		}
	}
	return nil
}

// resolveSiteScope descobre em qual obra a pessoa está. A escolha fica
// guardada na sessão, mas é conferida a cada requisição: se a obra não
// existe mais, ou se a pessoa não pode ver "Todas as obras", cai na obra
// em que ela atua ou, sem vínculo, no almoxarifado central.
func resolveSiteScope(r *http.Request, user *models.User) (siteScope, error) {
	scope := siteScope{
		CanSeeAll:   hasAdminRole(user),
		ReturnTo:    r.URL.Path,
		IsManager:   strings.EqualFold(strings.TrimSpace(user.Role), "gerente"),
		OwnSiteName: user.SiteName,
	}

	sites, err := services.GetSites("", "")
	if err != nil {
		return scope, err
	}
	scope.Sites = sites

	storedID := 0
	if hash, ok := sessionTokenHash(r); ok {
		if storedID, err = services.GetSessionSiteID(hash); err != nil {
			return scope, err
		}
	}

	// Sem obra escolhida, quem não pode ver todas começa na obra em que
	// atua; sem vínculo, no almoxarifado central.
	if storedID == 0 && !scope.CanSeeAll {
		storedID = user.SiteID
	}

	// &sites[i] aponta para o item dentro da lista. Com "for _, site :=
	// range", site seria uma cópia, e o ponteiro mudaria a cada volta.
	for i := range sites {
		if sites[i].ID == storedID {
			scope.Current = &sites[i]
		}
	}
	if scope.Current == nil && !scope.CanSeeAll {
		scope.Current = scope.Central()
	}
	scope.CanManage = canManageSite(user, scope.SiteID())
	return scope, nil
}

// requireSiteScope é o resolveSiteScope das telas: se der erro, já
// responde com 500 e devolve false.
func requireSiteScope(w http.ResponseWriter, r *http.Request, user *models.User) (siteScope, bool) {
	scope, err := resolveSiteScope(r, user)
	if err != nil {
		log.Println("erro ao descobrir a obra da sessão:", err)
		http.Error(w, "Erro ao carregar a obra selecionada", http.StatusInternalServerError)
		return scope, false
	}
	return scope, true
}

// sameSiteAsForm confere se a obra que estava na tela quando o formulário
// foi montado ainda é a obra da sessão. Sem isso, trocar de obra em outra
// aba e depois enviar um formulário antigo gravaria na obra errada.
func sameSiteAsForm(r *http.Request, scope siteScope) bool {
	return r.FormValue("obra_id") == strconv.Itoa(scope.SiteID())
}

// siteChangedMessage é o aviso quando sameSiteAsForm falha.
const siteChangedMessage = "A obra selecionada mudou (talvez em outra aba). Confira a obra no topo e tente de novo."

// siteSwitchHandler troca a obra da sessão (POST /obra-atual) e volta
// para a página de onde a pessoa veio.
func siteSwitchHandler(w http.ResponseWriter, r *http.Request) {
	user, authenticated := loggedUser(r)
	if !authenticated {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	siteID, err := strconv.Atoi(r.FormValue("obra_id"))
	switch {
	case err != nil || siteID < 0:
		http.Error(w, "Obra inválida", http.StatusBadRequest)
		return
	case siteID == 0 && !hasAdminRole(user):
		http.Error(w, "Só administradores podem ver todas as obras juntas", http.StatusForbidden)
		return
	case siteID > 0:
		if _, err := services.GetSiteByID(siteID); err != nil {
			http.Error(w, "Obra não encontrada", http.StatusBadRequest)
			return
		}
	}

	hash, _ := sessionTokenHash(r)
	if err := services.SetSessionSite(hash, siteID); err != nil {
		log.Println("erro ao trocar a obra da sessão:", err)
		http.Error(w, "Erro ao trocar de obra", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, safeReturnPath(r.FormValue("voltar")), http.StatusSeeOther)
}

// safeReturnPath só aceita voltar para uma página deste sistema. Um
// "voltar" vindo de fora, como "https://site-falso.com" ou
// "//site-falso.com" (que o navegador também lê como outro endereço),
// viraria um redirecionamento para qualquer lugar. A busca (?pagina=...)
// é descartada: a página e os filtros de uma obra não servem para outra.
func safeReturnPath(target string) string {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" ||
		!strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") ||
		strings.Contains(parsed.Path, `\`) {
		return "/dashboard"
	}
	return parsed.Path
}
