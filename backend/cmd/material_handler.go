package main

import (
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"

	"uchoastock/backend/models"
	"uchoastock/backend/services"
)

// materialHandler exibe a lista de materiais e processa o cadastro de
// um novo material (POST).
func materialHandler(w http.ResponseWriter, r *http.Request) {
	userID, authenticated := userFromSession(r)
	if !authenticated {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	const materialsPerPage = 5

	data := struct {
		Materials    []models.Material
		Name         string
		Quantity     int
		Search       string
		Order        string
		Message      string
		Error        string
		Page         int
		TotalPages   int
		PreviousPage int
		NextPage     int
		IsAdmin      bool
	}{IsAdmin: canViewUsersTab(r)}

	data.Message = map[string]string{
		"cadastrado": "Material cadastrado com sucesso.",
		"atualizado": "Material atualizado com sucesso.",
		"removido":   "Material removido com sucesso.",
		"entrada":    "Estoque adicionado com sucesso.",
		"saida":      "Saída registrada com sucesso.",
	}[r.URL.Query().Get("sucesso")]

	if r.Method == http.MethodPost {
		if !requireAdmin(w, r) {
			return
		}
		data.Name = strings.TrimSpace(r.FormValue("nome"))
		quantityText := strings.TrimSpace(r.FormValue("quantidade"))

		quantity, quantityErr := strconv.Atoi(quantityText)

		if data.Name == "" {
			data.Error = "Informe o nome do material."
		} else if quantityErr != nil || quantity < 0 {
			data.Error = "Informe uma quantidade válida."
		} else if err := services.CreateMaterialWeb(
			data.Name,
			quantity,
			userID,
		); err != nil {
			data.Error = err.Error()
		} else {
			http.Redirect(
				w,
				r,
				"/materiais?sucesso=cadastrado",
				http.StatusSeeOther,
			)
			return
		}

		data.Quantity = quantity
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

	tmpl, err := template.ParseFiles("frontend/html/materials.html")
	if err != nil {
		http.Error(
			w,
			"Erro ao carregar materiais",
			http.StatusInternalServerError,
		)
		return
	}

	if data.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}

	if err := tmpl.Execute(w, data); err != nil {
		http.Error(
			w,
			"Erro ao renderizar materiais",
			http.StatusInternalServerError,
		)
	}
}

// editMaterialHandler exibe a tela de edição/remoção de materiais e
// processa as ações de atualizar ou remover (POST).
func editMaterialHandler(w http.ResponseWriter, r *http.Request) {
	userID, authenticated := userFromSession(r)
	if !authenticated {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	const materialsPerPage = 5

	messages := map[string]string{
		"atualizado": "Material atualizado com sucesso.",
		"removido":   "Material removido com sucesso.",
	}

	data := struct {
		Materials    []models.Material
		Message      string
		Error        string
		Page         int
		TotalPages   int
		PreviousPage int
		NextPage     int
		IsAdmin      bool
	}{
		Message: messages[r.URL.Query().Get("sucesso")],
		IsAdmin: canViewUsersTab(r),
	}

	if r.Method == http.MethodPost {
		if !requireAdmin(w, r) {
			return
		}
		materialID, idErr := strconv.Atoi(r.FormValue("material_id"))

		if idErr != nil {
			data.Error = "Material inválido."

		} else if r.FormValue("acao") == "remover" {
			opErr := services.DeleteMaterialWeb(materialID)

			if opErr == nil {
				http.Redirect(
					w,
					r,
					"/alterar-material?sucesso=removido",
					http.StatusSeeOther,
				)
				return
			}

			data.Error = opErr.Error()

		} else if r.FormValue("acao") == "atualizar" {
			name := strings.TrimSpace(r.FormValue("nome"))

			opErr := services.UpdateMaterialWeb(
				materialID,
				name,
				userID,
			)

			if opErr == nil {
				http.Redirect(
					w,
					r,
					"/alterar-material?sucesso=atualizado",
					http.StatusSeeOther,
				)
				return
			}

			data.Error = opErr.Error()

		} else {
			data.Error = "Ação inválida."
		}

	} else if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	// Paginação
	page, _ := strconv.Atoi(r.URL.Query().Get("pagina"))

	if page < 1 {
		page = 1
	}

	materials, total, err := services.PaginatedMaterials(
		"",
		page,
		materialsPerPage,
	)

	if err != nil {
		log.Println("erro em PaginatedMaterials:", err)
		http.Error(
			w,
			"Erro ao buscar materiais",
			http.StatusInternalServerError,
		)
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

	tmpl, err := template.ParseFiles(
		"frontend/html/edit_material.html",
	)

	if err != nil {
		http.Error(
			w,
			"Erro ao carregar alteração de material",
			http.StatusInternalServerError,
		)
		return
	}

	if data.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}

	if err := tmpl.Execute(w, data); err != nil {
		http.Error(
			w,
			"Erro ao renderizar alteração de material",
			http.StatusInternalServerError,
		)
	}
}
