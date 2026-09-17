package main

import (
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"uchoastock/backend/models"
	"uchoastock/backend/services"
	"uchoastock/backend/utils"
)

// requestsPerPage é quantas requisições cabem em cada página da lista.
const requestsPerPage = 15

// navData são os contadores da barra lateral, iguais em todas as telas
// internas. Pending é quantas requisições esperam aprovação (para quem
// aprova); ToServe, quantas aprovadas ou parciais esperam atendimento
// (para quem atende). Contam só a obra do usuário (admin: a do seletor).
type navData struct {
	Pending int
	ToServe int
}

// buildNav calcula os contadores da barra lateral. Erro no banco não
// derruba a tela: o contador some e o motivo vai para o log.
func buildNav(user *models.User, scope siteScope) navData {
	actor := requestActor(user)
	visible := actor.VisibleFilter(scope.SiteID())
	var nav navData
	var err error

	if actor.CanApprove {
		filter := visible
		filter.Statuses = []string{services.RequestPending}
		if nav.Pending, err = services.CountRequests(filter); err != nil {
			log.Println("erro ao contar requisições pendentes:", err)
		}
	}
	if actor.CanServe {
		filter := visible
		filter.Statuses = []string{services.RequestApproved, services.RequestPartial}
		if nav.ToServe, err = services.CountRequests(filter); err != nil {
			log.Println("erro ao contar requisições para atender:", err)
		}
	}
	return nav
}

// requestErrorResponse decide a resposta para um erro do service de
// requisições. Devolve a mensagem para a tela e true quando a tela deve
// ser mostrada de novo com ela; false quando a resposta já foi enviada
// (404, 403 ou 500).
func requestErrorResponse(w http.ResponseWriter, r *http.Request, err error) (string, bool) {
	var inputErr services.RequestInputError
	switch {
	case errors.Is(err, services.ErrRequestNotFound):
		http.NotFound(w, r)
		return "", false
	case errors.Is(err, services.ErrRequestForbidden):
		renderAccessDenied(w)
		return "", false
	case errors.As(err, &inputErr):
		return inputErr.Message, true
	default:
		log.Println("erro na requisição:", err)
		http.Error(w, "Erro ao processar a requisição", http.StatusInternalServerError)
		return "", false
	}
}

// requestListHandler é a lista de requisições (GET /requisicoes), com
// filtro por situação, busca e paginação. Cada pessoa só vê o que está ao
// seu alcance (ver RequestActor.VisibleFilter).
func requestListHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}
	scope, ok := requireSiteScope(w, r, user)
	if !ok {
		return
	}
	actor := requestActor(user)

	page, _ := strconv.Atoi(r.URL.Query().Get("pagina"))
	if page < 1 {
		page = 1
	}

	data := struct {
		User           *models.User
		Scope          siteScope
		Nav            navData
		CanManageUsers bool
		CanCreate      bool
		// OnlyOwn avisa que a lista traz só as requisições da pessoa.
		OnlyOwn bool
		// OwnSiteOnly avisa que a lista é só da obra da pessoa, mesmo com
		// outra obra escolhida no seletor.
		OwnSiteOnly  bool
		Statuses     []struct{ Value, Label string }
		Status       string
		Search       string
		Requests     []models.Request
		Total        int
		Page         int
		TotalPages   int
		PreviousPage int
		NextPage     int
	}{
		User:           user,
		Scope:          scope,
		Nav:            buildNav(user, scope),
		CanManageUsers: can(user, PermManageUsers),
		CanCreate:      actor.CanCreate,
		OnlyOwn:        !actor.ViewAll,
		OwnSiteOnly:    !actor.AllSites,
		Statuses:       services.RequestStatusLabels,
		Status:         r.URL.Query().Get("situacao"),
		Search:         strings.TrimSpace(r.URL.Query().Get("busca")),
		Page:           page,
	}

	filter := actor.VisibleFilter(scope.SiteID())
	if data.Status != "" {
		filter.Statuses = []string{data.Status}
	}
	filter.Search = data.Search

	requests, total, err := services.ListRequests(filter, page, requestsPerPage)
	if err != nil {
		log.Println("erro ao listar requisições:", err)
		http.Error(w, "Erro ao buscar requisições", http.StatusInternalServerError)
		return
	}
	data.Requests = requests
	data.Total = total
	data.TotalPages = (total + requestsPerPage - 1) / requestsPerPage
	if data.TotalPages < 1 {
		data.TotalPages = 1
	}
	data.PreviousPage = page - 1
	data.NextPage = page + 1

	renderPage(w, http.StatusOK, "frontend/html/requests.html", data)
}

// requestFormRow é uma linha de item do formulário de nova requisição,
// como a pessoa digitou (para a tela voltar preenchida se der erro).
type requestFormRow struct {
	MaterialID int
	Quantity   string
}

// requestTargetSite descobre em qual obra a requisição nova vai ser
// criada: a do usuário, ou a do seletor para quem age em todas as obras.
// Devolve a obra (ou nil) e, quando não dá para pedir ali, o motivo.
func requestTargetSite(user *models.User, scope siteScope) (*models.Site, string) {
	var site *models.Site
	if can(user, PermAllSites) {
		site = scope.Current
		if site == nil {
			return nil, "Selecione uma obra no topo para pedir material."
		}
	} else {
		if user.SiteID == 0 {
			return nil, "Você ainda não está vinculado a nenhuma obra. Peça a um administrador para definir a sua obra."
		}
		found, err := services.GetSiteByID(user.SiteID)
		if err != nil {
			return nil, "A sua obra não foi encontrada. Peça a um administrador para conferir o seu cadastro."
		}
		site = found
	}

	if site.Type == services.SiteTypeCentral {
		return site, "Requisição é feita para uma obra, não para o almoxarifado central. Selecione uma obra no topo."
	}
	if site.Status != services.SiteStatusInProgress {
		return site, fmt.Sprintf("A obra %s está %s e não aceita requisição nova.", site.Name, strings.ToLower(site.FormattedStatus))
	}
	return site, ""
}

// newRequestHandler mostra e processa o formulário de nova requisição
// (/requisicoes/nova). O JS só acrescenta e remove linhas; toda validação
// é aqui e no service.
func newRequestHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	if !requirePermission(w, user, PermCreateRequest) {
		return
	}
	scope, ok := requireSiteScope(w, r, user)
	if !ok {
		return
	}

	site, blocker := requestTargetSite(user, scope)
	siteID := 0
	if site != nil {
		siteID = site.ID
	}

	materials, err := services.ListActiveMaterialOptions(siteID)
	if err != nil {
		log.Println("erro ao listar materiais para requisição:", err)
		http.Error(w, "Erro ao carregar materiais", http.StatusInternalServerError)
		return
	}

	data := struct {
		User           *models.User
		Scope          siteScope
		Nav            navData
		CanManageUsers bool
		Site           *models.Site
		Blocker        string
		Materials      []services.ActiveMaterialOption
		Rows           []requestFormRow
		Note           string
		Error          string
		MaxItems       int
	}{
		User:           user,
		Scope:          scope,
		Nav:            buildNav(user, scope),
		CanManageUsers: can(user, PermManageUsers),
		Site:           site,
		Blocker:        blocker,
		Materials:      materials,
		Rows:           []requestFormRow{{}},
		MaxItems:       services.MaxRequestItems,
	}

	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		data.Note = r.FormValue("observacao")
		items, rows, problem := readRequestItems(r, materials)
		data.Rows = rows
		if len(data.Rows) == 0 {
			data.Rows = []requestFormRow{{}}
		}

		switch {
		case problem != "":
			data.Error = problem
		case site == nil:
			data.Error = blocker
		case !sameSiteAsForm(r, scope) && can(user, PermAllSites):
			// Admin trocou de obra em outra aba: a requisição iria para a
			// obra errada.
			data.Error = siteChangedMessage
		default:
			id, err := services.CreateRequest(requestActor(user), site.ID, data.Note, items)
			if err == nil {
				http.Redirect(w, r, fmt.Sprintf("/requisicoes/%d?sucesso=criada", id), http.StatusSeeOther)
				return
			}
			message, show := requestErrorResponse(w, r, err)
			if !show {
				return
			}
			data.Error = message
		}
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	status := http.StatusOK
	if data.Error != "" {
		status = http.StatusBadRequest
	}
	renderPage(w, status, "frontend/html/request_new.html", data)
}

// readRequestItems lê as linhas do formulário (material_id e quantidade,
// na mesma ordem). Linha toda em branco é ignorada. Devolve os itens para
// o service, as linhas como vieram (para a tela voltar preenchida) e o
// primeiro problema de digitação encontrado.
func readRequestItems(r *http.Request, materials []services.ActiveMaterialOption) ([]services.RequestItemInput, []requestFormRow, string) {
	if err := r.ParseForm(); err != nil {
		return nil, nil, "Não foi possível ler o formulário."
	}
	materialIDs := r.PostForm["material_id"]
	quantities := r.PostForm["quantidade"]
	count := len(materialIDs)
	if len(quantities) > count {
		count = len(quantities)
	}

	names := map[int]string{}
	for _, m := range materials {
		names[m.ID] = m.Name
	}

	var items []services.RequestItemInput
	var rows []requestFormRow
	problem := ""
	for i := 0; i < count; i++ {
		materialText, quantityText := "", ""
		if i < len(materialIDs) {
			materialText = strings.TrimSpace(materialIDs[i])
		}
		if i < len(quantities) {
			quantityText = strings.TrimSpace(quantities[i])
		}
		if materialText == "" && quantityText == "" {
			continue
		}

		line := len(rows) + 1
		materialID, idErr := strconv.Atoi(materialText)
		rows = append(rows, requestFormRow{MaterialID: materialID, Quantity: quantityText})
		if problem != "" {
			continue
		}

		switch {
		case materialText == "":
			problem = fmt.Sprintf("Escolha o material da linha %d.", line)
		case idErr != nil || materialID <= 0:
			problem = fmt.Sprintf("Material inválido na linha %d.", line)
		case quantityText == "":
			problem = fmt.Sprintf("Informe a quantidade da linha %d%s.", line, materialSuffix(names, materialID))
		default:
			quantity, err := utils.ParseQuantity(quantityText)
			if err != nil {
				problem = fmt.Sprintf("Quantidade inválida na linha %d%s.", line, materialSuffix(names, materialID))
				break
			}
			items = append(items, services.RequestItemInput{MaterialID: materialID, Quantity: quantity})
		}
	}
	return items, rows, problem
}

func materialSuffix(names map[int]string, materialID int) string {
	if name, ok := names[materialID]; ok {
		return " (" + name + ")"
	}
	return ""
}

// requestDetailHandler mostra a requisição (/requisicoes/{id}) e processa
// as ações: aprovar, rejeitar, cancelar e atender. Requisição fora do
// alcance responde 404, igual a uma que não existe.
func requestDetailHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	scope, ok := requireSiteScope(w, r, user)
	if !ok {
		return
	}
	actor := requestActor(user)

	request, err := services.GetRequest(actor, id)
	if err != nil {
		// GetRequest só devolve "não encontrada" (404) ou erro do banco (500).
		requestErrorResponse(w, r, err)
		return
	}

	data := struct {
		User           *models.User
		Scope          siteScope
		Nav            navData
		CanManageUsers bool
		Request        *models.Request
		Message        string
		Error          string
		Buttons        requestButtons
		// Deliveries guarda o que foi digitado no atendimento, pelo ID do
		// item, para a tela voltar preenchida se der erro.
		Deliveries map[int]string
		// RejectOpen abre o modal de rejeição (?rejeitar=1 ou erro nele).
		RejectOpen   bool
		RejectReason string
	}{
		User:           user,
		Scope:          scope,
		Nav:            buildNav(user, scope),
		CanManageUsers: can(user, PermManageUsers),
		Request:        request,
		Deliveries:     map[int]string{},
		RejectOpen:     r.URL.Query().Get("rejeitar") != "",
	}

	data.Message = map[string]string{
		"criada":    "Requisição criada. Agora ela espera a aprovação.",
		"aprovada":  "Requisição aprovada.",
		"rejeitada": "Requisição rejeitada.",
		"cancelada": "Requisição cancelada.",
		"parcial":   "Atendimento registrado. Ainda falta material nesta requisição.",
		"atendida":  "Atendimento registrado. A requisição foi atendida por completo.",
	}[r.URL.Query().Get("sucesso")]

	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		var actionErr error
		success := ""

		switch r.FormValue("acao") {
		case "aprovar":
			actionErr = services.ApproveRequest(actor, id)
			success = "aprovada"
		case "rejeitar":
			data.RejectReason = r.FormValue("motivo")
			actionErr = services.RejectRequest(actor, id, data.RejectReason)
			success = "rejeitada"
			data.RejectOpen = actionErr != nil
		case "cancelar":
			actionErr = services.CancelRequest(actor, id)
			success = "cancelada"
		case "atender":
			var deliveries []services.RequestDelivery
			problem := ""
			for _, item := range request.Items {
				text := strings.TrimSpace(r.FormValue(fmt.Sprintf("entrega_%d", item.ID)))
				data.Deliveries[item.ID] = text
				if text == "" || problem != "" {
					continue
				}
				quantity, err := utils.ParseQuantity(text)
				if err != nil {
					problem = fmt.Sprintf("Quantidade entregue inválida para %s.", item.MaterialName)
					continue
				}
				deliveries = append(deliveries, services.RequestDelivery{ItemID: item.ID, Quantity: quantity})
			}
			if problem != "" {
				actionErr = services.RequestInputError{Message: problem}
				break
			}
			var status string
			status, actionErr = services.ServeRequest(actor, id, deliveries)
			success = map[string]string{services.RequestPartial: "parcial", services.RequestFulfilled: "atendida"}[status]
		default:
			http.Error(w, "Ação inválida", http.StatusBadRequest)
			return
		}

		if actionErr == nil {
			http.Redirect(w, r, fmt.Sprintf("/requisicoes/%d?sucesso=%s", id, url.QueryEscape(success)), http.StatusSeeOther)
			return
		}
		message, show := requestErrorResponse(w, r, actionErr)
		if !show {
			return
		}
		data.Error = message
		// A situação pode ter mudado (outra pessoa agiu): relê para a tela.
		if fresh, err := services.GetRequest(actor, id); err == nil {
			data.Request = fresh
		}
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	data.Buttons = buildRequestButtons(actor, data.Request)
	data.RejectOpen = data.RejectOpen && data.Buttons.Reject

	status := http.StatusOK
	if data.Error != "" {
		status = http.StatusBadRequest
	}
	renderPage(w, status, "frontend/html/request_detail.html", data)
}

// requestButtons são os botões da tela de detalhe. SiteBlocked explica
// por que aprovar e atender estão travados.
type requestButtons struct {
	Approve     bool
	Reject      bool
	Cancel      bool
	Serve       bool
	SiteBlocked string
}

// buildRequestButtons decide quais botões a tela de detalhe mostra. É só
// para não oferecer o que vai ser recusado: as regras de verdade estão no
// service, que confere tudo de novo no POST.
func buildRequestButtons(actor services.RequestActor, request *models.Request) requestButtons {
	open := request.Status == services.RequestPending || request.Status == services.RequestApproved || request.Status == services.RequestPartial
	decidable := request.Status == services.RequestPending && (request.RequesterID != actor.UserID || actor.ApproveOwn)
	siteRunning := request.SiteStatus == services.SiteStatusInProgress
	servable := request.Status == services.RequestApproved || request.Status == services.RequestPartial

	buttons := requestButtons{
		Approve: actor.CanApprove && decidable && siteRunning,
		Reject:  actor.CanApprove && decidable,
		Cancel:  open && (actor.CanApprove || (request.Status == services.RequestPending && request.RequesterID == actor.UserID)),
		Serve:   actor.CanServe && servable && siteRunning,
	}
	if open && !siteRunning {
		buttons.SiteBlocked = fmt.Sprintf("A obra %s não está em andamento: aprovar e atender ficam bloqueados até ela ser retomada. Rejeitar e cancelar continuam possíveis.", request.SiteName)
	}
	return buttons
}

// renderPage monta o template com o seletor de obra e responde com o
// status pedido. Renderiza num buffer antes: se o template falhar no
// meio, a pessoa vê o erro, e não meia página.
func renderPage(w http.ResponseWriter, status int, page string, data any) {
	tmpl, err := template.ParseFiles(page, "frontend/html/site_switcher.html")
	if err != nil {
		log.Println("erro ao carregar template:", err)
		http.Error(w, "Erro ao carregar a página", http.StatusInternalServerError)
		return
	}
	var content strings.Builder
	if err := tmpl.Execute(&content, data); err != nil {
		log.Println("erro ao renderizar template:", err)
		http.Error(w, "Erro ao renderizar a página", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(content.String()))
}
