package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"uchoastock/backend/models"
	"uchoastock/backend/services"
	"uchoastock/backend/utils"
)

// reportDefaultDays é o período que o relatório abre quando a URL não
// traz data: os últimos 30 dias, contando hoje.
const reportDefaultDays = 30

// reportMaterialsLimit é quantos materiais a tabela de consumo mostra. O
// relatório serve para decidir compra e reposição, então o que importa é
// o topo da lista; o resto está no histórico.
const reportMaterialsLimit = 15

// reportDateLayout é o formato das datas na URL e nos campos <input
// type="date">, igual ao que o SQLite compara.
const reportDateLayout = "2006-01-02"

// reportShortcut é um atalho de período da barra de abas ("7 dias").
type reportShortcut struct {
	Label  string
	From   string
	To     string
	Active bool
}

// readReportFilter lê o período do relatório da URL (?de= e ?ate=). Data
// em formato errado é descartada e vira mensagem, em vez de quebrar a
// consulta — mesma regra do histórico de movimentações.
//
// Sem nenhuma data na URL, o relatório abre nos últimos 30 dias: um
// relatório sem período seria a soma de toda a vida do sistema, que não
// responde "como foi este mês".
func readReportFilter(r *http.Request) (services.ReportFilter, string) {
	query := r.URL.Query()
	filter := services.ReportFilter{
		From: strings.TrimSpace(query.Get("de")),
		To:   strings.TrimSpace(query.Get("ate")),
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

	if filter.From == "" && filter.To == "" {
		filter.From, filter.To = lastDays(reportDefaultDays)
	}

	return filter, problem
}

// lastDays devolve o período dos últimos days dias contando hoje, no
// formato da URL.
func lastDays(days int) (from string, to string) {
	today := time.Now().Local()
	return today.AddDate(0, 0, -(days - 1)).Format(reportDateLayout), today.Format(reportDateLayout)
}

// reportShortcuts monta os atalhos de período, marcando como ativo o que
// bate com o período que está na tela.
func reportShortcuts(filter services.ReportFilter) []reportShortcut {
	var shortcuts []reportShortcut
	for _, days := range []int{7, 30, 90} {
		from, to := lastDays(days)
		shortcuts = append(shortcuts, reportShortcut{
			Label:  fmt.Sprintf("%d dias", days),
			From:   from,
			To:     to,
			Active: filter.From == from && filter.To == to,
		})
	}
	return shortcuts
}

// formatReportDate escreve a data da URL no padrão brasileiro, para o
// texto do cabeçalho. Data vazia ou inválida devolve "".
func formatReportDate(date string) string {
	parsed, err := time.Parse(reportDateLayout, date)
	if err != nil {
		return ""
	}
	return parsed.Format("02/01/2006")
}

// reportHandler monta o relatório de consumo do período.
//
// A permissão é a de ver as movimentações de todos: o relatório é a soma
// do que a obra inteira movimentou. Quem só pode ver as próprias
// movimentações (solicitante) enxergaria, pelo total, o que os outros
// fizeram — por isso a aba nem aparece para ele.
func reportHandler(w http.ResponseWriter, r *http.Request, user *models.User) {
	if !requirePermission(w, user, PermViewAllMovements) {
		return
	}

	scope, ok := requireSiteScope(w, r, user)
	if !ok {
		return
	}

	filter, problem := readReportFilter(r)
	filter.SiteID = scope.SiteID()

	summary, err := services.GetReportSummary(filter)
	if err != nil {
		log.Println("erro ao resumir o relatório:", err)
		http.Error(w, "Erro ao carregar o relatório", http.StatusInternalServerError)
		return
	}

	materials, err := services.GetMaterialConsumption(filter, reportMaterialsLimit)
	if err != nil {
		log.Println("erro ao calcular o consumo por material:", err)
		http.Error(w, "Erro ao carregar o relatório", http.StatusInternalServerError)
		return
	}

	// O comparativo só faz sentido na visão de todas as obras: com uma
	// obra escolhida, seria uma tabela de uma linha só.
	var sites []services.SiteConsumption
	if scope.Current == nil {
		if sites, err = services.GetSiteConsumption(filter); err != nil {
			log.Println("erro ao comparar as obras:", err)
			http.Error(w, "Erro ao carregar o relatório", http.StatusInternalServerError)
			return
		}
	}

	extra := summary.Materials - len(materials)
	if extra < 0 {
		extra = 0
	}

	data := struct {
		User      *models.User
		Scope     siteScope
		Summary   services.ReportSummary
		Materials []services.MaterialConsumption
		Sites     []services.SiteConsumption
		// MaterialsExtra é quantos materiais ficaram fora do corte da
		// tabela, para a linha "e mais N".
		MaterialsExtra int
		Shortcuts      []reportShortcut
		From           string
		To             string
		FormattedFrom  string
		FormattedTo    string
		CanManageUsers bool
		// Nav são os contadores da barra lateral.
		Nav   navData
		Error string
	}{
		User:           user,
		Scope:          scope,
		Summary:        summary,
		Materials:      materials,
		Sites:          sites,
		MaterialsExtra: extra,
		Shortcuts:      reportShortcuts(filter),
		From:           filter.From,
		To:             filter.To,
		FormattedFrom:  formatReportDate(filter.From),
		FormattedTo:    formatReportDate(filter.To),
		CanManageUsers: can(user, PermManageUsers),
		Nav:            buildNav(user, scope),
		Error:          problem,
	}

	renderPage(w, http.StatusOK, "frontend/html/reports.html", data)
}
