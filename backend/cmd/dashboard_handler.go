package main

import (
	"html/template"
	"log"
	"net/http"
	"time"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
	"uchoastock/backend/services"
)

type DashboardData struct {
	User          *models.User
	Materials     []models.Material
	LowStock      []models.LowStockMaterial
	Activities    []models.Movement
	Summary       SummaryData
	LowStockLimit int
	// LowStockExtra é quanto sobrou além dos itens exibidos no painel,
	// para a linha "e mais N".
	LowStockExtra int
	GeneratedAt   string
}

type SummaryData struct {
	TotalMaterials int
	TotalStock     int
	LowStock       int
	TotalMovements int
	TodayEntries   int
	TodayExits     int
}

// lowStockPanelSize é quantos materiais o painel de alerta mostra. É o
// mesmo corte da atividade recente, para os dois painéis do dashboard
// ficarem com a mesma altura.
const lowStockPanelSize = 5

// dashboardHandler monta a visão geral do sistema.
func dashboardHandler(w http.ResponseWriter, r *http.Request) {
	userID, authenticated := userFromSession(r)
	if !authenticated {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	user, err := services.GetUserByID(userID)
	if err != nil {
		http.Error(w, "Usuário não encontrado", http.StatusInternalServerError)
		return
	}

	materials, err := services.GetAllMaterials()
	if err != nil {
		log.Println("erro em GetAllMaterials (dashboard):", err)
		http.Error(w, "Erro ao buscar materiais", http.StatusInternalServerError)
		return
	}

	summary, err := loadSummary()
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
		Materials:     materials,
		LowStock:      lowStock,
		Activities:    activities,
		Summary:       summary,
		LowStockLimit: services.LowStockThreshold,
		LowStockExtra: extra,
		GeneratedAt:   time.Now().Local().Format("02/01/2006 às 15:04"),
	}

	tmpl, err := template.ParseFiles("frontend/html/dashboard.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "Erro ao renderizar página", http.StatusInternalServerError)
		return
	}
}

// loadSummary calcula os números exibidos nos cartões do dashboard:
// quantos materiais existem, quantas unidades há no total, quantos
// estão prestes a acabar, quantas movimentações já foram registradas
// e o movimento de hoje.
func loadSummary() (SummaryData, error) {
	var summary SummaryData

	err := database.DB.QueryRow(`
		SELECT COUNT(*) FROM produtos WHERE ativo = 1
	`).Scan(&summary.TotalMaterials)
	if err != nil {
		return summary, err
	}

	err = database.DB.QueryRow(`
		SELECT COALESCE(SUM(quantidade), 0) FROM produtos WHERE ativo = 1
	`).Scan(&summary.TotalStock)
	if err != nil {
		return summary, err
	}

	summary.LowStock, err = services.CountLowStockMaterials()
	if err != nil {
		return summary, err
	}

	err = database.DB.QueryRow(`SELECT COUNT(*) FROM movimentacoes`).Scan(&summary.TotalMovements)
	if err != nil {
		return summary, err
	}

	summary.TodayEntries, summary.TodayExits, err = services.CountTodayMovements()
	return summary, err
}
