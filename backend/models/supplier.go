package models

// Supplier é um fornecedor de material. CNPJ guarda só os 14 caracteres,
// sem pontuação; FormattedCNPJ é o texto da tela (12.345.678/0001-90).
// Active false é fornecedor desativado: continua no banco e na lista.
type Supplier struct {
	ID            int
	Name          string
	CNPJ          string
	FormattedCNPJ string
	Contact       string
	Phone         string
	Email         string
	City          string
	Note          string
	Active        bool
}
