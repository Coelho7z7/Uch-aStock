package models

// Request é uma requisição de material: alguém da obra pede, o gestor
// aprova e o almoxarife atende. Status guarda o valor do banco
// ("PENDENTE"); FormattedStatus, o texto da tela ("Pendente"). As datas já
// vêm formatadas no horário local.
type Request struct {
	ID              int
	SiteID          int
	SiteName        string
	SiteStatus      string
	RequesterID     int
	RequesterName   string
	Status          string
	FormattedStatus string
	Note            string
	ApprovedByName  string
	ApprovedAt      string
	RejectionReason string
	CreatedDate     string
	CreatedAt       string
	UpdatedAt       string
	ItemCount       int
	Items           []RequestItem
	Events          []RequestEvent
}

// RequestItem é um material pedido na requisição. Missing é o que ainda
// falta entregar; Balance, o saldo atual do material na obra da
// requisição. LowBalance marca quando o saldo não cobre o que falta.
// SuggestedDelivery é o valor que o formulário de atendimento já traz: o
// que falta, limitado pelo saldo, no formato que o campo aceita ("2,5").
type RequestItem struct {
	ID                 int
	MaterialID         int
	MaterialName       string
	Unit               string
	MaterialActive     bool
	Requested          float64
	Fulfilled          float64
	Missing            float64
	Balance            float64
	FormattedRequested string
	FormattedFulfilled string
	FormattedMissing   string
	FormattedBalance   string
	LowBalance         bool
	Complete           bool
	SuggestedDelivery  string
}

// RequestEvent é uma linha do histórico da requisição.
type RequestEvent struct {
	UserName        string
	Action          string
	FormattedAction string
	Detail          string
	CreatedAt       string
}
