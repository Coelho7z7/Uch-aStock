package main

import (
	"strings"
	"testing"

	"uchoastock/backend/models"
	"uchoastock/backend/services"
)

// allPermissions lista toda permissão existente, para o teste cobrir
// cada cargo contra cada uma.
var allPermissions = []Permission{
	PermEditMaterial, PermRemoveMaterial, PermMoveStock,
	PermCreateRequest, PermApproveRequest, PermServeRequest, PermViewAllRequests, PermApproveOwnRequest,
	PermViewAllMovements, PermExportMovements,
	PermManageUsers, PermManageSites, PermReopenSite, PermAllSites,
}

// adminPermissions é tudo menos aprovar a própria solicitação, que fica só
// com o superadmin.
var adminPermissions = []Permission{
	PermEditMaterial, PermRemoveMaterial, PermMoveStock,
	PermCreateRequest, PermApproveRequest, PermServeRequest, PermViewAllRequests,
	PermViewAllMovements, PermExportMovements,
	PermManageUsers, PermManageSites, PermReopenSite, PermAllSites,
}

func TestCanEveryRoleEveryPermission(t *testing.T) {
	// granted é o que cada cargo deve poder. Toda permissão que não está
	// na lista do cargo tem de ser negada.
	cases := []struct {
		role    string
		granted []Permission
	}{
		{services.RoleSuperadmin, allPermissions},
		{services.RoleAdmin, adminPermissions},
		{services.RoleManager, []Permission{
			PermMoveStock,
			PermCreateRequest, PermApproveRequest, PermServeRequest, PermViewAllRequests,
			PermViewAllMovements, PermExportMovements,
			PermManageUsers, PermManageSites,
		}},
		{services.RoleStorekeeper, []Permission{
			PermMoveStock,
			PermCreateRequest, PermServeRequest, PermViewAllRequests,
			PermViewAllMovements, PermExportMovements,
		}},
		{services.RoleRequester, []Permission{PermCreateRequest}},
		{services.RoleAuditor, []Permission{PermViewAllRequests, PermViewAllMovements, PermExportMovements}},
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

func TestManagerUserRules(t *testing.T) {
	admin := &models.User{ID: 1, Role: "admin"}
	manager := &models.User{ID: 2, Role: "gestor", SiteID: 7}
	storekeeper := &models.User{ID: 3, Role: "almoxarife", SiteID: 7}

	// Cargos que cada um pode dar.
	roleCases := []struct {
		actor *models.User
		role  string
		want  bool
	}{
		{admin, "admin", true},
		{admin, "gestor", true},
		{admin, "auditor", true},
		{admin, "superadmin", false},
		{manager, "almoxarife", true},
		{manager, "solicitante", true},
		{manager, " Solicitante ", true},
		{manager, "admin", false},
		{manager, "gestor", false},
		{manager, "auditor", false},
		{manager, "superadmin", false},
		{storekeeper, "solicitante", false},
		{nil, "solicitante", false},
	}
	for _, c := range roleCases {
		role := "nil"
		if c.actor != nil {
			role = c.actor.Role
		}
		if got := canAssignRole(c.actor, c.role); got != c.want {
			t.Errorf("canAssignRole(%s, %q) = %v, esperado %v", role, c.role, got, c.want)
		}
	}

	var labels []string
	for _, r := range assignableRoles(manager) {
		labels = append(labels, r.Label)
	}
	if got := strings.Join(labels, ","); got != "Almoxarife,Solicitante" {
		t.Errorf("dropdown do gestor = %q, esperado Almoxarife,Solicitante", got)
	}
	if got := len(assignableRoles(admin)); got != len(services.RoleLabels) {
		t.Errorf("dropdown do admin com %d cargos, esperado %d", got, len(services.RoleLabels))
	}
	if got := assignableRoles(storekeeper); len(got) != 0 {
		t.Errorf("almoxarife não gerencia usuários, mas o dropdown veio com %v", got)
	}

	// Obra a que cada um pode vincular.
	siteCases := []struct {
		actor  *models.User
		siteID int
		want   bool
	}{
		{admin, 8, true},
		{manager, 7, true},
		{manager, 0, true},
		{manager, 8, false},
	}
	for _, c := range siteCases {
		if got := canAssignSite(c.actor, c.siteID); got != c.want {
			t.Errorf("canAssignSite(%s, %d) = %v, esperado %v", c.actor.Role, c.siteID, got, c.want)
		}
	}

	// Contas em que cada um pode mexer.
	userCases := []struct {
		name   string
		actor  *models.User
		target *models.User
		want   bool
	}{
		{"admin mexe em outro admin", admin, &models.User{Role: "admin"}, true},
		{"admin mexe em gestor de qualquer obra", admin, &models.User{Role: "gestor", SiteID: 9}, true},
		{"ninguém mexe no superadmin", admin, &models.User{Role: "superadmin"}, false},
		{"gestor mexe em almoxarife da obra dele", manager, &models.User{Role: "almoxarife", SiteID: 7}, true},
		{"gestor mexe em solicitante sem obra", manager, &models.User{Role: "solicitante"}, true},
		{"gestor não mexe em almoxarife de outra obra", manager, &models.User{Role: "almoxarife", SiteID: 8}, false},
		{"gestor não mexe em admin", manager, &models.User{Role: "admin"}, false},
		{"gestor não mexe em outro gestor", manager, &models.User{Role: "gestor", SiteID: 7}, false},
		{"gestor não mexe em si mesmo", manager, manager, false},
		{"gestor não mexe em auditor", manager, &models.User{Role: "auditor", SiteID: 7}, false},
		{"gestor não mexe no superadmin", manager, &models.User{Role: "superadmin"}, false},
		{"almoxarife não mexe em ninguém", storekeeper, &models.User{Role: "solicitante", SiteID: 7}, false},
		{"alvo nil", admin, nil, false},
	}
	for _, c := range userCases {
		if got := canManageUser(c.actor, c.target); got != c.want {
			t.Errorf("%s: canManageUser = %v, esperado %v", c.name, got, c.want)
		}
	}
}

// TestRequestActorFlags confere a tradução de cada cargo para as flags do
// service de solicitações, inclusive superadmin e cargo desconhecido.
func TestRequestActorFlags(t *testing.T) {
	cases := []struct {
		role string
		want services.RequestActor
	}{
		{services.RoleSuperadmin, services.RequestActor{AllSites: true, ViewAll: true, CanCreate: true, CanApprove: true, CanServe: true, ApproveOwn: true}},
		{services.RoleAdmin, services.RequestActor{AllSites: true, ViewAll: true, CanCreate: true, CanApprove: true, CanServe: true}},
		{services.RoleManager, services.RequestActor{ViewAll: true, CanCreate: true, CanApprove: true, CanServe: true}},
		{services.RoleStorekeeper, services.RequestActor{ViewAll: true, CanCreate: true, CanServe: true}},
		{services.RoleRequester, services.RequestActor{CanCreate: true}},
		{services.RoleAuditor, services.RequestActor{ViewAll: true}},
		{"qualquer", services.RequestActor{}},
	}
	for _, c := range cases {
		user := &models.User{ID: 42, SiteID: 7, Role: c.role}
		c.want.UserID, c.want.SiteID = 42, 7
		if got := requestActor(user); got != c.want {
			t.Errorf("requestActor(%s) = %+v, esperado %+v", c.role, got, c.want)
		}
	}
}
