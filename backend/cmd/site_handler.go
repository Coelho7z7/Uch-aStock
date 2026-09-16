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

// statusChangeDenied é o erro quando um gerente tenta mudar a situação.
var statusChangeDenied = services.SiteInputError{Message: "apenas administradores podem paralisar, concluir ou reabrir uma obra"}

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
		// CanChangeStatus: só administrador paralisa, conclui ou reabre.
		CanChangeStatus bool
		Statuses        []struct{ Value, Label string }
		Search          string
		Status          string
		Form            *siteForm
		FormError       string
		Message         string
		IsAdmin         bool
	}{
		User:            user,
		Scope:           scope,
		Teams:           teams,
		CanChangeStatus: hasAdminRole(user),
		Statuses:        services.SiteStatusLabels,
		Search:          strings.TrimSpace(r.URL.Query().Get("busca")),
		Status:          r.URL.Query().Get("situacao"),
		IsAdmin:         canViewUsersTab(r),
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
		// Cadastrar obra é só para administrador. Editar, também para o
		// gerente da obra — conferido abaixo, quando já se sabe qual é.
		if r.FormValue("acao") != "atualizar" && !requireAdmin(w, r) {
			return
		}

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
			err = services.CreateSiteWeb(form.Name, form.City, form.Manager)
			success = "cadastrada"
		case "atualizar":
			form.ID, err = strconv.Atoi(r.FormValue("obra_id"))
			if !hasAdminRole(user) && !requireSiteManager(w, user, form.ID) {
				return
			}
			if err != nil || form.ID <= 0 {
				err = services.ErrSiteNotFound
				break
			}
			// O tipo e a situação atual não vêm do formulário: são lidos do
			// banco, para o modal reaberto com erro saber se esconde o campo
			// de situação e se ainda precisa pedir confirmação.
			if site, findErr := services.GetSiteByID(form.ID); findErr == nil {
				form.IsCentral = site.Type == services.SiteTypeCentral
				form.OriginalStatus = site.Status
			}
			if !hasAdminRole(user) && form.Status != form.OriginalStatus {
				err = statusChangeDenied
				break
			}
			err = services.UpdateSiteWeb(form.ID, form.Name, form.City, form.Manager, form.Status)
			success = "atualizada"
		case "situacao":
			// Botões Paralisar, Retomar e Encerrar da lista. Só chega aqui
			// administrador: o requireAdmin lá em cima já barrou os outros.
			id, convErr := strconv.Atoi(r.FormValue("obra_id"))
			if convErr != nil || id <= 0 {
				err = services.ErrSiteNotFound
				break
			}
			err = services.ChangeSiteStatus(id, form.Status)
			success = statusChangeSuccess[form.Status]
		default:
			http.Error(w, "Ação inválida", http.StatusBadRequest)
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
			if site, err := services.GetSiteByID(editID); err == nil {
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
