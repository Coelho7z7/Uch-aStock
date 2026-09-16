package models

import "time"

// Material é um item de obra controlado no estoque (cimento, areia,
// vergalhão...). O sistema acompanha apenas a quantidade: obra não
// trabalha com preço de venda, e sim com o que entra e o que sai.
//
// O cadastro é um só para a empresa. Quantity é o saldo da obra que foi
// consultada (ou a soma de todas, na visão "Todas as obras").
//
// Quantity e MinimumStock são float64 porque há material que se mede
// em fração (2,5 m³ de areia). Os campos Formatted* trazem o número já
// escrito no padrão brasileiro ("1.250,5"), prontos para o template.
type Material struct {
	ID                int     `json:"id"`
	Name              string  `json:"nome"`
	Quantity          float64 `json:"quantidade"`
	Unit              string  `json:"unidade"`
	MinimumStock      float64 `json:"limite_minimo"`
	StockStatus       string  `json:"status_estoque"`
	FormattedQuantity string  `json:"-"`
	FormattedMinimum  string  `json:"-"`
}

// LowStockMaterial é um material prestes a acabar numa obra, acompanhado
// de quem foi o último a movimentá-lo ali. Fica separado de Material
// porque esses dados extras só fazem sentido no painel de alerta do
// dashboard, e custam um JOIN que as outras telas não precisam pagar.
type LowStockMaterial struct {
	Material
	SiteID        int
	SiteName      string
	LastUser      string
	LastMovedAt   time.Time
	FormattedDate string
	FormattedType string
}
