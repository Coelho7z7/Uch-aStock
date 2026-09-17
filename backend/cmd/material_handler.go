package main

import (
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"

	"uchoastock/backend/models"
	"uchoastock/backend/services"
	"uchoastock/backend/utils"
)

// materialsPerPage é quantos materiais cada lista mostra por página. Era
// 5, pouco para quem procura um item no meio de dezenas.
const materialsPerPage = 10

// materialHandler exibe a lista de materiais e processa o cadastro de
// um novo material (POST).
func materialHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	scope, ok := requireSiteScope(w, r, user)
	if !ok {
		return
	}

	// O estoque inicial de um material novo entra na obra selecionada. Na
	// visão de todas as obras, entra no almoxarifado central.
	targetSite := scope.Current
	if targetSite == nil {
		targetSite = scope.Central()
	}

	data := struct {
		User         *models.User
		Scope        siteScope
		TargetSite   *models.Site
		Materials    []models.Material
		Units        []string
		Name         string
		Quantity     string
		Unit         string
		Minimum      string
		Search       string
		Order        string
		Message      string
		Error        string
		Page         int
		TotalPages   int
		PreviousPage int
		NextPage     int
		// Flags de permissão: escondem na tela o que a pessoa não pode
		// fazer. Quem barra de verdade é a checagem no POST.
		CanManageUsers bool
		// Nav são os contadores da barra lateral.
		Nav             navData
		CanEditMaterial bool
	}{
		User:            user,
		Scope:           scope,
		TargetSite:      targetSite,
		Units:           utils.MaterialUnits,
		Quantity:        "0",
		Unit:            "un",
		Minimum:         utils.FormatQuantity(services.LowStockThreshold),
		CanManageUsers:  can(user, PermManageUsers),
		Nav:             buildNav(user, scope),
		CanEditMaterial: can(user, PermEditMaterial),
	}

	data.Message = map[string]string{
		"cadastrado": "Material cadastrado com sucesso.",
		"atualizado": "Material atualizado com sucesso.",
		"removido":   "Material removido com sucesso.",
		"entrada":    "Estoque adicionado com sucesso.",
		"saida":      "Saída registrada com sucesso.",
	}[r.URL.Query().Get("sucesso")]

	if r.Method == http.MethodPost {
		if !requirePermission(w, user, PermEditMaterial) {
			return
		}
		data.Name = strings.TrimSpace(r.FormValue("nome"))
		data.Quantity = strings.TrimSpace(r.FormValue("quantidade"))
		data.Unit = r.FormValue("unidade")
		data.Minimum = strings.TrimSpace(r.FormValue("limite_minimo"))

		quantity, quantityErr := utils.ParseQuantity(data.Quantity)
		minimum, minimumErr := utils.ParseQuantity(data.Minimum)

		switch {
		case !sameSiteAsForm(r, scope):
			data.Error = siteChangedMessage
		case targetSite == nil:
			data.Error = "Almoxarifado central não encontrado."
		case data.Name == "":
			data.Error = "Informe o nome do material."
		case quantityErr != nil || quantity < 0:
			data.Error = "Informe uma quantidade inicial válida."
		case minimumErr != nil || minimum < 0:
			data.Error = "Informe um limite de aviso válido."
		case quantity > 0 && !canMoveStockAt(user, targetSite.ID):
			// Estoque inicial é uma entrada na obra: exige a mesma
			// permissão da tela de estoque.
			data.Error = "Você não pode dar entrada de estoque nesta obra. Cadastre o material com quantidade 0."
		default:
			if err := services.CreateMaterialWeb(data.Name, quantity, data.Unit, minimum, targetSite.ID, user.ID); err != nil {
				data.Error = err.Error()
			} else {
				http.Redirect(w, r, "/materiais?sucesso=cadastrado", http.StatusSeeOther)
				return
			}
		}
	} else if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("pagina"))
	if page < 1 {
		page = 1
	}

	data.Search = strings.TrimSpace(r.URL.Query().Get("busca"))
	data.Order = r.URL.Query().Get("ordem")
	if data.Order == "" {
		data.Order = "recentes"
	}
	materials, total, err := services.PaginatedSortedMaterials(
		data.Search,
		page,
		materialsPerPage,
		data.Order,
		scope.SiteID(),
	)
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

	tmpl, err := template.ParseFiles("frontend/html/materials.html", "frontend/html/site_switcher.html")
	if err != nil {
		http.Error(w, "Erro ao carregar materiais", http.StatusInternalServerError)
		return
	}

	if data.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}

	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "Erro ao renderizar materiais", http.StatusInternalServerError)
	}
}

// editMaterialHandler exibe a tela de edição/remoção de materiais e
// processa as ações de atualizar ou remover (POST).
//
// Com ?editar=ID a tela já abre com o modal de edição daquele material
// preenchido. É o destino do botão "Editar" da lista de materiais — antes
// ele só levava para esta tela, e a pessoa tinha que achar o material de
// novo, às vezes em outra página.
func editMaterialHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	messages := map[string]string{
		"atualizado": "Material atualizado com sucesso.",
		"removido":   "Material removido com sucesso.",
	}

	scope, ok := requireSiteScope(w, r, user)
	if !ok {
		return
	}

	data := struct {
		User         *models.User
		Scope        siteScope
		Materials    []models.Material
		Units        []string
		Editing      *models.Material
		Message      string
		Error        string
		Page         int
		TotalPages   int
		PreviousPage int
		NextPage     int
		// Flags de permissão: escondem na tela o que a pessoa não pode
		// fazer. Quem barra de verdade é a checagem no POST.
		CanManageUsers bool
		// Nav são os contadores da barra lateral.
		Nav               navData
		CanEditMaterial   bool
		CanRemoveMaterial bool
	}{
		User:              user,
		Scope:             scope,
		Units:             utils.MaterialUnits,
		Message:           messages[r.URL.Query().Get("sucesso")],
		CanManageUsers:    can(user, PermManageUsers),
		Nav:               buildNav(user, scope),
		CanEditMaterial:   can(user, PermEditMaterial),
		CanRemoveMaterial: can(user, PermRemoveMaterial),
	}

	if r.Method == http.MethodPost {
		// Cada ação exige a sua permissão (hoje as duas são só do
		// administrador, mas continuam separadas no map).
		action := r.FormValue("acao")
		if action == "remover" && !requirePermission(w, user, PermRemoveMaterial) {
			return
		}
		if action == "atualizar" && !requirePermission(w, user, PermEditMaterial) {
			return
		}
		materialID, idErr := strconv.Atoi(r.FormValue("material_id"))

		if idErr != nil {
			data.Error = "Material inválido."

		} else if action == "remover" {
			if opErr := services.DeleteMaterialWeb(materialID); opErr != nil {
				data.Error = opErr.Error()
			} else {
				http.Redirect(w, r, "/alterar-material?sucesso=removido", http.StatusSeeOther)
				return
			}

		} else if action == "atualizar" {
			name := strings.TrimSpace(r.FormValue("nome"))
			unit := r.FormValue("unidade")
			minimumText := strings.TrimSpace(r.FormValue("limite_minimo"))

			minimum, minimumErr := utils.ParseQuantity(minimumText)
			if minimumErr != nil {
				data.Error = "Informe um limite de aviso válido."
			} else if opErr := services.UpdateMaterialWeb(materialID, name, unit, minimum, user.ID); opErr != nil {
				data.Error = opErr.Error()
			} else {
				http.Redirect(w, r, "/alterar-material?sucesso=atualizado", http.StatusSeeOther)
				return
			}

			// Deu erro: o modal reabre com o que foi digitado, para a
			// pessoa corrigir em vez de preencher tudo de novo.
			data.Editing = &models.Material{ID: materialID, Name: name, Unit: unit, FormattedMinimum: minimumText}

		} else {
			data.Error = "Ação inválida."
		}

	} else if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	} else if editID, err := strconv.Atoi(r.URL.Query().Get("editar")); err == nil {
		if material, err := services.GetMaterialByID(editID); err == nil {
			data.Editing = material
		} else {
			data.Error = "Material não encontrado."
		}
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("pagina"))
	if page < 1 {
		page = 1
	}

	materials, total, err := services.PaginatedMaterials("", page, materialsPerPage, scope.SiteID())
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

	tmpl, err := template.ParseFiles("frontend/html/edit_material.html", "frontend/html/site_switcher.html")
	if err != nil {
		http.Error(w, "Erro ao carregar alteração de material", http.StatusInternalServerError)
		return
	}

	if data.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}

	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "Erro ao renderizar alteração de material", http.StatusInternalServerError)
	}
}
