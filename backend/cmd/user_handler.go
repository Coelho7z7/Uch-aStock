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

// userHandler exibe a lista de usuários cadastrados e processa a
// criação de novos usuários, a alteração de permissão, a troca de senha
// e a remoção.
// A tela inteira exige PermManageUsers. Por enquanto só quem também age
// em todas as obras (administrador) entra; o gestor ganha acesso, com
// limites, quando as regras dele estiverem prontas.
func userHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	userID := user.ID
	if !requirePermission(w, user, PermManageUsers) || !requirePermission(w, user, PermAllSites) {
		return
	}

	scope, ok := requireSiteScope(w, r, user)
	if !ok {
		return
	}

	const usersPerPage = 8

	data := struct {
		User   *models.User
		Scope  siteScope
		Users  []models.User
		UserID int
		// CanManageUsers mostra a aba Usuários (aqui é sempre true: a tela
		// inteira já exige a permissão).
		CanManageUsers bool
		// Roles são as opções do dropdown de cargo.
		Roles        []struct{ Value, Label string }
		Search       string
		Name         string
		Email        string
		Role         string
		SiteID       int
		Message      string
		Error        string
		Page         int
		TotalPages   int
		PreviousPage int
		NextPage     int
	}{
		User:           user,
		Scope:          scope,
		UserID:         userID,
		CanManageUsers: true,
		Roles:          services.RoleLabels,
		// Cargo pré-selecionado no cadastro: o de menos poder, para
		// ninguém virar administrador sem querer.
		Role: services.RoleRequester,
	}

	data.Message = map[string]string{
		"criado":     "Usuário criado com sucesso.",
		"atualizado": "Permissão atualizada com sucesso.",
		"removido":   "Usuário removido com sucesso.",
		"senha":      "Senha atualizada com sucesso.",
	}[r.URL.Query().Get("sucesso")]

	if r.Method == http.MethodPost {
		switch r.FormValue("acao") {

		case "criar":
			data.Name = strings.TrimSpace(r.FormValue("nome"))
			data.Email = strings.TrimSpace(r.FormValue("email"))
			data.Role = r.FormValue("role")
			password := r.FormValue("senha")
			data.SiteID, _ = strconv.Atoi(r.FormValue("obra_id"))

			if err := services.CreateUserWeb(data.Name, data.Email, password, data.Role, data.SiteID); err != nil {
				data.Error = err.Error()
			} else {
				http.Redirect(w, r, "/usuarios?sucesso=criado", http.StatusSeeOther)
				return
			}

		case "alterar_permissao":
			targetID, idErr := strconv.Atoi(r.FormValue("usuario_id"))
			newRole := r.FormValue("role")
			siteID, siteErr := strconv.Atoi(r.FormValue("obra_id"))

			if idErr != nil {
				data.Error = "Usuário inválido."
			} else if siteErr != nil {
				data.Error = "Obra inválida."
			} else if targetID == userID {
				data.Error = "Você não pode alterar a sua própria permissão."
			} else if err := services.UpdateUserAccessWeb(targetID, newRole, siteID); err != nil {
				data.Error = err.Error()
			} else {
				http.Redirect(w, r, "/usuarios?sucesso=atualizado", http.StatusSeeOther)
				return
			}

		case "redefinir_senha":
			targetID, idErr := strconv.Atoi(r.FormValue("usuario_id"))

			if idErr != nil {
				data.Error = "Usuário inválido."
			} else if err := services.ResetUserPasswordWeb(targetID, userID, r.FormValue("senha")); err != nil {
				data.Error = err.Error()
			} else {
				http.Redirect(w, r, "/usuarios?sucesso=senha", http.StatusSeeOther)
				return
			}

		case "remover":
			targetID, idErr := strconv.Atoi(r.FormValue("usuario_id"))

			if idErr != nil {
				data.Error = "Usuário inválido."
			} else if targetID == userID {
				data.Error = "Você não pode remover a sua própria conta."
			} else if err := services.DeleteUserWeb(targetID); err != nil {
				data.Error = err.Error()
			} else {
				http.Redirect(w, r, "/usuarios?sucesso=removido", http.StatusSeeOther)
				return
			}

		default:
			data.Error = "Ação inválida."
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

	users, total, err := services.ListPaginatedUsers(data.Search, page, usersPerPage)
	if err != nil {
		log.Println("erro em ListPaginatedUsers:", err)
		http.Error(w, "Erro ao buscar usuários", http.StatusInternalServerError)
		return
	}

	data.Users = users
	data.Page = page
	data.TotalPages = (total + usersPerPage - 1) / usersPerPage
	if data.TotalPages < 1 {
		data.TotalPages = 1
	}
	data.PreviousPage = page - 1
	data.NextPage = page + 1

	tmpl, err := template.ParseFiles("frontend/html/users.html", "frontend/html/site_switcher.html")
	if err != nil {
		http.Error(w, "Erro ao carregar usuários", http.StatusInternalServerError)
		return
	}

	if data.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}

	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "Erro ao renderizar usuários", http.StatusInternalServerError)
	}
}
