package main

import (
	"testing"

	"uchoastock/backend/models"
)

func TestCanManageSite(t *testing.T) {
	cases := []struct {
		name   string
		user   models.User
		siteID int
		want   bool
	}{
		{"admin em qualquer obra", models.User{Role: "admin"}, 7, true},
		{"superadmin em qualquer obra", models.User{Role: "superadmin"}, 7, true},
		{"admin em Todas as obras", models.User{Role: "admin"}, 0, false},
		{"gerente na obra dele", models.User{Role: "gerente", SiteID: 7}, 7, true},
		{"gerente em outra obra", models.User{Role: "gerente", SiteID: 7}, 8, false},
		{"gerente sem obra", models.User{Role: "gerente"}, 7, false},
		{"gerente sem obra em Todas", models.User{Role: "gerente"}, 0, false},
		{"básico na obra dele", models.User{Role: "basico", SiteID: 7}, 7, false},
	}
	for _, c := range cases {
		if got := canManageSite(&c.user, c.siteID); got != c.want {
			t.Errorf("%s: canManageSite = %v, esperado %v", c.name, got, c.want)
		}
	}
}
