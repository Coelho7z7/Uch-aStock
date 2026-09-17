package main

import (
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"uchoastock/backend/models"
	"uchoastock/backend/services"
	"uchoastock/backend/utils"
)

// stockHandler exibe a tela de entrada e saída de material e processa
// cada movimentação (POST).
func stockHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	scope, ok := requireSiteScope(w, r, user)
	if !ok {
		return
	}

	data := struct {
		User         *models.User
		Scope        siteScope
		Materials    []models.Material
		Search       string
		Message      string
		Error        string
		PreviousPage int
		NextPage     int
		TotalPages   int
		Page         int
		// CanManageUsers mostra a aba Usuários.
		CanManageUsers bool
		// Nav são os contadores da barra lateral.
		Nav navData
		// CanEditMaterial mostra a aba Alterar material.
		CanEditMaterial bool
	}{
		User:            user,
		Scope:           scope,
		CanManageUsers:  can(user, PermManageUsers),
		Nav:             buildNav(user, scope),
		CanEditMaterial: can(user, PermEditMaterial),
	}

	messages := map[string]string{
		"entrada":    "Entrada registrada com sucesso.",
		"saida":      "Saída registrada com sucesso.",
		"cadastrado": "Material cadastrado com sucesso.",
	}

	// FormValue lê tanto da URL (GET) quanto do formulário (POST): a busca
	// e a página vêm como campos escondidos em cada linha, para a tela
	// voltar exatamente onde a pessoa estava depois de movimentar.
	data.Search = strings.TrimSpace(r.FormValue("busca"))
	page, _ := strconv.Atoi(r.FormValue("pagina"))
	if page < 1 {
		page = 1
	}

	if r.Method == http.MethodPost {
		// Precisa da permissão e, com uma obra escolhida, de poder agir nela
		// (a própria obra, ou qualquer uma para quem tem PermAllSites). Sem
		// obra escolhida ("Todas as obras"), o switch abaixo explica o
		// problema em vez de negar acesso.
		if !can(user, PermMoveStock) || (scope.Current != nil && !canMoveStockAt(user, scope.Current.ID)) {
			renderAccessDenied(w)
			return
		}
		materialID, idErr := strconv.Atoi(r.FormValue("material_id"))
		quantity, qtyErr := utils.ParseQuantity(r.FormValue("quantidade"))
		note := r.FormValue("observacao")
		action := r.FormValue("acao")

		// A obra vem da sessão, não do formulário: o obra_id do formulário
		// só serve para conferir que ninguém trocou de obra em outra aba.
		var opErr error
		switch {
		case !sameSiteAsForm(r, scope):
			data.Error = siteChangedMessage
		case scope.Current == nil:
			data.Error = "Selecione uma obra no topo para registrar entrada ou saída."
		case !scope.CanMove():
			opErr = services.ErrSiteFinished
		case idErr != nil:
			data.Error = "Material inválido."
		case qtyErr != nil || quantity <= 0:
			data.Error = "Informe uma quantidade maior que zero."
		case action == "entrada":
			opErr = services.AddStockWeb(materialID, scope.Current.ID, quantity, user.ID, note)
		case action == "saida":
			opErr = services.RegisterStockExitWeb(materialID, scope.Current.ID, quantity, user.ID, note)
		default:
			data.Error = "Ação inválida."
		}

		if opErr != nil {
			data.Error = opErr.Error()
		} else if data.Error == "" {
			redirectToStock(w, r, action, data.Search, page)
			return
		}
	} else if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	materials, total, err := services.PaginatedMaterials(data.Search, page, materialsPerPage, scope.SiteID())
	if err != nil {
		log.Println("erro em PaginatedMaterials:", err)
		http.Error(w, "Erro ao buscar materiais", http.StatusInternalServerError)
		return
	}

	data.Materials = materials
	data.Page = page

	data.TotalPages = (total + materialsPerPage - 1) / materialsPerPage
	if data.TotalPages < 1 {
		data.TotalPages = 1
	}

	data.PreviousPage = page - 1
	data.NextPage = page + 1

	if data.Message == "" {
		data.Message = messages[r.URL.Query().Get("sucesso")]
	}

	tmpl, err := template.ParseFiles("frontend/html/stock.html", "frontend/html/site_switcher.html")
	if err != nil {
		http.Error(w, "Erro ao carregar estoque", http.StatusInternalServerError)
		return
	}

	if data.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}

	if err := tmpl.Execute(w, data); err != nil {
		log.Println("erro ao renderizar estoque:", err)
		http.Error(w, "Erro ao renderizar estoque", http.StatusInternalServerError)
	}
}

// redirectToStock volta para a tela de estoque depois de uma
// movimentação, mantendo a busca e a página. url.Values monta a query
// string já com os caracteres especiais escapados ("m³", espaços...).
func redirectToStock(w http.ResponseWriter, r *http.Request, action string, search string, page int) {
	query := url.Values{"sucesso": {action}}
	if search != "" {
		query.Set("busca", search)
	}
	if page > 1 {
		query.Set("pagina", strconv.Itoa(page))
	}
	http.Redirect(w, r, "/estoque?"+query.Encode(), http.StatusSeeOther)
}
