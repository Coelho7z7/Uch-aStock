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
	LowStock   []models.LowStockMaterial
	Activities []models.Movement
	Summary    services.DashboardSummary
	// LowStockExtra é quanto sobrou além dos itens exibidos no painel,
	// para a linha "e mais N".
	LowStockExtra int
	GeneratedAt   string
}

// lowStockPanelSize é quantos materiais o painel de alerta mostra. É o
// mesmo corte da atividade recente, para os dois painéis do dashboard
// ficarem com a mesma altura.
const lowStockPanelSize = 5

// dashboardHandler monta a visão geral do sistema.
func dashboardHandler(w http.ResponseWriter, r *http.Request) {
	user, authenticated := loggedUser(r)
	if !authenticated {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	summary, err := services.GetDashboardSummary()
	if err != nil {
		http.Error(w, "Erro ao carregar resumo", http.StatusInternalServerError)
		return
	}

	activities, err := services.GetMovementsWeb()
	if err != nil {
		http.Error(w, "Erro ao carregar atividades", http.StatusInternalServerError)
		return
	}
	if len(activities) > 5 {
		activities = activities[:5]
	}

	lowStock, err := services.GetLowStockMaterials(lowStockPanelSize)
	if err != nil {
		http.Error(w, "Erro ao carregar materiais em falta", http.StatusInternalServerError)
		return
	}

	extra := summary.LowStock - len(lowStock)
	if extra < 0 {
		extra = 0
	}

	data := DashboardData{
		User:          user,
		LowStock:      lowStock,
		Activities:    activities,
		Summary:       summary,
		LowStockExtra: extra,
		GeneratedAt:   time.Now().Local().Format("02/01/2006 às 15:04"),
	}

	tmpl, err := template.ParseFiles("frontend/html/dashboard.html")
	if err != nil {
		http.Error(w, "Erro ao carregar o dashboard", http.StatusInternalServerError)
		return
	}

	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "Erro ao renderizar página", http.StatusInternalServerError)
		return
	}
}
