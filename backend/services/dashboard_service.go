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

// GetDashboardSummary calcula os números dos cartões da obra siteID, ou
// da empresa toda quando siteID é 0. Essas consultas ficavam no handler;
// vieram para cá porque SQL mora em services (ver CLAUDE.md, seção 3).
//
// Não existe "total de unidades em estoque": com unidades diferentes
// (saco, m³, barra), somar tudo daria um número sem sentido. No lugar
// entra quantos materiais estão zerados.
//
// Numa obra, "materiais" são os que já têm saldo nela. Na visão de todas
// as obras, é o catálogo inteiro. Zerados e em falta contam cada obra
// separadamente, pelo mesmo motivo do alerta (ver lowStockWhere).
func GetDashboardSummary(siteID int) (DashboardSummary, error) {
	var summary DashboardSummary

	var err error
	if siteID == 0 {
		err = database.DB.QueryRow(`SELECT COUNT(*) FROM produtos WHERE ativo = 1`).Scan(&summary.TotalMaterials)
	} else {
		err = database.DB.QueryRow(`
			SELECT COUNT(*)
			FROM saldos s
			JOIN produtos p ON p.id = s.produto_id
			WHERE p.ativo = 1 AND s.obra_id = ?
		`, siteID).Scan(&summary.TotalMaterials)
	}
	if err != nil {
		return summary, err
	}

	err = database.DB.QueryRow(`
		SELECT COUNT(*)
		FROM saldos s
		JOIN produtos p ON p.id = s.produto_id
		JOIN obras o ON o.id = s.obra_id
		WHERE p.ativo = 1
			AND o.ativo = 1
			AND o.situacao <> 'CONCLUIDA'
			AND s.quantidade <= 0
			AND (? = 0 OR s.obra_id = ?)
	`, siteID, siteID).Scan(&summary.EmptyStock)
	if err != nil {
		return summary, err
	}

	summary.LowStock, err = CountLowStockMaterials(siteID)
	if err != nil {
		return summary, err
	}

	err = database.DB.QueryRow(`
		SELECT COUNT(*)
		FROM movimentacoes
		WHERE (? = 0 OR obra_id = ?)
	`, siteID, siteID).Scan(&summary.TotalMovements)
	if err != nil {
		return summary, err
	}

	summary.TodayEntries, summary.TodayExits, err = CountTodayMovements(siteID)
	return summary, err
}
