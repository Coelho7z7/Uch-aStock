package main

import (
	"strings"

	"uchoastock/backend/models"
	"uchoastock/backend/services"
)

// Permission é uma ação do sistema que um cargo pode ou não fazer. O
// valor segue o formato "area.acao", em português, como as rotas.
type Permission string

const (
	// PermEditMaterial é cadastrar material e editar nome, unidade e limite
	// mínimo. O catálogo é um só para a empresa (o limite mínimo também é
	// um campo único do material), por isso só o administrador tem.
	PermEditMaterial   Permission = "material.editar"
	PermRemoveMaterial Permission = "material.remover"
	PermMoveStock      Permission = "estoque.movimentar"
	// PermCreateRequest é pedir material (criar solicitação) na própria obra.
	PermCreateRequest Permission = "solicitacao.criar"
	// PermApproveRequest é aprovar, rejeitar e cancelar solicitação (e
	// cancelar mesmo depois de aprovada).
	PermApproveRequest Permission = "solicitacao.aprovar"
	// PermServeRequest é atender solicitação aprovada: entregar o material,
	// o que gera as saídas de estoque.
	PermServeRequest Permission = "solicitacao.atender"
	// PermViewAllRequests é ver solicitações de todos os solicitantes da
	// obra. Sem ela, só as próprias.
	PermViewAllRequests Permission = "solicitacao.ver_todas"
	// PermApproveOwnRequest é aprovar ou rejeitar a própria solicitação.
	// Nenhum cargo recebe: só o superadmin, porque can() libera tudo para
	// ele. Assim a exceção existe sem checar nome de cargo.
	PermApproveOwnRequest Permission = "solicitacao.aprovar_propria"

	PermViewAllMovements Permission = "movimentacoes.ver_todas"
	PermExportMovements  Permission = "movimentacoes.exportar"
	PermManageUsers      Permission = "usuarios.gerenciar"
	// PermManageSites é cadastrar, editar, paralisar, retomar e encerrar
	// obra. Quem não tem PermAllSites só mexe na própria obra.
	PermManageSites Permission = "obras.gerenciar"
	// PermReopenSite é tirar uma obra de concluída (reabrir).
	PermReopenSite Permission = "obras.reabrir"
	// PermAllSites é agir em qualquer obra e usar "Todas as obras" no
	// seletor. Sem ela, as ações que dependem de obra (movimentar estoque,
	// editar obra) valem só na obra vinculada ao usuário.
	PermAllSites Permission = "obras.todas"
)

// rolePermissions é a fonte única da verdade sobre o que cada cargo pode
// fazer. Para criar um cargo, basta uma linha nova aqui (e o cargo em
// services.validRoles). O SuperAdmin não aparece: can() libera tudo
// para ele.
//
// Ver dashboard, materiais e estoque não é permissão: todo cargo vê.
var rolePermissions = map[string][]Permission{
	services.RoleAdmin: {
		PermEditMaterial, PermRemoveMaterial, PermMoveStock,
		PermCreateRequest, PermApproveRequest, PermServeRequest, PermViewAllRequests,
		PermViewAllMovements, PermExportMovements,
		PermManageUsers, PermManageSites, PermReopenSite, PermAllSites,
	},
	// O gestor age na obra dele (sem PermAllSites) e não mexe no catálogo
	// de materiais, que é da empresa toda. Na gestão de usuários há mais
	// um limite, em manageableRoles.
	services.RoleManager: {
		PermMoveStock,
		PermCreateRequest, PermApproveRequest, PermServeRequest, PermViewAllRequests,
		PermViewAllMovements, PermExportMovements,
		PermManageUsers, PermManageSites,
	},
	services.RoleStorekeeper: {
		PermMoveStock,
		PermCreateRequest, PermServeRequest, PermViewAllRequests,
		PermViewAllMovements, PermExportMovements,
	},
	// Sem PermViewAllMovements e PermViewAllRequests, o solicitante vê só
	// as movimentações que registrou e as solicitações que criou.
	services.RoleRequester: {PermCreateRequest},
	services.RoleAuditor:   {PermViewAllRequests, PermViewAllMovements, PermExportMovements},
}

// manageableRoles limita quais cargos quem gerencia usuários pode dar e
// em quais contas pode mexer. Cargo que não está aqui (o administrador)
// não tem limite.
var manageableRoles = map[string][]string{
	services.RoleManager: {services.RoleStorekeeper, services.RoleRequester},
}

// normalizedRole devolve o cargo sem espaços e em minúsculas, como a
// comparação espera.
func normalizedRole(u *models.User) string {
	return strings.ToLower(strings.TrimSpace(u.Role))
}

// can indica se o usuário pode fazer a ação p. Usuário nil (sem sessão)
// não pode nada; o SuperAdmin pode tudo.
func can(u *models.User, p Permission) bool {
	if u == nil {
		return false
	}
	role := normalizedRole(u)
	if role == services.RoleSuperadmin {
		return true
	}
	for _, granted := range rolePermissions[role] {
		if granted == p {
			return true
		}
	}
	return false
}

// assignableRoles devolve os cargos que actor pode dar, na ordem do
// dropdown. Sem PermManageUsers, nenhum.
func assignableRoles(actor *models.User) []struct{ Value, Label string } {
	var roles []struct{ Value, Label string }
	for _, role := range services.RoleLabels {
		if canAssignRole(actor, role.Value) {
			roles = append(roles, role)
		}
	}
	return roles
}

// canAssignRole indica se actor pode dar o cargo role a alguém, no
// cadastro ou na edição. SuperAdmin nunca é dado pela tela.
func canAssignRole(actor *models.User, role string) bool {
	if !can(actor, PermManageUsers) {
		return false
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == services.RoleSuperadmin {
		return false
	}
	allowed, limited := manageableRoles[normalizedRole(actor)]
	if !limited {
		return true
	}
	for _, r := range allowed {
		if r == role {
			return true
		}
	}
	return false
}

// canAssignSite indica se actor pode vincular alguém à obra siteID. Quem
// não tem PermAllSites só vincula à própria obra (ou a nenhuma): senão um
// gestor criaria um almoxarife em outra obra e movimentaria o estoque dela
// por tabela.
func canAssignSite(actor *models.User, siteID int) bool {
	return can(actor, PermAllSites) || siteID == 0 || siteID == actor.SiteID
}

// canManageUser indica se actor pode alterar o cargo e a obra, trocar a
// senha e remover a conta target. O SuperAdmin é intocável; o gestor só
// mexe em quem tem um cargo que ele poderia dar (almoxarife e
// solicitante), e só na obra dele.
func canManageUser(actor, target *models.User) bool {
	if actor == nil || target == nil || !can(actor, PermManageUsers) {
		return false
	}
	if normalizedRole(target) == services.RoleSuperadmin {
		return false
	}
	if !canAssignRole(actor, target.Role) {
		return false
	}
	return can(actor, PermAllSites) || target.SiteID == 0 || target.SiteID == actor.SiteID
}
