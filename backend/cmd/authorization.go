package main

import (
	"html/template"
	"net/http"

	"uchoastock/backend/models"
	"uchoastock/backend/services"
)

// loggedUser devolve o usuário da sessão, ou false se não houver sessão
// válida. As telas usam o usuário para mostrar nome e cargo na topbar.
func loggedUser(r *http.Request) (*models.User, bool) {
	userID, authenticated := userFromSession(r)
	if !authenticated {
		return nil, false
	}

	user, err := services.GetUserByID(userID)
	if err != nil {
		return nil, false
	}
	return user, true
}

// authenticatedHandler é um handler que já recebe o usuário logado.
type authenticatedHandler func(w http.ResponseWriter, r *http.Request, user *models.User)

// withUser é o "middleware" das telas internas: uma função que envolve o
// handler e roda antes dele. Carrega o usuário da sessão uma vez só por
// requisição e o entrega pronto; sem sessão, volta para o login e o
// handler nem é chamado.
func withUser(next authenticatedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, authenticated := loggedUser(r)
		if !authenticated {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		next(w, r, user)
	}
}

// requirePermission deixa passar quem tem a permissão p e responde
// "Acesso negado" para o resto. Devolve false quando já respondeu.
func requirePermission(w http.ResponseWriter, user *models.User, p Permission) bool {
	if can(user, p) {
		return true
	}
	renderAccessDenied(w)
	return false
}

// canActOnSite indica se a obra siteID está ao alcance do usuário: todas,
// para quem tem PermAllSites; senão, só a obra vinculada a ele. siteID 0
// ("Todas as obras") nunca é uma obra em que se possa agir.
//
// Não diz O QUE a pessoa pode fazer ali: isso é can(). As funções abaixo
// juntam as duas perguntas.
func canActOnSite(user *models.User, siteID int) bool {
	if siteID <= 0 || user == nil {
		return false
	}
	return can(user, PermAllSites) || user.SiteID == siteID
}

// canMoveStockAt indica se o usuário pode registrar entrada e saída na
// obra siteID.
func canMoveStockAt(user *models.User, siteID int) bool {
	return can(user, PermMoveStock) && canActOnSite(user, siteID)
}

// canEditSite indica se o usuário pode editar os dados e mudar a situação
// da obra siteID.
func canEditSite(user *models.User, siteID int) bool {
	return can(user, PermManageSites) && canActOnSite(user, siteID)
}

// renderAccessDenied responde 403 com a tela de acesso negado.
func renderAccessDenied(w http.ResponseWriter) {
	tmpl, err := template.ParseFiles("frontend/html/access_denied.html")
	if err != nil {
		http.Error(w, "Acesso negado", http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusForbidden)
	_ = tmpl.Execute(w, nil)
}
