package models

import "time"

// Movement é uma linha do histórico: uma entrada, uma saída ou uma
// atualização de cadastro. Note é a observação opcional (para qual
// obra foi, quem retirou).
type Movement struct {
	ID                int
	MaterialID        int
	UserID            int
	Type              string
	Quantity          float64
	Unit              string
	Note              string
	Date              time.Time
	Material          string
	User              string
	FormattedQuantity string
	FormattedDate     string
	FormattedTime     string
	FormattedType     string
}
