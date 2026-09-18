package models

import "time"

// Movement é uma linha do histórico: uma entrada, uma saída ou uma
// atualização de cadastro. Note é a observação opcional (quem retirou,
// para qual etapa). SiteID é a obra onde aconteceu; atualização de
// cadastro não tem obra (SiteID 0, SiteName vazio).
type Movement struct {
	ID                int
	MaterialID        int
	UserID            int
	SiteID            int
	SiteName          string
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
	// RequestID é a solicitação que originou a saída (0 = avulsa).
	// RequestRequesterID é quem pediu essa solicitação, para decidir se o
	// link aparece (a pessoa precisa poder ver a solicitação).
	RequestID          int
	RequestRequesterID int
}
