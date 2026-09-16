package main

import (
	"testing"

	"uchoastock/backend/models"
	"uchoastock/backend/services"
)

// allPermissions lista toda permissão existente, para o teste cobrir
// cada cargo contra cada uma.
var allPermissions = []Permission{
	PermEditMaterial, PermRemoveMaterial, PermMoveStock,
	PermCreateRequest, PermApproveRequest,
	PermViewAllMovements, PermExportMovements,
	PermManageUsers, PermManageSites, PermAllSites,
}

func TestCanEveryRoleEveryPermission(t *testing.T) {
	// granted é o que cada cargo deve poder. Toda permissão que não está
	// na lista do cargo tem de ser negada.
	cases := []struct {
		role    string
		granted []Permission
	}{
		{services.RoleSuperadmin, allPermissions},
		{services.RoleAdmin, allPermissions},
		{services.RoleManager, []Permission{
			PermEditMaterial, PermRemoveMaterial, PermMoveStock,
			PermCreateRequest, PermApproveRequest,
			PermViewAllMovements, PermExportMovements,
			PermManageUsers, PermManageSites,
		}},
		{services.RoleStorekeeper, []Permission{
			PermEditMaterial, PermMoveStock, PermCreateRequest,
			PermViewAllMovements, PermExportMovements,
		}},
		{services.RoleRequester, []Permission{PermCreateRequest}},
		{services.RoleAuditor, []Permission{PermViewAllMovements, PermExportMovements}},
		// Cargos antigos e desconhecidos não podem nada: se a migração não
		// rodasse, ninguém ganharia acesso por engano.
		{"gerente", nil},
		{"basico", nil},
		{"", nil},
		{"qualquer", nil},
	}

	for _, c := range cases {
		want := map[Permission]bool{}
		for _, p := range c.granted {
			want[p] = true
		}
		user := &models.User{Role: c.role}
		for _, p := range allPermissions {
			if got := can(user, p); got != want[p] {
				t.Errorf("can(%q, %s) = %v, esperado %v", c.role, p, got, want[p])
			}
		}
	}
}

func TestCanNormalizesRole(t *testing.T) {
	if !can(&models.User{Role: "  ALMOXARIFE "}, PermMoveStock) {
		t.Error("cargo com espaços e maiúsculas deveria ser reconhecido")
	}
	if !can(&models.User{Role: "SuperAdmin"}, PermAllSites) {
		t.Error("SuperAdmin com maiúsculas deveria poder tudo")
	}
}

func TestCanNilUser(t *testing.T) {
	for _, p := range allPermissions {
		if can(nil, p) {
			t.Errorf("usuário nil não deveria ter %s", p)
		}
	}
}

// Todo cargo aceito na gestão de usuários precisa ter uma linha em
// rolePermissions; senão ele existiria no banco sem poder nada.
func TestEveryRoleHasPermissions(t *testing.T) {
	for _, role := range services.RoleLabels {
		if _, ok := rolePermissions[role.Value]; !ok {
			t.Errorf("cargo %q sem linha em rolePermissions", role.Value)
		}
	}
}
