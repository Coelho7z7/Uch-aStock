package main

import (
	"errors"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"

	"uchoastock/backend/models"
	"uchoastock/backend/services"
)

// siteForm é o conteúdo do modal de obra. ID 0 é cadastro; outro valor
// é a edição daquela obra.
type siteForm struct {
	ID        int
	Name      string
	City      string
	Manager   string
	Status    string
	IsCentral bool
	// OriginalStatus é a situação gravada no banco. A tela compara com a
	// escolhida para saber se pede confirmação (paralisar ou concluir).
	OriginalStatus string
}

// statusChangeSuccess liga a situação escolhida no botão da lista à
// mensagem de sucesso (?sucesso=...) mostrada depois do redirecionamento.
var statusChangeSuccess = map[string]string{
	services.SiteStatusPaused:     "paralisada",
	services.SiteStatusInProgress: "retomada",
	services.SiteStatusFinished:   "encerrada",
}

// siteHandler exibe a lista de obras e processa o cadastro e a edição
// (POST). Com ?editar=ID a tela já abre com o modal daquela obra.
func siteHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	scope, ok := requireSiteScope(w, r, user)
	if !ok {
		return
	}

	teams, err := services.GetSiteTeams()
	if err != nil {
		log.Println("erro em GetSiteTeams:", err)
		http.Error(w, "Erro ao buscar a equipe das obras", http.StatusInternalServerError)
		return
	}

	data := struct {
		User  *models.User
		Scope siteScope
		Sites []models.Site
		// Teams é quem atua em cada obra, pelo ID da obra.
		Teams map[int][]string
		// CanCreateSite: cadastrar obra é de quem gerencia obras em todas
		// elas (administrador).
		CanCreateSite bool
		// CanReopenSite mostra o botão Reabrir nas obras concluídas.
		CanReopenSite bool
		// CanManageSites, AllSites e OwnSiteID decidem, linha a linha, em
		// quais obras aparecem Editar, Paralisar, Retomar e Encerrar: em
		// todas, ou só na obra vinculada ao usuário (ver canEditSite).
		CanManageSites bool
		AllSites       bool
		OwnSiteID      int
		Statuses       []struct{ Value, Label string }
		Search         string
		Status         string
		Form           *siteForm
		FormError      string
		Message        string
		CanManageUsers bool
	}{
		User:           user,
		Scope:          scope,
		Teams:          teams,
		CanCreateSite:  can(user, PermManageSites) && can(user, PermAllSites),
		CanManageSites: can(user, PermManageSites),
		CanReopenSite:  can(user, PermReopenSite),
		AllSites:       can(user, PermAllSites),
		OwnSiteID:      user.SiteID,
		Statuses:       services.SiteStatusLabels,
		Search:         strings.TrimSpace(r.URL.Query().Get("busca")),
		Status:         r.URL.Query().Get("situacao"),
		CanManageUsers: can(user, PermManageUsers),
	}

	data.Message = map[string]string{
		"cadastrada": "Obra cadastrada com sucesso.",
		"atualizada": "Obra atualizada com sucesso.",
		"paralisada": "Obra paralisada.",
		"retomada":   "Obra retomada: está em andamento de novo.",
		"encerrada":  "Obra encerrada. Ela continua na lista e no histórico.",
	}[r.URL.Query().Get("sucesso")]

	switch r.Method {
	case http.MethodPost:
		form := &siteForm{
			Name:    strings.TrimSpace(r.FormValue("nome")),
			City:    strings.TrimSpace(r.FormValue("cidade")),
			Manager: strings.TrimSpace(r.FormValue("responsavel")),
			Status:  r.FormValue("situacao"),
		}

		var err error
		success := ""

		switch r.FormValue("acao") {
		case "cadastrar":
			if !can(user, PermManageSites) || !can(user, PermAllSites) {
				renderAccessDenied(w)
				return
			}
			err = services.CreateSiteWeb(form.Name, form.City, form.Manager)
			success = "cadastrada"
		case "atualizar":
			form.ID, err = strconv.Atoi(r.FormValue("obra_id"))
			if err != nil || form.ID <= 0 {
				err = services.ErrSiteNotFound
				break
			}
			if !canEditSite(user, form.ID) {
				renderAccessDenied(w)
				return
			}
			// O tipo e a situação atual não vêm do formulário: são lidos do
			// banco, para o modal reaberto com erro saber se esconde o campo
			// de situação e se ainda precisa pedir confirmação.
			if site, findErr := services.GetSiteByID(form.ID); findErr == nil {
				form.IsCentral = site.Type == services.SiteTypeCentral
				form.OriginalStatus = site.Status
			}
			err = services.UpdateSiteWeb(form.ID, form.Name, form.City, form.Manager, form.Status, can(user, PermReopenSite))
			success = "atualizada"
		case "situacao":
			// Botões Paralisar, Retomar, Reabrir e Encerrar da lista. Aqui
			// se confere quem pode mexer nesta obra (canEditSite); quais
			// mudanças de situação valem, e o bloqueio de encerrar com
			// saldo, ficam no service. Reabrir obra concluída exige
			// PermReopenSite: sem ela o service devolve ErrReopenNotAllowed.
			id, convErr := strconv.Atoi(r.FormValue("obra_id"))
			if convErr != nil || id <= 0 {
				err = services.ErrSiteNotFound
				break
			}
			if !canEditSite(user, id) {
				renderAccessDenied(w)
				return
			}
			err = services.ChangeSiteStatus(id, form.Status, can(user, PermReopenSite))
			success = statusChangeSuccess[form.Status]
		default:
			http.Error(w, "Ação inválida", http.StatusBadRequest)
			return
		}

		if errors.Is(err, services.ErrReopenNotAllowed) {
			renderAccessDenied(w)
			return
		}
		if err == nil {
			http.Redirect(w, r, "/obras?sucesso="+success, http.StatusSeeOther)
			return
		}

		// Deu erro: o modal reabre com o que foi digitado, para a pessoa
		// corrigir em vez de preencher tudo de novo. O botão de situação
		// não tem modal: o erro aparece direto na página.
		if r.FormValue("acao") != "situacao" {
			data.Form = form
		}
		data.FormError = siteErrorMessage(err)

	case http.MethodGet:
		if editID, err := strconv.Atoi(r.URL.Query().Get("editar")); err == nil {
			if !canEditSite(user, editID) {
				data.FormError = "Você não pode editar esta obra."
			} else if site, err := services.GetSiteByID(editID); err == nil {
				data.Form = &siteForm{
					ID:        site.ID,
					Name:      site.Name,
					City:      site.City,
					Manager:   site.Manager,
					Status:    site.Status,
					IsCentral: site.Type == services.SiteTypeCentral,

					OriginalStatus: site.Status,
				}
			} else {
				data.FormError = "Obra não encontrada."
			}
		}

	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	sites, err := services.GetSites(data.Search, data.Status)
	if err != nil {
		log.Println("erro em GetSites:", err)
		http.Error(w, "Erro ao buscar obras", http.StatusInternalServerError)
		return
	}
	data.Sites = sites

	tmpl, err := template.ParseFiles("frontend/html/sites.html", "frontend/html/site_switcher.html")
	if err != nil {
		http.Error(w, "Erro ao carregar obras", http.StatusInternalServerError)
		return
	}

	if data.FormError != "" {
		w.WriteHeader(http.StatusBadRequest)
	}

	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "Erro ao renderizar obras", http.StatusInternalServerError)
	}
}

// siteErrorMessage escolhe o que mostrar na tela. Erro de digitação
// (SiteInputError) aparece como veio; erro do banco vai para o log e a
// pessoa vê uma mensagem genérica, sem detalhe interno.
//
// errors.As procura, dentro do erro, um valor do tipo pedido e o copia
// para inputErr — funciona mesmo se o erro tiver sido embrulhado com %w.
func siteErrorMessage(err error) string {
	var inputErr services.SiteInputError
	if errors.As(err, &inputErr) {
		return inputErr.Message
	}
	log.Println("erro ao salvar obra:", err)
	return "Não foi possível salvar a obra. Tente novamente."
}
