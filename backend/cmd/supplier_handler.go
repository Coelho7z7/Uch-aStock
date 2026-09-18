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

// suppliersPerPage é quantos fornecedores a lista mostra por página.
const suppliersPerPage = 10

// supplierHandler exibe a lista de fornecedores e processa o cadastro, a
// edição, a desativação e a reativação (POST). Com ?editar=ID a tela já
// abre com o modal daquele fornecedor. Todo cargo vê a lista; só quem tem
// PermManageSuppliers altera.
func supplierHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	scope, ok := requireSiteScope(w, r, user)
	if !ok {
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("pagina"))
	if page < 1 {
		page = 1
	}

	data := struct {
		User           *models.User
		Scope          siteScope
		Nav            navData
		CanManageUsers bool
		// CanManageSuppliers mostra Novo fornecedor, Editar, Desativar e
		// Reativar. É só UX: o POST confere de novo.
		CanManageSuppliers bool
		Suppliers          []models.Supplier
		Statuses           []struct{ Value, Label string }
		Search             string
		Status             string
		Total              int
		Page               int
		TotalPages         int
		PreviousPage       int
		NextPage           int
		Form               *models.Supplier
		FormError          string
		Message            string
	}{
		User:               user,
		Scope:              scope,
		Nav:                buildNav(user, scope),
		CanManageUsers:     can(user, PermManageUsers),
		CanManageSuppliers: can(user, PermManageSuppliers),
		Statuses:           services.SupplierStatusLabels,
		Search:             strings.TrimSpace(r.URL.Query().Get("busca")),
		Status:             r.URL.Query().Get("situacao"),
	}

	data.Message = map[string]string{
		"cadastrado": "Fornecedor cadastrado com sucesso.",
		"atualizado": "Fornecedor atualizado com sucesso.",
		"desativado": "Fornecedor desativado. Ele continua na lista, marcado como desativado.",
		"reativado":  "Fornecedor reativado.",
	}[r.URL.Query().Get("sucesso")]

	switch r.Method {
	case http.MethodPost:
		if !requirePermission(w, user, PermManageSuppliers) {
			return
		}

		form := &models.Supplier{
			Name:    r.FormValue("nome"),
			CNPJ:    r.FormValue("cnpj"),
			Contact: r.FormValue("contato"),
			Phone:   r.FormValue("telefone"),
			Email:   r.FormValue("email"),
			City:    r.FormValue("cidade"),
			Note:    r.FormValue("observacao"),
		}

		var err error
		success := ""
		action := r.FormValue("acao")

		switch action {
		case "cadastrar":
			err = services.CreateSupplierWeb(*form)
			success = "cadastrado"
		case "atualizar", "desativar", "reativar":
			form.ID, err = strconv.Atoi(r.FormValue("fornecedor_id"))
			if err != nil || form.ID <= 0 {
				err = services.ErrSupplierNotFound
				break
			}
			switch action {
			case "atualizar":
				err = services.UpdateSupplierWeb(*form)
				success = "atualizado"
			case "desativar":
				err = services.SetSupplierActive(form.ID, false)
				success = "desativado"
			case "reativar":
				err = services.SetSupplierActive(form.ID, true)
				success = "reativado"
			}
		default:
			http.Error(w, "Ação inválida", http.StatusBadRequest)
			return
		}

		if err == nil {
			http.Redirect(w, r, "/fornecedores?sucesso="+success, http.StatusSeeOther)
			return
		}

		// Deu erro no cadastro ou na edição: o modal reabre com o que foi
		// digitado. Desativar e reativar não têm modal: o erro aparece
		// direto na página.
		if action == "cadastrar" || action == "atualizar" {
			data.Form = form
		}
		data.FormError = supplierErrorMessage(err)

	case http.MethodGet:
		if editID, err := strconv.Atoi(r.URL.Query().Get("editar")); err == nil {
			if !data.CanManageSuppliers {
				data.FormError = "Você não pode editar fornecedores."
			} else if supplier, err := services.GetSupplierByID(editID); err == nil {
				data.Form = supplier
			} else {
				data.FormError = "Fornecedor não encontrado."
			}
		}

	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	suppliers, total, err := services.PaginatedSuppliers(data.Search, data.Status, page, suppliersPerPage)
	if err != nil {
		log.Println("erro em PaginatedSuppliers:", err)
		http.Error(w, "Erro ao buscar fornecedores", http.StatusInternalServerError)
		return
	}
	data.Suppliers = suppliers
	data.Total = total
	data.Page = page
	data.TotalPages = (total + suppliersPerPage - 1) / suppliersPerPage
	if data.TotalPages < 1 {
		data.TotalPages = 1
	}
	data.PreviousPage = page - 1
	data.NextPage = page + 1

	tmpl, err := template.ParseFiles("frontend/html/suppliers.html", "frontend/html/site_switcher.html")
	if err != nil {
		http.Error(w, "Erro ao carregar fornecedores", http.StatusInternalServerError)
		return
	}

	if data.FormError != "" {
		w.WriteHeader(http.StatusBadRequest)
	}

	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "Erro ao renderizar fornecedores", http.StatusInternalServerError)
	}
}

// supplierErrorMessage escolhe o que mostrar na tela, como
// siteErrorMessage: erro de digitação aparece como veio; erro do banco vai
// para o log e a pessoa vê uma mensagem genérica.
func supplierErrorMessage(err error) string {
	var inputErr services.SupplierInputError
	if errors.As(err, &inputErr) {
		return inputErr.Message
	}
	log.Println("erro ao salvar fornecedor:", err)
	return "Não foi possível salvar o fornecedor. Tente novamente."
}
