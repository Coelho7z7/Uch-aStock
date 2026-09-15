package services

import database "uchoastock/backend/database"

// DashboardSummary são os números dos cartões do dashboard.
type DashboardSummary struct {
	TotalMaterials int
	EmptyStock     int
	LowStock       int
	TotalMovements int
	TodayEntries   int
	TodayExits     int
}

// GetDashboardSummary calcula os números dos cartões. Essas consultas
// ficavam no handler; vieram para cá porque SQL mora em services (ver
// CLAUDE.md, seção 3).
//
// Não existe mais "total de unidades em estoque": com unidades
// diferentes (saco, m³, barra), somar tudo daria um número sem sentido.
// No lugar entra quantos materiais estão zerados.
func GetDashboardSummary() (DashboardSummary, error) {
	var summary DashboardSummary

	err := database.DB.QueryRow(`
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN quantidade <= 0 THEN 1 ELSE 0 END), 0)
		FROM produtos
		WHERE ativo = 1
	`).Scan(&summary.TotalMaterials, &summary.EmptyStock)
	if err != nil {
		return summary, err
	}

	summary.LowStock, err = CountLowStockMaterials()
	if err != nil {
		return summary, err
	}

	err = database.DB.QueryRow(`SELECT COUNT(*) FROM movimentacoes`).Scan(&summary.TotalMovements)
	if err != nil {
		return summary, err
	}

	summary.TodayEntries, summary.TodayExits, err = CountTodayMovements()
	return summary, err
}
