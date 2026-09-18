package services

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	database "uchoastock/backend/database"
	"uchoastock/backend/utils"
)

// Situações do inventário, como ficam gravadas em inventarios.status.
const (
	InventoryCounting         = "EM_CONTAGEM"
	InventoryAwaitingApproval = "AGUARDANDO_APROVACAO"
	InventoryApproved         = "APROVADO"
	InventoryCanceled         = "CANCELADO"
)

// InventoryStatusLabels liga cada situação ao texto da tela, na ordem do
// filtro.
var InventoryStatusLabels = []struct{ Value, Label string }{
	{InventoryCounting, "Em contagem"},
	{InventoryAwaitingApproval, "Aguardando aprovação"},
	{InventoryApproved, "Aprovado"},
	{InventoryCanceled, "Cancelado"},
}

// MovementAdjustment é o tipo da movimentação gerada pela aprovação do
// inventário. A quantidade tem sinal: positiva quando sobrou material,
// negativa quando faltou.
const MovementAdjustment = "AJUSTE"

// Limites do inventário.
const (
	maxJustificationLength         = 200
	maxInventoryRejectionReasonLen = 300
)

func inventoryStatusLabel(status string) string {
	for _, s := range InventoryStatusLabels {
		if s.Value == status {
			return s.Label
		}
	}
	return status
}

func isValidInventoryStatus(status string) bool {
	for _, s := range InventoryStatusLabels {
		if s.Value == status {
			return true
		}
	}
	return false
}

// InventoryCode é o número do inventário na tela: INV-0007.
func InventoryCode(id int) string {
	return fmt.Sprintf("INV-%04d", id)
}

// InventoryInputError é um erro que pode ir para a tela como está (regra
// de negócio ou dado digitado). Erro do banco nunca vem neste tipo.
type InventoryInputError struct {
	Message string
}

func (e InventoryInputError) Error() string {
	return e.Message
}

var (
	// ErrInventoryNotFound vale para inventário que não existe e para o que
	// existe fora do alcance de quem pede: a resposta é a mesma.
	ErrInventoryNotFound = errors.New("inventário não encontrado")

	// ErrInventoryForbidden é a pessoa ver o inventário, mas não poder
	// fazer a ação.
	ErrInventoryForbidden = errors.New("sem permissão para esta ação no inventário")

	// ErrInventoryChanged é a situação ter mudado entre a leitura e a
	// gravação (outra pessoa agiu antes).
	ErrInventoryChanged = InventoryInputError{"o inventário mudou enquanto você agia: outra pessoa já enviou, aprovou, rejeitou ou cancelou. Confira a situação atual."}

	// ErrSiteInInventory é devolvido por requireOperableSiteTx quando a obra
	// tem inventário aberto: entrada e saída ficam bloqueadas até ele ser
	// aprovado ou cancelado.
	ErrSiteInInventory = errors.New("obra em inventário: entradas e saídas ficam bloqueadas até o inventário ser aprovado ou cancelado")
)

// InventoryActor é quem age num inventário, com as permissões já
// resolvidas por quem chama (o handler, com can()). O service nunca olha
// nome de cargo.
type InventoryActor struct {
	UserID int
	// SiteID é a obra vinculada ao usuário (0 = nenhuma).
	SiteID int
	// AllSites: vê e age em qualquer obra (obras.todas).
	AllSites   bool
	CanView    bool
	CanCount   bool
	CanApprove bool
	// ApproveOwn: aprova o ajuste de uma contagem da qual participou
	// (inventario.aprovar_propria, que só o superadmin tem).
	ApproveOwn bool
}

// CanSee indica se o inventário da obra siteID está ao alcance do actor.
// Quem não passa aqui não vê nem age: recebe "não encontrado".
func (a InventoryActor) CanSee(siteID int) bool {
	return a.CanView && a.CanActAt(siteID)
}

// CanActAt indica se a obra está ao alcance: qualquer uma com AllSites,
// senão só a obra vinculada ao usuário.
func (a InventoryActor) CanActAt(siteID int) bool {
	return a.AllSites || (a.SiteID != 0 && siteID == a.SiteID)
}

// VisibleFilter devolve o recorte de inventários que o actor pode ver.
// selectedSiteID é a obra do seletor do topo e só vale para quem tem
// AllSites (0 = todas); os outros ficam sempre na própria obra.
func (a InventoryActor) VisibleFilter(selectedSiteID int) InventoryFilter {
	filter := InventoryFilter{SiteID: selectedSiteID}
	if !a.CanView {
		filter.None = true
	}
	if !a.AllSites {
		filter.SiteID = a.SiteID
		if a.SiteID == 0 {
			filter.None = true
		}
	}
	return filter
}

// InventoryCount é o que foi digitado para um item. Counted nil é campo
// vazio: o item fica sem contagem.
type InventoryCount struct {
	ItemID        int
	Counted       *float64
	Justification string
}

// StartInventory abre um inventário na obra siteID: congela o saldo de
// cada material com saldo diferente de zero ali e, a partir daí, a obra
// não aceita entrada nem saída. Devolve o ID do inventário.
func StartInventory(actor InventoryActor, siteID int) (int, error) {
	if !actor.CanCount {
		return 0, ErrInventoryForbidden
	}
	if siteID <= 0 {
		return 0, InventoryInputError{"escolha a obra do inventário"}
	}
	if !actor.CanActAt(siteID) {
		return 0, ErrInventoryForbidden
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var status string
	err = tx.QueryRow(`SELECT situacao FROM obras WHERE id = ? AND ativo = 1`, siteID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, InventoryInputError{"obra não encontrada"}
	}
	if err != nil {
		return 0, err
	}
	if status == SiteStatusFinished {
		return 0, InventoryInputError{"obra concluída não tem inventário: ela não aceita mais ajuste de estoque"}
	}

	openID, err := openInventoryTx(tx, siteID)
	if err != nil {
		return 0, err
	}
	if openID > 0 {
		return 0, InventoryInputError{fmt.Sprintf("esta obra já tem um inventário aberto (%s): conclua ou cancele antes de abrir outro", InventoryCode(openID))}
	}

	result, err := tx.Exec(`
		INSERT INTO inventarios (obra_id, status, aberto_por) VALUES (?, ?, ?)
	`, siteID, InventoryCounting, actor.UserID)
	if err != nil {
		return 0, err
	}
	id64, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	id := int(id64)

	// O saldo de agora vira o saldo esperado. Material removido do catálogo
	// fica de fora: não há mais como movimentá-lo.
	if _, err := tx.Exec(`
		INSERT INTO inventario_itens (inventario_id, produto_id, saldo_esperado)
		SELECT ?, s.produto_id, ROUND(s.quantidade, 3)
		FROM saldos s
		JOIN produtos p ON p.id = s.produto_id AND p.ativo = 1
		WHERE s.obra_id = ? AND ROUND(s.quantidade, 3) <> 0
	`, id, siteID); err != nil {
		return 0, err
	}

	return id, tx.Commit()
}

// AddInventoryItem acrescenta à contagem um material que não estava na
// lista (achado na obra sem saldo no sistema, por exemplo). O saldo
// esperado é o saldo atual dele na obra, que está congelado desde o início.
func AddInventoryItem(actor InventoryActor, inventoryID, materialID int) error {
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	state, err := loadInventoryTx(tx, actor, inventoryID)
	if err != nil {
		return err
	}
	if err := checkCounter(actor, state); err != nil {
		return err
	}

	var name string
	err = tx.QueryRow(`SELECT nome FROM produtos WHERE id = ? AND ativo = 1`, materialID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return InventoryInputError{"escolha um material do catálogo"}
	}
	if err != nil {
		return err
	}

	var exists int
	if err := tx.QueryRow(`
		SELECT COUNT(*) FROM inventario_itens WHERE inventario_id = ? AND produto_id = ?
	`, inventoryID, materialID).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return InventoryInputError{fmt.Sprintf("%s já está na contagem", name)}
	}

	expected, err := balanceTx(tx, materialID, state.SiteID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO inventario_itens (inventario_id, produto_id, saldo_esperado) VALUES (?, ?, ROUND(?, 3))
	`, inventoryID, materialID, expected); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveInventoryCounts grava as quantidades contadas e as justificativas.
// Com submit, também confere que tudo foi contado e que toda diferença tem
// justificativa, e manda o inventário para aprovação. Se a conferência
// falhar, nada é gravado (a tela volta com o que foi digitado).
func SaveInventoryCounts(actor InventoryActor, inventoryID int, counts []InventoryCount, submit bool) error {
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	state, err := loadInventoryTx(tx, actor, inventoryID)
	if err != nil {
		return err
	}
	if err := checkCounter(actor, state); err != nil {
		return err
	}

	items, err := loadInventoryItemsTx(tx, inventoryID)
	if err != nil {
		return err
	}
	byID := map[int]*inventoryItemState{}
	for i := range items {
		byID[items[i].ID] = &items[i]
	}

	seen := map[int]bool{}
	for _, c := range counts {
		item, ok := byID[c.ItemID]
		if !ok {
			return InventoryInputError{"um dos itens informados não pertence a este inventário"}
		}
		if seen[c.ItemID] {
			return InventoryInputError{fmt.Sprintf("%s foi informado mais de uma vez", item.Name)}
		}
		seen[c.ItemID] = true

		justification := strings.TrimSpace(c.Justification)
		if utf8.RuneCountInString(justification) > maxJustificationLength {
			return InventoryInputError{fmt.Sprintf("%s: a justificativa pode ter no máximo %d caracteres", item.Name, maxJustificationLength)}
		}

		var counted any
		countedBy := item.CountedBy
		if c.Counted == nil {
			item.Counted = nil
			countedBy = 0
		} else {
			value := utils.RoundQuantity(*c.Counted)
			if value < 0 {
				return InventoryInputError{fmt.Sprintf("%s: a quantidade contada não pode ser negativa", item.Name)}
			}
			// Quem muda o número passa a ser quem contou (e, por isso, não
			// aprova o ajuste). Só reescrever a justificativa não conta.
			if item.Counted == nil || *item.Counted != value {
				countedBy = actor.UserID
			}
			item.Counted = &value
			counted = value
		}
		item.Justification = justification
		item.CountedBy = countedBy

		var countedByValue any
		if countedBy > 0 {
			countedByValue = countedBy
		}
		if _, err := tx.Exec(`
			UPDATE inventario_itens SET quantidade_contada = ?, justificativa = ?, contado_por = ? WHERE id = ?
		`, counted, justification, countedByValue, item.ID); err != nil {
			return err
		}
	}

	if submit {
		var missing []string
		for _, item := range items {
			if item.Counted == nil {
				missing = append(missing, item.Name)
				continue
			}
			if utils.RoundQuantity(*item.Counted-item.Expected) != 0 && item.Justification == "" {
				return InventoryInputError{fmt.Sprintf("%s: justifique a diferença entre o contado e o saldo do sistema", item.Name)}
			}
		}
		if len(missing) == 1 {
			return InventoryInputError{fmt.Sprintf("falta contar %s", missing[0])}
		}
		if len(missing) > 1 {
			return InventoryInputError{fmt.Sprintf("faltam %d materiais sem contagem, como %s", len(missing), missing[0])}
		}

		if err := changeInventoryStatusTx(tx, inventoryID, []string{InventoryCounting}, InventoryAwaitingApproval); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			UPDATE inventarios SET enviado_por = ?, enviado_em = CURRENT_TIMESTAMP WHERE id = ?
		`, actor.UserID, inventoryID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ApproveInventory aprova o ajuste: cada material contado diferente do
// saldo esperado recebe uma movimentação AJUSTE com a diferença, o saldo
// passa a ser o contado e a obra volta a aceitar entrada e saída. Tudo numa
// transação: se um item falhar, nenhum ajuste fica gravado.
func ApproveInventory(actor InventoryActor, inventoryID int) error {
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	state, err := loadInventoryTx(tx, actor, inventoryID)
	if err != nil {
		return err
	}
	if !actor.CanApprove || !actor.CanActAt(state.SiteID) {
		return ErrInventoryForbidden
	}
	if state.Status != InventoryAwaitingApproval {
		return InventoryInputError{fmt.Sprintf("só inventário aguardando aprovação pode ser aprovado (este está %s)",
			strings.ToLower(inventoryStatusLabel(state.Status)))}
	}

	// Ninguém aprova a contagem da qual participou: quem abriu, quem enviou
	// ou quem registrou algum número. A exceção é ApproveOwn (superadmin).
	if !actor.ApproveOwn {
		var participated int
		if err := tx.QueryRow(`
			SELECT
				(SELECT COUNT(*) FROM inventarios WHERE id = ? AND (aberto_por = ? OR enviado_por = ?)) +
				(SELECT COUNT(*) FROM inventario_itens WHERE inventario_id = ? AND contado_por = ?)
		`, inventoryID, actor.UserID, actor.UserID, inventoryID, actor.UserID).Scan(&participated); err != nil {
			return err
		}
		if participated > 0 {
			return InventoryInputError{"você participou desta contagem e não pode aprovar o ajuste: outra pessoa com permissão de aprovar precisa decidir"}
		}
	}

	items, err := loadInventoryItemsTx(tx, inventoryID)
	if err != nil {
		return err
	}
	note := "Inventário " + InventoryCode(inventoryID)
	for _, item := range items {
		if item.Counted == nil {
			return InventoryInputError{fmt.Sprintf("%s está sem contagem", item.Name)}
		}
		difference := utils.RoundQuantity(*item.Counted - item.Expected)
		if difference == 0 {
			continue
		}

		// A obra ficou bloqueada desde o início, então o saldo tem de ser o
		// esperado. Se não for, algo passou por fora do sistema: melhor
		// recusar do que ajustar em cima de um número que ninguém conferiu.
		balance, err := balanceTx(tx, item.MaterialID, state.SiteID)
		if err != nil {
			return err
		}
		if utils.RoundQuantity(balance) != item.Expected {
			return InventoryInputError{fmt.Sprintf("o saldo de %s mudou durante o inventário: cancele e abra outro", item.Name)}
		}

		if err := addToBalanceTx(tx, item.MaterialID, state.SiteID, difference); err != nil {
			return err
		}
		if err := registerAdjustmentTx(tx, item.MaterialID, state.SiteID, actor.UserID, difference, note, inventoryID); err != nil {
			return err
		}
	}

	if err := changeInventoryStatusTx(tx, inventoryID, []string{InventoryAwaitingApproval}, InventoryApproved); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE inventarios SET decidido_por = ?, decidido_em = CURRENT_TIMESTAMP, motivo_rejeicao = '' WHERE id = ?
	`, actor.UserID, inventoryID); err != nil {
		return err
	}
	return tx.Commit()
}

// RejectInventory devolve o inventário para contagem, com o motivo. A obra
// continua bloqueada. Diferente de aprovar, quem contou pode rejeitar: é o
// jeito de reabrir a própria contagem para corrigir, e não mexe no estoque.
func RejectInventory(actor InventoryActor, inventoryID int, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return InventoryInputError{"informe o motivo da rejeição"}
	}
	if utf8.RuneCountInString(reason) > maxInventoryRejectionReasonLen {
		return InventoryInputError{fmt.Sprintf("o motivo pode ter no máximo %d caracteres", maxInventoryRejectionReasonLen)}
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	state, err := loadInventoryTx(tx, actor, inventoryID)
	if err != nil {
		return err
	}
	if !actor.CanApprove || !actor.CanActAt(state.SiteID) {
		return ErrInventoryForbidden
	}
	if state.Status != InventoryAwaitingApproval {
		return InventoryInputError{fmt.Sprintf("só inventário aguardando aprovação pode ser rejeitado (este está %s)",
			strings.ToLower(inventoryStatusLabel(state.Status)))}
	}

	if err := changeInventoryStatusTx(tx, inventoryID, []string{InventoryAwaitingApproval}, InventoryCounting); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE inventarios SET decidido_por = ?, decidido_em = CURRENT_TIMESTAMP, motivo_rejeicao = ? WHERE id = ?
	`, actor.UserID, reason, inventoryID); err != nil {
		return err
	}
	return tx.Commit()
}

// CancelInventory encerra o inventário sem ajustar nada e libera a obra.
// Quem aprova cancela em contagem ou aguardando aprovação; quem abriu,
// só enquanto ainda está em contagem.
func CancelInventory(actor InventoryActor, inventoryID int) error {
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	state, err := loadInventoryTx(tx, actor, inventoryID)
	if err != nil {
		return err
	}
	if state.Status != InventoryCounting && state.Status != InventoryAwaitingApproval {
		return InventoryInputError{fmt.Sprintf("um inventário %s não pode ser cancelado",
			strings.ToLower(inventoryStatusLabel(state.Status)))}
	}
	isOpener := state.OpenedBy == actor.UserID && actor.CanCount && state.Status == InventoryCounting
	if !actor.CanActAt(state.SiteID) || !(actor.CanApprove || isOpener) {
		return ErrInventoryForbidden
	}

	if err := changeInventoryStatusTx(tx, inventoryID, []string{InventoryCounting, InventoryAwaitingApproval}, InventoryCanceled); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE inventarios SET decidido_por = ?, decidido_em = CURRENT_TIMESTAMP WHERE id = ?
	`, actor.UserID, inventoryID); err != nil {
		return err
	}
	return tx.Commit()
}

// checkCounter reúne as regras de quem mexe na contagem: permissão de
// contar, obra ao alcance e inventário ainda em contagem.
func checkCounter(actor InventoryActor, state inventoryState) error {
	if !actor.CanCount || !actor.CanActAt(state.SiteID) {
		return ErrInventoryForbidden
	}
	if state.Status != InventoryCounting {
		return InventoryInputError{fmt.Sprintf("a contagem só pode ser alterada em contagem (este inventário está %s)",
			strings.ToLower(inventoryStatusLabel(state.Status)))}
	}
	return nil
}

// inventoryState é o que as ações precisam saber do inventário.
type inventoryState struct {
	SiteID   int
	OpenedBy int
	Status   string
}

// loadInventoryTx relê o inventário dentro da transação e confere se ele
// está ao alcance do actor. Fora do alcance é ErrInventoryNotFound.
func loadInventoryTx(tx *sql.Tx, actor InventoryActor, inventoryID int) (inventoryState, error) {
	var state inventoryState
	err := tx.QueryRow(`
		SELECT obra_id, aberto_por, status FROM inventarios WHERE id = ?
	`, inventoryID).Scan(&state.SiteID, &state.OpenedBy, &state.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return state, ErrInventoryNotFound
	}
	if err != nil {
		return state, err
	}
	if !actor.CanSee(state.SiteID) {
		return state, ErrInventoryNotFound
	}
	return state, nil
}

// inventoryItemState é um item lido dentro da transação. Counted nil é
// item sem contagem; CountedBy 0, ninguém.
type inventoryItemState struct {
	ID            int
	MaterialID    int
	Name          string
	Expected      float64
	Counted       *float64
	Justification string
	CountedBy     int
}

func loadInventoryItemsTx(tx *sql.Tx, inventoryID int) ([]inventoryItemState, error) {
	rows, err := tx.Query(`
		SELECT i.id, i.produto_id, p.nome, i.saldo_esperado, i.quantidade_contada, i.justificativa, COALESCE(i.contado_por, 0)
		FROM inventario_itens i
		JOIN produtos p ON p.id = i.produto_id
		WHERE i.inventario_id = ?
		ORDER BY p.nome COLLATE NOCASE
	`, inventoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []inventoryItemState
	for rows.Next() {
		var item inventoryItemState
		// sql.NullFloat64 é como o database/sql lê uma coluna que pode ser
		// NULL: Valid diz se veio um número.
		var counted sql.NullFloat64
		if err := rows.Scan(&item.ID, &item.MaterialID, &item.Name, &item.Expected, &counted, &item.Justification, &item.CountedBy); err != nil {
			return nil, err
		}
		item.Expected = utils.RoundQuantity(item.Expected)
		if counted.Valid {
			value := utils.RoundQuantity(counted.Float64)
			item.Counted = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// changeInventoryStatusTx muda a situação só se ela ainda for uma das
// esperadas (from), como changeRequestStatusTx: se outra pessoa agiu entre
// a leitura e a gravação, nada muda e a ação é recusada.
func changeInventoryStatusTx(tx *sql.Tx, inventoryID int, from []string, to string) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(from)), ", ")
	args := []any{to, inventoryID}
	for _, status := range from {
		args = append(args, status)
	}
	result, err := tx.Exec(`
		UPDATE inventarios
		SET status = ?, atualizado_em = CURRENT_TIMESTAMP
		WHERE id = ? AND status IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrInventoryChanged
	}
	return nil
}

// openInventoryTx devolve o ID do inventário aberto (em contagem ou
// aguardando aprovação) da obra, ou 0 se não houver.
func openInventoryTx(tx *sql.Tx, siteID int) (int, error) {
	var id int
	err := tx.QueryRow(`
		SELECT id FROM inventarios WHERE obra_id = ? AND status IN (?, ?) LIMIT 1
	`, siteID, InventoryCounting, InventoryAwaitingApproval).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// OpenInventoryAt devolve o ID do inventário aberto da obra, ou 0. É o
// que a tela de estoque usa para avisar que a obra está bloqueada.
func OpenInventoryAt(siteID int) (int, error) {
	var id int
	err := database.DB.QueryRow(`
		SELECT id FROM inventarios WHERE obra_id = ? AND status IN (?, ?) LIMIT 1
	`, siteID, InventoryCounting, InventoryAwaitingApproval).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}
