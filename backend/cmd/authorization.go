package main

import (
	"html/template"
	"net/http"
	"strings"

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

// isAdmin indica se o usuário logado tem poderes de administrador.
// O SuperAdmin (reservado a superadmin@gmail.com) também conta como admin aqui — ele
// fica acima do administrador na hierarquia, então tudo que um admin pode
// fazer o SuperAdmin também pode.
func isAdmin(r *http.Request) bool {
	userID, authenticated := userFromSession(r)
	if !authenticated {
		return false
	}

	user, err := services.GetUserByID(userID)
	if err != nil {
		return false
	}
	return hasAdminRole(user)
}

// hasAdminRole é a mesma regra de isAdmin, para quando o usuário já foi
// carregado e não vale a pena consultar o banco de novo.
func hasAdminRole(user *models.User) bool {
	role := strings.ToLower(strings.TrimSpace(user.Role))
	return role == "admin" || role == "superadmin"
}

// canViewUsersTab indica se o usuário logado pode acessar a aba de
// administração de usuários. Só administradores: o gerente cuida da
// obra dele, não das contas.
func canViewUsersTab(r *http.Request) bool {
	return isAdmin(r)
}

// canManageSite indica se o usuário pode registrar entrada e saída e
// editar os dados da obra siteID. Administrador pode em todas; gerente,
// só na obra em que atua; conta básica só consulta. siteID 0 ("Todas as
// obras") nunca é uma obra que se possa gerenciar.
func canManageSite(user *models.User, siteID int) bool {
	if siteID <= 0 {
		return false
	}
	if hasAdminRole(user) {
		return true
	}
	role := strings.ToLower(strings.TrimSpace(user.Role))
	return role == "gerente" && user.SiteID == siteID
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if isAdmin(r) {
		return true
	}
	renderAccessDenied(w)
	return false
}

// requireSiteManager é o requireAdmin das telas de obra: deixa passar
// quem pode gerenciar a obra (ver canManageSite) e mostra "Acesso negado"
// para o resto.
func requireSiteManager(w http.ResponseWriter, user *models.User, siteID int) bool {
	if canManageSite(user, siteID) {
		return true
	}
	renderAccessDenied(w)
	return false
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
