package utils

import (
	"fmt"
	"math"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// MaterialUnits são as unidades aceitas no cadastro de material. A lista
// é fechada de propósito: com texto livre, a mesma coisa viraria "saco",
// "sacos" e "sc", e a busca e o relatório deixariam de bater.
var MaterialUnits = []string{
	"un", "saco", "m³", "m²", "m", "kg", "litro", "barra", "milheiro", "lata", "caixa", "rolo",
}

func ValidateName(name string) bool {
	return strings.TrimSpace(name) != ""
}

// ValidateUnit indica se a unidade está na lista de MaterialUnits.
func ValidateUnit(unit string) bool {
	for _, valid := range MaterialUnits {
		if unit == valid {
			return true
		}
	}
	return false
}

// ParseQuantity converte o texto digitado em quantidade. Aceita vírgula
// ou ponto como separador decimal ("2,5" ou "2.5"): no Brasil se escreve
// com vírgula, mas o teclado numérico do celular às vezes só tem ponto.
// O sinal não é validado aqui — quem chama decide se aceita zero ou
// negativo.
func ParseQuantity(text string) (float64, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, fmt.Errorf("informe a quantidade")
	}

	// Com vírgula, o ponto só pode ser separador de milhar ("1.250,5").
	// Sem vírgula, o ponto é o decimal.
	if strings.Contains(text, ",") {
		text = strings.ReplaceAll(text, ".", "")
		text = strings.ReplaceAll(text, ",", ".")
	}

	// Só dígitos, ponto e sinal. Sem essa checagem o ParseFloat aceitaria
	// coisas como "1e3" ou "Inf".
	for _, char := range text {
		if !unicode.IsDigit(char) && char != '.' && char != '-' {
			return 0, fmt.Errorf("quantidade inválida")
		}
	}

	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, fmt.Errorf("quantidade inválida")
	}
	return RoundQuantity(value), nil
}

// RoundQuantity arredonda para 3 casas decimais — o bastante para
// 0,125 m³. Computador guarda fração em binário, e 1,1 - 0,3 dá
// 0,8000000000000002; arredondar evita que esse resíduo apareça na tela
// ou impeça uma saída que zeraria o estoque.
func RoundQuantity(value float64) float64 {
	return math.Round(value*1000) / 1000
}

// FormatQuantity escreve a quantidade no padrão brasileiro: vírgula nos
// decimais, ponto nos milhares e sem zeros sobrando ("1.200", "2,5").
func FormatQuantity(value float64) string {
	value = RoundQuantity(value)
	if value == 0 {
		return "0"
	}

	negative := value < 0
	if negative {
		value = -value
	}

	text := strconv.FormatFloat(value, 'f', -1, 64)
	intPart, fracPart, _ := strings.Cut(text, ".")

	var grouped strings.Builder
	for i, digit := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			grouped.WriteByte('.')
		}
		grouped.WriteRune(digit)
	}

	result := grouped.String()
	if fracPart != "" {
		result += "," + fracPart
	}
	if negative {
		result = "-" + result
	}
	return result
}

// FormatQuantityInput escreve a quantidade para o valor de um campo de
// formulário: vírgula nos decimais e SEM separador de milhar ("1200",
// "2,5"). FormatQuantity serve para leitura ("1.200"), mas nesse formato
// o ParseQuantity leria "1.200" como 1,2.
func FormatQuantityInput(value float64) string {
	return strings.ReplaceAll(strconv.FormatFloat(RoundQuantity(value), 'f', -1, 64), ".", ",")
}

// ValidateDate confere se o texto é uma data no formato AAAA-MM-DD, que
// é o que o <input type="date"> envia.
func ValidateDate(text string) bool {
	_, err := time.Parse("2006-01-02", text)
	return err == nil
}

// ValidateEmail aceita qualquer endereço de email válido, de qualquer
// domínio. O formato é conferido pelo net/mail, da biblioteca padrão.
//
// Duas regras a mais: o endereço precisa vir puro ("Ana <ana@empresa.com>"
// é válido para o net/mail, mas não serve como login), e o domínio precisa
// ter um ponto no meio ("ana@empresacom" também passa no net/mail, mas é
// quase sempre erro de digitação).
func ValidateEmail(email string) bool {
	email = strings.TrimSpace(email)

	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return false
	}

	_, domain, _ := strings.Cut(email, "@")
	return strings.Contains(domain, ".") &&
		!strings.HasPrefix(domain, ".") &&
		!strings.HasSuffix(domain, ".")
}

func ValidatePassword(password string) bool {
	if len(password) < 6 {
		return false
	}

	for _, char := range password {
		if !unicode.IsLetter(char) && !unicode.IsDigit(char) {
			return true
		}
	}

	return false
}
