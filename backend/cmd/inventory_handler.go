package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"

	"uchoastock/backend/models"
	"uchoastock/backend/services"
	"uchoastock/backend/utils"
)

// inventoriesPerPage é quantos inventários a lista mostra por página.
const inventoriesPerPage = 10

// inventoryErrorResponse decide a resposta para um erro do service de
// inventário, como requestErrorResponse: devolve a mensagem e true quando
// a tela deve ser mostrada de novo com ela; false quando a resposta já foi
// enviada (404, 403 ou 500).
func inventoryErrorResponse(w http.ResponseWriter, r *http.Request, user *models.User, err error) (string, bool) {
	var inputErr services.InventoryInputError
	switch {
	case errors.Is(err, services.ErrInventoryNotFound):
		renderNotFound(w, r, user)
		return "", false
	case errors.Is(err, services.ErrInventoryForbidden):
		renderAccessDenied(w)
		return "", false
	case errors.As(err, &inputErr):
		return inputErr.Message, true
	default:
		log.Println("erro no inventário:", err)
		http.Error(w, "Erro ao processar o inventário", http.StatusInternalServerError)
		return "", false
	}
}

// inventoryListHandler é a lista de inventários (GET /inventarios), com
// busca, filtro por situação e paginação, e o "Iniciar inventário" (POST).
// Cada pessoa só vê a própria obra, ou todas com PermAllSites.
func inventoryListHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	if !requirePermission(w, user, PermViewInventory) {
		return
	}
	scope, ok := requireSiteScope(w, r, user)
	if !ok {
		return
	}
	actor := inventoryActor(user)

	page, _ := strconv.Atoi(r.URL.Query().Get("pagina"))
	if page < 1 {
		page = 1
	}

	data := struct {
		User           *models.User
		Scope          siteScope
		Nav            navData
		CanManageUsers bool
		// CanStart mostra o botão "Iniciar inventário"; StartSites são as
		// obras em que a pessoa pode abrir um (a dela, ou todas para o
		// admin), menos as concluídas.
		CanStart    bool
		StartSites  []models.Site
		StartSiteID int
		// StartOpen manda o modal de iniciar já aberto (?iniciar=1).
		StartOpen    bool
		Inventories  []models.Inventory
		Statuses     []struct{ Value, Label string }
		Search       string
		Status       string
		Total        int
		Page         int
		TotalPages   int
		PreviousPage int
		NextPage     int
		Error        string
	}{
		User:           user,
		Scope:          scope,
		Nav:            buildNav(user, scope),
		CanManageUsers: can(user, PermManageUsers),
		CanStart:       actor.CanCount,
		StartSiteID:    scope.SiteID(),
		StartOpen:      r.URL.Query().Get("iniciar") != "",
		Statuses:       services.InventoryStatusLabels,
		Search:         strings.TrimSpace(r.URL.Query().Get("busca")),
		Status:         r.URL.Query().Get("situacao"),
	}
	for _, site := range scope.Sites {
		if actor.CanActAt(site.ID) && site.Status != services.SiteStatusFinished {
			data.StartSites = append(data.StartSites, site)
		}
	}
	if data.StartSiteID == 0 && !actor.AllSites {
		data.StartSiteID = user.SiteID
	}

	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		if r.FormValue("acao") != "iniciar" {
			http.Error(w, "Ação inválida", http.StatusBadRequest)
			return
		}
		siteID, _ := strconv.Atoi(r.FormValue("obra"))
		id, err := services.StartInventory(actor, siteID)
		if err == nil {
			http.Redirect(w, r, fmt.Sprintf("/inventarios/%d?sucesso=iniciado", id), http.StatusSeeOther)
			return
		}
		message, show := inventoryErrorResponse(w, r, user, err)
		if !show {
			return
		}
		data.Error = message
		data.StartSiteID = siteID
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	filter := actor.VisibleFilter(scope.SiteID())
	filter.Search = data.Search
	filter.Status = data.Status
	inventories, total, err := services.ListInventories(filter, page, inventoriesPerPage)
	if err != nil {
		log.Println("erro em ListInventories:", err)
		http.Error(w, "Erro ao buscar inventários", http.StatusInternalServerError)
		return
	}
	data.Inventories = inventories
	data.Total = total
	data.Page = page
	data.TotalPages = (total + inventoriesPerPage - 1) / inventoriesPerPage
	if data.TotalPages < 1 {
		data.TotalPages = 1
	}
	data.PreviousPage = page - 1
	data.NextPage = page + 1

	renderInventoryPage(w, "frontend/html/inventories.html", data.Error != "", data)
}

// inventoryButtons diz quais ações aparecem na tela da contagem. É só UX:
// o service confere tudo de novo.
type inventoryButtons struct {
	Count   bool
	Approve bool
	// Participated avisa por que o "Aprovar ajuste" não aparece para quem
	// pode aprovar, mas participou da contagem.
	Participated bool
	Reject       bool
	Cancel       bool
}

func buildInventoryButtons(actor services.InventoryActor, inventory *models.Inventory) inventoryButtons {
	var b inventoryButtons
	atSite := actor.CanActAt(inventory.SiteID)
	counting := inventory.Status == services.InventoryCounting
	awaiting := inventory.Status == services.InventoryAwaitingApproval

	b.Count = actor.CanCount && atSite && counting
	if actor.CanApprove && atSite && awaiting {
		b.Reject = true
		b.Approve = true
		if !actor.ApproveOwn {
			for _, id := range inventory.ParticipantIDs {
				if id == actor.UserID {
					b.Approve = false
					b.Participated = true
				}
			}
		}
	}
	isOpener := inventory.OpenedByID == actor.UserID && actor.CanCount && counting
	b.Cancel = atSite && (counting || awaiting) && (actor.CanApprove || isOpener)
	return b
}

// inventoryDetailHandler mostra a contagem (/inventarios/{id}) e processa
// as ações: salvar, enviar, acrescentar material, aprovar, rejeitar e
// cancelar. Inventário fora do alcance responde 404, igual a um que não
// existe.
func inventoryDetailHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	if !requirePermission(w, user, PermViewInventory) {
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		renderNotFound(w, r, user)
		return
	}
	scope, ok := requireSiteScope(w, r, user)
	if !ok {
		return
	}
	actor := inventoryActor(user)

	inventory, err := services.GetInventory(actor, id)
	if err != nil {
		inventoryErrorResponse(w, r, user, err)
		return
	}

	data := struct {
		User           *models.User
		Scope          siteScope
		Nav            navData
		CanManageUsers bool
		Inventory      *models.Inventory
		Buttons        inventoryButtons
		Materials      []models.Material
		Message        string
		Error          string
		// Typed marca que a tela voltou de um envio com erro: os campos
		// mostram o que foi digitado (Inputs e Notes, pelo ID do item) em
		// vez do que está gravado.
		Typed        bool
		Inputs       map[int]string
		Notes        map[int]string
		RejectOpen   bool
		RejectReason string
	}{
		User:           user,
		Scope:          scope,
		Nav:            buildNav(user, scope),
		CanManageUsers: can(user, PermManageUsers),
		Inventory:      inventory,
		Inputs:         map[int]string{},
		Notes:          map[int]string{},
		RejectOpen:     r.URL.Query().Get("rejeitar") != "",
	}

	data.Message = map[string]string{
		"iniciado":   "Inventário iniciado. As entradas e saídas desta obra estão bloqueadas até ele ser aprovado ou cancelado.",
		"salvo":      "Contagem salva.",
		"adicionado": "Material acrescentado à contagem.",
		"enviado":    "Contagem enviada para aprovação.",
		"aprovado":   "Ajuste aprovado. O saldo agora é o contado, e a obra voltou a aceitar entrada e saída.",
		"rejeitado":  "Inventário devolvido para contagem.",
		"cancelado":  "Inventário cancelado, sem ajuste. A obra voltou a aceitar entrada e saída.",
	}[r.URL.Query().Get("sucesso")]

	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		var actionErr error
		success := ""

		// Salvar e Enviar dividem o formulário da contagem. Com JS, o
		// script troca o "acao" escondido; sem JS, o botão Enviar manda
		// enviar=1 junto com acao=salvar.
		action := r.FormValue("acao")
		if action == "salvar" && r.FormValue("enviar") != "" {
			action = "enviar"
		}

		switch action {
		case "salvar", "enviar":
			data.Typed = true
			var counts []services.InventoryCount
			problem := ""
			for _, item := range inventory.Items {
				text := strings.TrimSpace(r.FormValue(fmt.Sprintf("contado_%d", item.ID)))
				note := r.FormValue(fmt.Sprintf("justificativa_%d", item.ID))
				data.Inputs[item.ID] = text
				data.Notes[item.ID] = note
				count := services.InventoryCount{ItemID: item.ID, Justification: note}
				if text != "" {
					quantity, err := utils.ParseQuantity(text)
					if err != nil && problem == "" {
						problem = fmt.Sprintf("Quantidade contada inválida para %s.", item.MaterialName)
					}
					count.Counted = &quantity
				}
				counts = append(counts, count)
			}
			if problem != "" {
				actionErr = services.InventoryInputError{Message: problem}
				break
			}
			actionErr = services.SaveInventoryCounts(actor, id, counts, action == "enviar")
			success = map[string]string{"salvar": "salvo", "enviar": "enviado"}[action]
		case "adicionar":
			materialID, _ := strconv.Atoi(r.FormValue("produto_id"))
			actionErr = services.AddInventoryItem(actor, id, materialID)
			success = "adicionado"
		case "aprovar":
			actionErr = services.ApproveInventory(actor, id)
			success = "aprovado"
		case "rejeitar":
			data.RejectReason = r.FormValue("motivo")
			actionErr = services.RejectInventory(actor, id, data.RejectReason)
			success = "rejeitado"
			data.RejectOpen = actionErr != nil
		case "cancelar":
			actionErr = services.CancelInventory(actor, id)
			success = "cancelado"
		default:
			http.Error(w, "Ação inválida", http.StatusBadRequest)
			return
		}

		if actionErr == nil {
			http.Redirect(w, r, fmt.Sprintf("/inventarios/%d?sucesso=%s", id, success), http.StatusSeeOther)
			return
		}
		message, show := inventoryErrorResponse(w, r, user, actionErr)
		if !show {
			return
		}
		data.Error = message
		// A situação pode ter mudado (outra pessoa agiu): relê para a tela.
		if fresh, err := services.GetInventory(actor, id); err == nil {
			data.Inventory = fresh
		}
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	data.Buttons = buildInventoryButtons(actor, data.Inventory)
	data.RejectOpen = data.RejectOpen && data.Buttons.Reject
	if data.Buttons.Count {
		if data.Materials, err = services.InventoryMaterialOptions(id); err != nil {
			log.Println("erro em InventoryMaterialOptions:", err)
		}
	}

	renderInventoryPage(w, "frontend/html/inventory_detail.html", data.Error != "", data)
}

// renderInventoryPage desenha uma das telas de inventário. withError
// responde 400, como as outras telas quando o envio deu errado.
func renderInventoryPage(w http.ResponseWriter, page string, withError bool, data any) {
	tmpl, err := template.ParseFiles(page, "frontend/html/site_switcher.html")
	if err != nil {
		log.Println("erro ao carregar", page, err)
		http.Error(w, "Erro ao carregar inventários", http.StatusInternalServerError)
		return
	}
	if withError {
		w.WriteHeader(http.StatusBadRequest)
	}
	if err := tmpl.Execute(w, data); err != nil {
		log.Println("erro ao renderizar", page, err)
	}
}

// inventoryExportHandler baixa a contagem em CSV
// (/inventarios/{id}/exportar), no mesmo formato da exportação de
// movimentações: ";" e BOM do UTF-8, para o Excel em português.
func inventoryExportHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	if !requirePermission(w, user, PermViewInventory) {
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		renderNotFound(w, r, user)
		return
	}
	inventory, err := services.GetInventory(inventoryActor(user), id)
	if err != nil {
		inventoryErrorResponse(w, r, user, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="inventario-`+inventory.Code+`.csv"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

	writer := csv.NewWriter(w)
	writer.Comma = ';'
	_ = writer.Write([]string{"Inventário", "Obra", "Situação", "Material", "Unidade", "Saldo esperado", "Contado", "Diferença", "Justificativa", "Contado por"})
	for _, item := range inventory.Items {
		// A diferença sai sem o "+" da tela: célula começando com "+" é
		// lida como fórmula pelo Excel.
		counted, difference := "", ""
		if item.IsCounted {
			counted = item.FormattedCounted
			difference = utils.FormatQuantity(item.Difference)
		}
		_ = writer.Write([]string{
			inventory.Code,
			csvSafe(inventory.SiteName),
			inventory.FormattedStatus,
			csvSafe(item.MaterialName),
			item.Unit,
			item.FormattedExpected,
			counted,
			difference,
			csvSafe(item.Justification),
			csvSafe(item.CountedByName),
		})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		log.Println("erro ao escrever CSV do inventário:", err)
	}
}
