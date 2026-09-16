package main

import (
	"bytes"
	"encoding/csv"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"uchoastock/backend/models"
	"uchoastock/backend/services"
	"uchoastock/backend/utils"
)

// movementsPerPage é quantas linhas do histórico cabem em cada página.
const movementsPerPage = 20

// readMovementFilter lê os filtros do histórico da URL: ?tipo=,
// ?material=, ?de= e ?ate=. Data em formato errado é descartada e vira
// uma mensagem de erro, em vez de quebrar a consulta.
func readMovementFilter(r *http.Request) (services.MovementFilter, string) {
	query := r.URL.Query()
	filter := services.MovementFilter{
		Type:     strings.ToUpper(strings.TrimSpace(query.Get("tipo"))),
		Material: strings.TrimSpace(query.Get("material")),
		From:     strings.TrimSpace(query.Get("de")),
		To:       strings.TrimSpace(query.Get("ate")),
	}

	if !services.IsValidMovementType(filter.Type) {
		filter.Type = ""
	}

	var problem string
	if filter.From != "" && !utils.ValidateDate(filter.From) {
		filter.From = ""
		problem = "Data inicial inválida."
	}
	if filter.To != "" && !utils.ValidateDate(filter.To) {
		filter.To = ""
		problem = "Data final inválida."
	}
	// Datas AAAA-MM-DD comparadas como texto ficam na ordem certa.
	if filter.From != "" && filter.To != "" && filter.From > filter.To {
		problem = "A data inicial é depois da data final."
	}

	return filter, problem
}

// movementHandler exibe o histórico de entradas/saídas/atualizações.
func movementHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	scope, ok := requireSiteScope(w, r, user)
	if !ok {
		return
	}

	filter, problem := readMovementFilter(r)
	filter.SiteID = scope.SiteID()

	movements, err := services.GetMovementsFilteredWeb(filter)
	if err != nil {
		log.Println("erro ao buscar movimentações:", err)
		http.Error(w, "Erro ao buscar movimentações", http.StatusInternalServerError)
		return
	}

	totalFiltered := len(movements)

	page, _ := strconv.Atoi(r.URL.Query().Get("pagina"))
	if page < 1 {
		page = 1
	}
	totalPages := (len(movements) + movementsPerPage - 1) / movementsPerPage
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * movementsPerPage
	end := start + movementsPerPage
	if end > len(movements) {
		end = len(movements)
	}
	movements = movements[start:end]

	tmpl, err := template.ParseFiles("frontend/html/movements.html", "frontend/html/site_switcher.html")
	if err != nil {
		http.Error(w, "Erro ao carregar movimentações", http.StatusInternalServerError)
		return
	}

	data := struct {
		User           *models.User
		Scope          siteScope
		Movements      []models.Movement
		Page           int
		TotalPages     int
		PreviousPage   int
		NextPage       int
		IsAdmin        bool
		Filter         string
		MaterialSearch string
		From           string
		To             string
		Total          int
		Error          string
	}{
		User:           user,
		Scope:          scope,
		Movements:      movements,
		Page:           page,
		TotalPages:     totalPages,
		PreviousPage:   page - 1,
		NextPage:       page + 1,
		IsAdmin:        canViewUsersTab(r),
		Filter:         filter.Type,
		MaterialSearch: filter.Material,
		From:           filter.From,
		To:             filter.To,
		Total:          totalFiltered,
		Error:          problem,
	}

	var content bytes.Buffer
	if err := tmpl.Execute(&content, data); err != nil {
		log.Println("erro ao renderizar movimentações:", err)
		http.Error(w, "Erro ao renderizar movimentações", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(content.Bytes())
}

// movementExportHandler baixa o histórico filtrado em CSV, para abrir no
// Excel do escritório. Usa ";" como separador e começa com o BOM do
// UTF-8 (os bytes EF BB BF): é assim que o Excel em português separa as colunas e
// mostra os acentos sem pedir importação manual.
func movementExportHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	scope, ok := requireSiteScope(w, r, user)
	if !ok {
		return
	}

	// O arquivo traz o mesmo recorte da tela: a obra selecionada e os filtros.
	filter, _ := readMovementFilter(r)
	filter.SiteID = scope.SiteID()

	movements, err := services.GetMovementsFilteredWeb(filter)
	if err != nil {
		log.Println("erro ao exportar movimentações:", err)
		http.Error(w, "Erro ao exportar movimentações", http.StatusInternalServerError)
		return
	}

	filename := "movimentacoes-" + time.Now().Local().Format("2006-01-02") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

	writer := csv.NewWriter(w)
	writer.Comma = ';'

	_ = writer.Write([]string{"Data", "Hora", "Obra", "Tipo", "Material", "Quantidade", "Unidade", "Usuário", "Observação"})
	for _, movement := range movements {
		quantity, unit := movement.FormattedQuantity, movement.Unit
		if movement.Type == "ATUALIZACAO" {
			quantity, unit = "", ""
		}
		_ = writer.Write([]string{
			movement.FormattedDate,
			movement.FormattedTime,
			csvSafe(movement.SiteName),
			movement.FormattedType,
			csvSafe(movement.Material),
			quantity,
			unit,
			csvSafe(movement.User),
			csvSafe(movement.Note),
		})
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		log.Println("erro ao escrever CSV:", err)
	}
}

// csvSafe impede que um texto digitado vire fórmula no Excel. Célula que
// começa com = + - @ é executada como fórmula ao abrir o arquivo — um
// nome de material como "=HIPERLINK(...)" viraria um link malicioso. O
// apóstrofo na frente faz o Excel tratar como texto comum.
func csvSafe(text string) string {
	if text != "" && strings.ContainsRune("=+-@\t\r", rune(text[0])) {
		return "'" + text
	}
	return text
}
