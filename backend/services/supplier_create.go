package services

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
)

// Tamanho máximo, em letras, de cada campo do fornecedor.
const (
	maxSupplierNameLength  = 100
	maxSupplierTextLength  = 80
	maxSupplierPhoneLength = 20
	maxSupplierEmailLength = 120
	maxSupplierNoteLength  = 300
)

// SupplierInputError é um erro causado pelo que a pessoa digitou. Como em
// SiteInputError, a mensagem pode ir para a tela; a de um erro do banco não.
type SupplierInputError struct {
	Message string
}

func (e SupplierInputError) Error() string {
	return e.Message
}

// ErrSupplierNotFound é devolvido quando o fornecedor pedido não existe.
var ErrSupplierNotFound = SupplierInputError{"fornecedor não encontrado"}

// normalizeSupplier tira os espaços das pontas, deixa CNPJ e email num
// formato só e valida todos os campos. É usado no cadastro e na edição.
func normalizeSupplier(s models.Supplier) (models.Supplier, error) {
	s.Name = strings.TrimSpace(s.Name)
	s.Contact = strings.TrimSpace(s.Contact)
	s.Phone = strings.TrimSpace(s.Phone)
	s.Email = strings.ToLower(strings.TrimSpace(s.Email))
	s.City = strings.TrimSpace(s.City)
	s.Note = strings.TrimSpace(s.Note)

	cnpj, err := normalizeCNPJ(s.CNPJ)
	if err != nil {
		return s, err
	}
	s.CNPJ = cnpj

	switch {
	case s.Name == "":
		return s, SupplierInputError{"informe o nome do fornecedor"}
	case utf8.RuneCountInString(s.Name) > maxSupplierNameLength:
		return s, SupplierInputError{fmt.Sprintf("o nome do fornecedor pode ter no máximo %d caracteres", maxSupplierNameLength)}
	case utf8.RuneCountInString(s.Contact) > maxSupplierTextLength:
		return s, SupplierInputError{fmt.Sprintf("o contato pode ter no máximo %d caracteres", maxSupplierTextLength)}
	case utf8.RuneCountInString(s.City) > maxSupplierTextLength:
		return s, SupplierInputError{fmt.Sprintf("a cidade pode ter no máximo %d caracteres", maxSupplierTextLength)}
	case utf8.RuneCountInString(s.Note) > maxSupplierNoteLength:
		return s, SupplierInputError{fmt.Sprintf("a observação pode ter no máximo %d caracteres", maxSupplierNoteLength)}
	case !validPhone(s.Phone):
		return s, SupplierInputError{fmt.Sprintf("telefone inválido: use só números, espaço, parênteses, + e -, até %d caracteres", maxSupplierPhoneLength)}
	case !validEmail(s.Email):
		return s, SupplierInputError{"email inválido"}
	}
	return s, nil
}

// normalizeCNPJ tira a pontuação e confere os dígitos verificadores.
// Vazio é aceito: o CNPJ é opcional.
//
// Desde julho de 2026 a Receita também emite CNPJ alfanumérico: os 12
// primeiros caracteres podem ser letras (A-Z) ou números, e os 2 últimos
// continuam sendo dígitos verificadores. O cálculo é o mesmo de sempre,
// só que cada caractere vale o código ASCII dele menos 48: '0' vale 0,
// '9' vale 9 e 'A' vale 17. Para CNPJ só de números nada muda.
func normalizeCNPJ(raw string) (string, error) {
	cnpj := strings.ToUpper(raw)
	cnpj = strings.NewReplacer(".", "", "/", "", "-", "", " ", "").Replace(cnpj)
	if cnpj == "" {
		return "", nil
	}

	invalid := SupplierInputError{"CNPJ inválido: confira os 14 caracteres"}
	if len(cnpj) != 14 {
		return "", invalid
	}
	for i, c := range cnpj {
		isDigit := c >= '0' && c <= '9'
		isLetter := c >= 'A' && c <= 'Z'
		if !isDigit && (i >= 12 || !isLetter) {
			return "", invalid
		}
	}
	// "00000000000000" passa na conta dos dígitos, mas não é CNPJ.
	if strings.Count(cnpj, cnpj[:1]) == len(cnpj) {
		return "", invalid
	}

	if cnpjCheckDigit(cnpj[:12]) != cnpj[12] || cnpjCheckDigit(cnpj[:13]) != cnpj[13] {
		return "", invalid
	}
	return cnpj, nil
}

// cnpjCheckDigit calcula o próximo dígito verificador de base (12
// caracteres para o primeiro, 13 para o segundo). Os pesos vão de 2 a 9,
// da direita para a esquerda, recomeçando em 2 depois do 9.
func cnpjCheckDigit(base string) byte {
	sum := 0
	weight := 2
	for i := len(base) - 1; i >= 0; i-- {
		sum += int(base[i]-'0') * weight
		weight++
		if weight > 9 {
			weight = 2
		}
	}
	rest := sum % 11
	if rest < 2 {
		return '0'
	}
	return byte('0' + 11 - rest)
}

// formatCNPJ devolve o CNPJ com a pontuação de sempre: 12.345.678/0001-90.
func formatCNPJ(cnpj string) string {
	if len(cnpj) != 14 {
		return cnpj
	}
	return cnpj[0:2] + "." + cnpj[2:5] + "." + cnpj[5:8] + "/" + cnpj[8:12] + "-" + cnpj[12:14]
}

// validPhone aceita vazio ou um telefone digitado do jeito que a pessoa
// escreve: "(82) 99999-0000", "+55 82 3333-4444".
func validPhone(phone string) bool {
	if utf8.RuneCountInString(phone) > maxSupplierPhoneLength {
		return false
	}
	for _, c := range phone {
		if !strings.ContainsRune("0123456789 ()+-.", c) {
			return false
		}
	}
	return true
}

// validEmail aceita vazio ou algo no formato nome@dominio.algo. Não tenta
// ser completo: só pega o erro de digitação mais comum.
func validEmail(email string) bool {
	if email == "" {
		return true
	}
	if utf8.RuneCountInString(email) > maxSupplierEmailLength || strings.ContainsAny(email, " <>,;") {
		return false
	}
	user, domain, found := strings.Cut(email, "@")
	return found && user != "" && !strings.Contains(domain, "@") &&
		strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".")
}

// CreateSupplierWeb cadastra um fornecedor novo, já ativo.
func CreateSupplierWeb(s models.Supplier) error {
	s, err := normalizeSupplier(s)
	if err != nil {
		return err
	}

	// Como em CreateSiteWeb, a checagem de repetidos e o INSERT ficam na
	// mesma transação, para dois cadastros ao mesmo tempo não passarem os dois.
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := checkSupplierDuplicates(tx, s, 0); err != nil {
		return err
	}

	if _, err := tx.Exec(`
		INSERT INTO fornecedores (nome, cnpj, contato, telefone, email, cidade, observacao)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, s.Name, s.CNPJ, s.Contact, s.Phone, s.Email, s.City, s.Note); err != nil {
		return err
	}

	return tx.Commit()
}

// UpdateSupplierWeb altera os dados do fornecedor s.ID. Não mexe em ativo:
// isso é SetSupplierActive.
func UpdateSupplierWeb(s models.Supplier) error {
	s, err := normalizeSupplier(s)
	if err != nil {
		return err
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := supplierActiveTx(tx, s.ID); err != nil {
		return err
	}
	if err := checkSupplierDuplicates(tx, s, s.ID); err != nil {
		return err
	}

	if _, err := tx.Exec(`
		UPDATE fornecedores
		SET nome = ?, cnpj = ?, contato = ?, telefone = ?, email = ?, cidade = ?, observacao = ?
		WHERE id = ?
	`, s.Name, s.CNPJ, s.Contact, s.Phone, s.Email, s.City, s.Note, s.ID); err != nil {
		return err
	}

	return tx.Commit()
}

// SetSupplierActive desativa (active false) ou reativa um fornecedor.
// Pedir o estado em que ele já está é recusado, para a tela não dizer
// "desativado" de algo que nada mudou.
func SetSupplierActive(id int, active bool) error {
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	current, err := supplierActiveTx(tx, id)
	if err != nil {
		return err
	}
	if current == active {
		if active {
			return SupplierInputError{"o fornecedor já está ativo"}
		}
		return SupplierInputError{"o fornecedor já está desativado"}
	}

	value := 0
	if active {
		value = 1
	}
	if _, err := tx.Exec(`UPDATE fornecedores SET ativo = ? WHERE id = ?`, value, id); err != nil {
		return err
	}
	return tx.Commit()
}

// supplierActiveTx devolve se o fornecedor está ativo, ou
// ErrSupplierNotFound se ele não existe.
func supplierActiveTx(tx *sql.Tx, id int) (bool, error) {
	var active bool
	err := tx.QueryRow(`SELECT ativo FROM fornecedores WHERE id = ?`, id).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrSupplierNotFound
	}
	return active, err
}

// checkSupplierDuplicates recusa nome ou CNPJ que outro fornecedor já usa,
// inclusive um desativado: nesse caso o certo é reativar, não cadastrar de
// novo. exceptID é o próprio fornecedor, na edição (0 no cadastro).
//
// O nome é comparado no Go com strings.EqualFold pelo mesmo motivo de
// siteNameTaken: o LOWER do SQLite não entende letra acentuada.
func checkSupplierDuplicates(tx *sql.Tx, s models.Supplier, exceptID int) error {
	rows, err := tx.Query(`SELECT nome, cnpj, ativo FROM fornecedores WHERE id <> ?`, exceptID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var name, cnpj string
		var active bool
		if err := rows.Scan(&name, &cnpj, &active); err != nil {
			return err
		}

		field := ""
		switch {
		case strings.EqualFold(strings.TrimSpace(name), s.Name):
			field = "esse nome"
		case s.CNPJ != "" && cnpj == s.CNPJ:
			field = "esse CNPJ"
		default:
			continue
		}
		if !active {
			return SupplierInputError{fmt.Sprintf("já existe um fornecedor desativado com %s: reative-o em vez de cadastrar de novo", field)}
		}
		return SupplierInputError{fmt.Sprintf("já existe um fornecedor com %s", field)}
	}
	return rows.Err()
}
