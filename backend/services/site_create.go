package services

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	database "uchoastock/backend/database"
)

// Tamanho máximo, em letras, de cada campo da obra.
const (
	maxSiteNameLength = 100
	maxSiteTextLength = 80
)

// SiteInputError é um erro causado pelo que a pessoa digitou (nome vazio,
// nome repetido...). Ter um tipo próprio deixa o handler separar os dois
// casos: a mensagem deste pode ir para a tela; a de um erro do banco não,
// porque revelaria detalhe interno.
type SiteInputError struct {
	Message string
}

func (e SiteInputError) Error() string {
	return e.Message
}

// ErrSiteNotFound é devolvido quando a obra a editar não existe.
var ErrSiteNotFound = SiteInputError{"obra não encontrada"}

// normalizeSiteFields tira os espaços das pontas e valida os três campos
// de texto da obra. É usado tanto no cadastro quanto na edição.
func normalizeSiteFields(name, city, manager string) (string, string, string, error) {
	name = strings.TrimSpace(name)
	city = strings.TrimSpace(city)
	manager = strings.TrimSpace(manager)

	switch {
	case name == "":
		return "", "", "", SiteInputError{"informe o nome da obra"}
	case utf8.RuneCountInString(name) > maxSiteNameLength:
		return "", "", "", SiteInputError{fmt.Sprintf("o nome da obra pode ter no máximo %d caracteres", maxSiteNameLength)}
	case utf8.RuneCountInString(city) > maxSiteTextLength:
		return "", "", "", SiteInputError{fmt.Sprintf("a cidade pode ter no máximo %d caracteres", maxSiteTextLength)}
	case utf8.RuneCountInString(manager) > maxSiteTextLength:
		return "", "", "", SiteInputError{fmt.Sprintf("o responsável pode ter no máximo %d caracteres", maxSiteTextLength)}
	}
	return name, city, manager, nil
}

// CreateSiteWeb cadastra uma obra nova, já em andamento. Pela tela só se
// cadastra obra: o almoxarifado central é único e nasce com o banco.
func CreateSiteWeb(name, city, manager string) error {
	name, city, manager, err := normalizeSiteFields(name, city, manager)
	if err != nil {
		return err
	}

	// A checagem do nome e o INSERT ficam na mesma transação. Como o banco
	// tem uma conexão só (SetMaxOpenConns(1)), outra requisição espera
	// esta terminar, e dois cadastros simultâneos não passam os dois.
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	taken, err := siteNameTaken(tx, name, 0)
	if err != nil {
		return err
	}
	if taken {
		return SiteInputError{"já existe uma obra com esse nome"}
	}

	if _, err := tx.Exec(`
		INSERT INTO obras (nome, tipo, cidade, responsavel, situacao)
		VALUES (?, ?, ?, ?, ?)
	`, name, SiteTypeProject, city, manager, SiteStatusInProgress); err != nil {
		return err
	}

	return tx.Commit()
}

// UpdateSiteWeb altera nome, cidade, responsável e situação de uma obra.
// O almoxarifado central não pode ser paralisado nem concluído: é dele
// que o material sai para as obras.
func UpdateSiteWeb(id int, name, city, manager, status string) error {
	name, city, manager, err := normalizeSiteFields(name, city, manager)
	if err != nil {
		return err
	}
	if !isValidSiteStatus(status) {
		return SiteInputError{"situação inválida"}
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := checkSiteStatusChange(tx, id, status); err != nil {
		return err
	}

	taken, err := siteNameTaken(tx, name, id)
	if err != nil {
		return err
	}
	if taken {
		return SiteInputError{"já existe uma obra com esse nome"}
	}

	if _, err := tx.Exec(`
		UPDATE obras
		SET nome = ?, cidade = ?, responsavel = ?, situacao = ?
		WHERE id = ?
	`, name, city, manager, status, id); err != nil {
		return err
	}

	return tx.Commit()
}

// ChangeSiteStatus muda só a situação de uma obra: paralisar, retomar ou
// encerrar (concluir). É o que os botões da lista de obras usam, sem
// precisar reenviar nome, cidade e responsável.
func ChangeSiteStatus(id int, status string) error {
	if !isValidSiteStatus(status) {
		return SiteInputError{"situação inválida"}
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := checkSiteStatusChange(tx, id, status); err != nil {
		return err
	}

	if _, err := tx.Exec(`UPDATE obras SET situacao = ? WHERE id = ?`, status, id); err != nil {
		return err
	}

	return tx.Commit()
}

// checkSiteStatusChange confere se a obra existe e se pode ficar na
// situação pedida. O almoxarifado central não pode ser paralisado nem
// concluído: é dele que o material sai para as obras.
func checkSiteStatusChange(tx *sql.Tx, id int, status string) error {
	var siteType string
	err := tx.QueryRow(`SELECT tipo FROM obras WHERE id = ? AND ativo = 1`, id).Scan(&siteType)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSiteNotFound
	}
	if err != nil {
		return err
	}

	if siteType == SiteTypeCentral && status != SiteStatusInProgress {
		return SiteInputError{"o almoxarifado central não pode ser paralisado nem concluído"}
	}
	return nil
}
