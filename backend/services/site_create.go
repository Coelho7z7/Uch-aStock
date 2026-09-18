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

// ErrReopenNotAllowed é devolvido quando alguém sem permissão tenta tirar
// uma obra de concluída. Não é SiteInputError de propósito: não é erro de
// digitação, e o handler responde "Acesso negado".
var ErrReopenNotAllowed = errors.New("reabrir obra concluída é só para administrador")

// siteStatusTransitions diz para quais situações uma obra pode ir a partir
// de cada uma. Ficar na mesma situação não é uma transição.
var siteStatusTransitions = map[string][]string{
	SiteStatusInProgress: {SiteStatusPaused, SiteStatusFinished},
	SiteStatusPaused:     {SiteStatusInProgress, SiteStatusFinished},
	SiteStatusFinished:   {SiteStatusInProgress}, // reabrir
}

// canTransitionSite indica se a obra pode passar de from para to.
func canTransitionSite(from, to string) bool {
	for _, allowed := range siteStatusTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

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
	// tem uma conexão só (SetMaxOpenConns(1)), outra solicitação espera
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
// Manter a situação é permitido (é o caso de só corrigir o nome); mudar
// passa pelas mesmas regras de ChangeSiteStatus. allowReopen diz se quem
// pede pode reabrir obra concluída.
func UpdateSiteWeb(id int, name, city, manager, status string, allowReopen bool) error {
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

	if _, err := checkSiteStatusChange(tx, id, status, allowReopen); err != nil {
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

// ChangeSiteStatus muda só a situação de uma obra: paralisar, retomar,
// encerrar (concluir) ou reabrir. É o que os botões da lista de obras
// usam, sem precisar reenviar nome, cidade e responsável. Pedir a
// situação em que a obra já está é recusado. allowReopen diz se quem pede
// pode reabrir obra concluída.
func ChangeSiteStatus(id int, status string, allowReopen bool) error {
	if !isValidSiteStatus(status) {
		return SiteInputError{"situação inválida"}
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	current, err := checkSiteStatusChange(tx, id, status, allowReopen)
	if err != nil {
		return err
	}
	if current == status {
		return SiteInputError{"a obra já está " + strings.ToLower(siteStatusLabel(status))}
	}

	if _, err := tx.Exec(`UPDATE obras SET situacao = ? WHERE id = ?`, status, id); err != nil {
		return err
	}

	return tx.Commit()
}

// checkSiteStatusChange confere se a obra existe e se pode ficar na
// situação pedida, e devolve a situação atual. Se a situação pedida é a
// atual, não há o que conferir: quem chama decide se isso é erro.
//
// Regras, nesta ordem:
//   - o almoxarifado central fica sempre em andamento: é dele que o
//     material sai para as obras;
//   - só vale uma transição de siteStatusTransitions;
//   - reabrir obra concluída exige allowReopen;
//   - encerrar exige que nenhum material ativo tenha saldo na obra e que
//     nenhuma solicitação esteja em aberto (pendente, aprovada ou parcial).
func checkSiteStatusChange(tx *sql.Tx, id int, status string, allowReopen bool) (string, error) {
	var siteType, current string
	err := tx.QueryRow(`SELECT tipo, situacao FROM obras WHERE id = ? AND ativo = 1`, id).Scan(&siteType, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSiteNotFound
	}
	if err != nil {
		return "", err
	}

	if siteType == SiteTypeCentral && status != SiteStatusInProgress {
		return current, SiteInputError{"o almoxarifado central não pode ser paralisado nem concluído"}
	}
	if status == current {
		return current, nil
	}
	if !canTransitionSite(current, status) {
		return current, SiteInputError{fmt.Sprintf("uma obra %s não pode passar para %s",
			strings.ToLower(siteStatusLabel(current)), strings.ToLower(siteStatusLabel(status)))}
	}
	if current == SiteStatusFinished && !allowReopen {
		return current, ErrReopenNotAllowed
	}

	if status == SiteStatusFinished {
		// Material removido do catálogo não conta: não há mais como
		// registrar a saída dele, e ele travaria o encerramento para sempre.
		// (Hoje DeleteMaterialWeb recusa material com saldo; isso cobre
		// materiais removidos antes dessa regra.)
		var stocked int
		if err := tx.QueryRow(`
			SELECT COUNT(*)
			FROM saldos s
			JOIN produtos p ON p.id = s.produto_id AND p.ativo = 1
			WHERE s.obra_id = ? AND s.quantidade > 0
		`, id).Scan(&stocked); err != nil {
			return current, err
		}
		openRequests, err := countOpenRequestsTx(tx, id)
		if err != nil {
			return current, err
		}
		openInventory, err := openInventoryTx(tx, id)
		if err != nil {
			return current, err
		}

		var blockers []string
		if stocked == 1 {
			blockers = append(blockers, "1 material ainda tem saldo nesta obra. Registre a saída antes de encerrar.")
		}
		if stocked > 1 {
			blockers = append(blockers, fmt.Sprintf("%d materiais ainda têm saldo nesta obra. Registre a saída de todos antes de encerrar.", stocked))
		}
		if openRequests == 1 {
			blockers = append(blockers, "1 solicitação ainda está em aberto (pendente, aprovada ou parcial). Atenda, rejeite ou cancele antes de encerrar.")
		}
		if openRequests > 1 {
			blockers = append(blockers, fmt.Sprintf("%d solicitações ainda estão em aberto (pendentes, aprovadas ou parciais). Atenda, rejeite ou cancele antes de encerrar.", openRequests))
		}
		if openInventory > 0 {
			blockers = append(blockers, fmt.Sprintf("o inventário %s ainda está aberto. Aprove ou cancele antes de encerrar.", InventoryCode(openInventory)))
		}
		if len(blockers) > 0 {
			return current, SiteInputError{"não é possível encerrar: " + strings.Join(blockers, " ")}
		}
	}
	return current, nil
}
