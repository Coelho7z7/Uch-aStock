package main

import (
	"testing"

	"uchoastock/backend/models"
)

func TestSitePermissions(t *testing.T) {
	cases := []struct {
		name     string
		user     models.User
		siteID   int
		wantMove bool
		wantEdit bool
	}{
		{"admin em qualquer obra", models.User{Role: "admin"}, 7, true, true},
		{"superadmin em qualquer obra", models.User{Role: "superadmin"}, 7, true, true},
		{"admin em Todas as obras", models.User{Role: "admin"}, 0, false, false},
		{"gestor na obra dele", models.User{Role: "gestor", SiteID: 7}, 7, true, true},
		{"gestor em outra obra", models.User{Role: "gestor", SiteID: 7}, 8, false, false},
		{"gestor sem obra", models.User{Role: "gestor"}, 7, false, false},
		{"gestor sem obra em Todas", models.User{Role: "gestor"}, 0, false, false},
		{"almoxarife na obra dele", models.User{Role: "almoxarife", SiteID: 7}, 7, true, false},
		{"almoxarife em outra obra", models.User{Role: "almoxarife", SiteID: 7}, 8, false, false},
		{"solicitante na obra dele", models.User{Role: "solicitante", SiteID: 7}, 7, false, false},
		{"auditor na obra dele", models.User{Role: "auditor", SiteID: 7}, 7, false, false},
		{"cargo antigo gerente", models.User{Role: "gerente", SiteID: 7}, 7, false, false},
	}
	for _, c := range cases {
		if got := canMoveStockAt(&c.user, c.siteID); got != c.wantMove {
			t.Errorf("%s: canMoveStockAt = %v, esperado %v", c.name, got, c.wantMove)
		}
		if got := canEditSite(&c.user, c.siteID); got != c.wantEdit {
			t.Errorf("%s: canEditSite = %v, esperado %v", c.name, got, c.wantEdit)
		}
	}
	if canActOnSite(nil, 7) {
		t.Error("usuário nil não deveria agir em obra nenhuma")
	}
}
