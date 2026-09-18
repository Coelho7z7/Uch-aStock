package models

// Inventory é a contagem física de uma obra, comparada com o saldo do
// sistema. Code é o número da tela ("INV-0007"). Status guarda o valor do
// banco ("EM_CONTAGEM"); FormattedStatus, o texto da tela ("Em contagem").
// As datas já vêm formatadas no horário local.
type Inventory struct {
	ID              int
	Code            string
	SiteID          int
	SiteName        string
	Status          string
	FormattedStatus string
	OpenedByID      int
	OpenedByName    string
	OpenedDate      string
	OpenedAt        string
	SentByName      string
	SentAt          string
	DecidedByName   string
	DecidedAt       string
	RejectionReason string
	// ItemCount é quantos materiais estão na contagem; CountedCount, quantos
	// já foram contados; DifferenceCount, quantos contados diferem do saldo.
	ItemCount       int
	CountedCount    int
	DifferenceCount int
	Items           []InventoryItem
	// ParticipantIDs são quem abriu, quem enviou e quem registrou alguma
	// contagem: essas pessoas não aprovam o ajuste. Só GetInventory preenche.
	ParticipantIDs []int
}

// InventoryItem é um material da contagem. Expected é o saldo congelado;
// Counted só vale com IsCounted (item ainda não contado fica sem valor).
// Difference é Counted - Expected: positivo sobrou, negativo faltou.
// CountedInput é o valor no formato que o campo aceita ("2,5").
type InventoryItem struct {
	ID                  int
	MaterialID          int
	MaterialName        string
	Unit                string
	Expected            float64
	Counted             float64
	IsCounted           bool
	Difference          float64
	HasDifference       bool
	FormattedExpected   string
	FormattedCounted    string
	FormattedDifference string
	CountedInput        string
	Justification       string
	CountedByName       string
}
