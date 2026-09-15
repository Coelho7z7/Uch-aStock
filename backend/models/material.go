package models

import "time"

// Material é um item de obra controlado no estoque (cimento, areia,
// vergalhão...). O sistema acompanha apenas a quantidade: obra não
// trabalha com preço de venda, e sim com o que entra e o que sai.
type Material struct {
	ID          int    `json:"id"`
	Name        string `json:"nome"`
	Quantity    int    `json:"quantidade"`
	StockStatus string `json:"status_estoque"`
}

// LowStockMaterial é um material prestes a acabar acompanhado de quem
// foi o último a movimentá-lo. Fica separado de Material porque esses
// dados extras só fazem sentido no painel de alerta do dashboard, e
// custam um JOIN que as outras telas não precisam pagar.
type LowStockMaterial struct {
	Material
	LastUser      string
	LastMovedAt   time.Time
	FormattedDate string
	FormattedType string
}
