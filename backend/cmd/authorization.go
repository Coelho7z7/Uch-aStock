package main

import (
	"html/template"
	"log"
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

// requestActor traduz as permissões do usuário para o service de
// solicitações. O service confere as regras (obra, autor, própria
// solicitação) só com essas flags, sem saber nome de cargo.
func requestActor(user *models.User) services.RequestActor {
	return services.RequestActor{
		UserID:     user.ID,
		SiteID:     user.SiteID,
		AllSites:   can(user, PermAllSites),
		ViewAll:    can(user, PermViewAllRequests),
		CanCreate:  can(user, PermCreateRequest),
		CanApprove: can(user, PermApproveRequest),
		CanServe:   can(user, PermServeRequest),
		ApproveOwn: can(user, PermApproveOwnRequest),
	}
}

// inventoryActor monta o InventoryActor do usuário, com as permissões já
// resolvidas, como requestActor.
func inventoryActor(user *models.User) services.InventoryActor {
	return services.InventoryActor{
		UserID:     user.ID,
		SiteID:     user.SiteID,
		AllSites:   can(user, PermAllSites),
		CanView:    can(user, PermViewInventory),
		CanCount:   can(user, PermCountInventory),
		CanApprove: can(user, PermApproveInventory),
		ApproveOwn: can(user, PermApproveOwnInventory),
	}
}

// renderNotFound responde 404 com a página "não encontrada" no visual do
// sistema. É a mesma resposta para um endereço que não existe e para uma
// solicitação fora do alcance: quem pede não fica sabendo se ela existe.
// Com user nil (sem sessão), a página mostra só o cartão, sem a barra
// lateral.
func renderNotFound(w http.ResponseWriter, r *http.Request, user *models.User) {
	data := struct {
		User           *models.User
		Scope          siteScope
		Nav            navData
		CanManageUsers bool
	}{User: user}

	if user != nil {
		scope, err := resolveSiteScope(r, user)
		if err != nil {
			log.Println("erro ao descobrir a obra da sessão na página 404:", err)
		}
		data.Scope = scope
		data.Nav = buildNav(user, scope)
		data.CanManageUsers = can(user, PermManageUsers)
	}
	renderPage(w, http.StatusNotFound, "frontend/html/not_found.html", data)
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
