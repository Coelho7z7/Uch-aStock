package models

// Site é uma obra ou um local de estoque da empresa: o almoxarifado
// central ou uma obra em execução. Type e Status guardam o valor do
// banco ("OBRA", "ANDAMENTO"); os campos Formatted* trazem o texto que
// aparece na tela ("Obra", "Em andamento").
type Site struct {
	ID              int
	Name            string
	Type            string
	City            string
	Manager         string
	Status          string
	FormattedType   string
	FormattedStatus string
}
