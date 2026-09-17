package main

import (
	"html/template"
	"net/http"
	"time"

	"uchoastock/backend/models"
	"uchoastock/backend/services"
)

type DashboardData struct {
	User       *models.User
	Scope      siteScope
	LowStock   []models.LowStockMaterial
	Activities []models.Movement
	Summary    services.DashboardSummary
	// LowStockExtra é quanto sobrou além dos itens exibidos no painel,
	// para a linha "e mais N".
	LowStockExtra int
	GeneratedAt   string
	// OpenRequests são as solicitações em aberto mais antigas ao alcance
	// do usuário.
	OpenRequests []models.Request
	// CanManageUsers mostra a aba Usuários.
	CanManageUsers bool
	// Nav são os contadores da barra lateral.
	Nav navData
}

// lowStockPanelSize é quantos materiais o painel de alerta mostra. É o
// mesmo corte da atividade recente, para os dois painéis do dashboard
// ficarem com a mesma altura.
const lowStockPanelSize = 5

// dashboardHandler monta a visão geral do sistema.
func dashboardHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	scope, ok := requireSiteScope(w, r, user)
	if !ok {
		return
	}

	summary, err := services.GetDashboardSummary(scope.SiteID())
	if err != nil {
		http.Error(w, "Erro ao carregar resumo", http.StatusInternalServerError)
		return
	}

	// A atividade recente segue a mesma regra do histórico: quem não vê as
	// movimentações de todos vê só as próprias.
	activities, err := services.GetMovementsFilteredWeb(services.MovementFilter{
		SiteID: scope.SiteID(),
		UserID: ownMovementsOnly(user),
	})
	if err != nil {
		http.Error(w, "Erro ao carregar atividades", http.StatusInternalServerError)
		return
	}
	if len(activities) > 5 {
		activities = activities[:5]
	}

	lowStock, err := services.GetLowStockMaterials(lowStockPanelSize, scope.SiteID())
	if err != nil {
		http.Error(w, "Erro ao carregar materiais em falta", http.StatusInternalServerError)
		return
	}

	openRequests, err := services.OldestOpenRequests(requestActor(user).VisibleFilter(scope.SiteID()), 5)
	if err != nil {
		http.Error(w, "Erro ao carregar solicitações em aberto", http.StatusInternalServerError)
		return
	}

	extra := summary.LowStock - len(lowStock)
	if extra < 0 {
		extra = 0
	}

	data := DashboardData{
		User:          user,
		Scope:         scope,
		LowStock:      lowStock,
		Activities:    activities,
		Summary:       summary,
		LowStockExtra: extra,
		GeneratedAt:   time.Now().Local().Format("02/01/2006 às 15:04"),
		OpenRequests:  openRequests,

		CanManageUsers: can(user, PermManageUsers),
		Nav:            buildNav(user, scope),
	}

	tmpl, err := template.ParseFiles("frontend/html/dashboard.html", "frontend/html/site_switcher.html")
	if err != nil {
		http.Error(w, "Erro ao carregar o dashboard", http.StatusInternalServerError)
		return
	}

	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "Erro ao renderizar página", http.StatusInternalServerError)
		return
	}
}
