package services

import (
	"database/sql"
	"errors"
	"strings"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
	"uchoastock/backend/utils"

	"golang.org/x/crypto/bcrypt"
)

// Cargos, como ficam gravados em usuarios.role. O que cada um pode fazer
// fica em backend/cmd/permissions.go.
const (
	RoleSuperadmin  = "superadmin"
	RoleAdmin       = "admin"
	RoleManager     = "gestor"
	RoleStorekeeper = "almoxarife"
	RoleRequester   = "solicitante"
	RoleAuditor     = "auditor"
)

// RoleLabels liga cada cargo ao texto da tela, na ordem do dropdown. O
// SuperAdmin fica de fora de propósito: ele não é oferecido na tela.
var RoleLabels = []struct{ Value, Label string }{
	{RoleAdmin, "Administrador"},
	{RoleManager, "Gestor"},
	{RoleStorekeeper, "Almoxarife"},
	{RoleRequester, "Solicitante"},
	{RoleAuditor, "Auditor"},
}

// validRoles concentra as permissões aceitas em toda a gestão de
// usuários, para não espalhar a mesma lista pelo arquivo inteiro.
var validRoles = map[string]bool{
	"admin":   true,
	"gerente": true,
	"basico":  true,
}

// ListPaginatedUsers retorna os usuários ativos, filtrados por
// nome/email e paginados, além do total de registros encontrados.
// Usuários removidos (ativo = 0) não aparecem na listagem.
func ListPaginatedUsers(search string, page, perPage int) ([]models.User, int, error) {
	search = strings.TrimSpace(search)
	filter := "%" + search + "%"
	offset := (page - 1) * perPage

	var total int
	if err := database.DB.QueryRow(`
		SELECT COUNT(*) FROM usuarios
		WHERE ativo = 1 AND (nome LIKE ? OR email LIKE ?)
	`, filter, filter).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := database.DB.Query(`
		SELECT u.id, u.nome, u.email, u.role, COALESCE(o.id, 0), COALESCE(o.nome, '')
		FROM usuarios u
		`+userSiteJoin+`
		WHERE u.ativo = 1 AND (u.nome LIKE ? OR u.email LIKE ?)
		ORDER BY u.nome
		LIMIT ? OFFSET ?
	`, filter, filter, perPage, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	users := make([]models.User, 0, perPage)
	for rows.Next() {
		var user models.User
		if err := rows.Scan(&user.ID, &user.Name, &user.Email, &user.Role, &user.SiteID, &user.SiteName); err != nil {
			return nil, 0, err
		}
		user.Active = true
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

// CreateUserWeb cadastra um novo usuário a partir do painel de
// administração (restrito a administradores, validado no handler) ou do
// comando create-user. siteID é a obra em que a pessoa vai atuar; 0 é
// "nenhuma". Para administrador a obra é ignorada: ele vê todas.
func CreateUserWeb(name, email, password, role string, siteID int) error {
	name = strings.TrimSpace(name)
	email = strings.ToLower(strings.TrimSpace(email))
	role = strings.TrimSpace(role)

	if !utils.ValidateName(name) {
		return errors.New("Informe o nome do usuário.")
	}
	if !utils.ValidateEmail(email) {
		return errors.New("Email inválido. Use um endereço @gmail.com.")
	}
	// O endereço do SuperAdmin é reservado e não pode ser reutilizado por outra conta.
	if strings.EqualFold(email, "superadmin@gmail.com") {
		return errors.New("O email superadmin@gmail.com é reservado ao SuperAdmin e não pode ser cadastrado.")
	}
	if !utils.ValidatePassword(password) {
		return errors.New("A senha deve ter no mínimo 6 caracteres e 1 caractere especial.")
	}
	if strings.EqualFold(role, "superadmin") {
		return errors.New("O cargo SuperAdmin é reservado exclusivamente para superadmin@gmail.com.")
	}
	if !validRoles[role] {
		return errors.New("Permissão inválida.")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return errors.New("Erro ao proteger a senha.")
	}

	// Usuário e vínculo com a obra entram juntos: se a obra for inválida,
	// o usuário também não é criado.
	tx, err := database.DB.Begin()
	if err != nil {
		return errors.New("Erro ao cadastrar usuário.")
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		INSERT INTO usuarios (nome, email, senha, role, ativo)
		VALUES (?, ?, ?, ?, 1)
	`, name, email, string(hash), role)
	if err != nil {
		return errors.New("Esse email já está cadastrado.")
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return errors.New("Erro ao cadastrar usuário.")
	}

	if err := setUserSiteTx(tx, int(userID), role, siteID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return errors.New("Erro ao cadastrar usuário.")
	}
	return nil
}

// setUserSiteTx troca a obra do usuário: apaga o vínculo antigo e grava o
// novo. É o "apagar antes" que garante uma obra só por usuário.
// Administrador fica sem vínculo, mesmo que uma obra tenha sido enviada.
func setUserSiteTx(tx *sql.Tx, userID int, role string, siteID int) error {
	if siteID < 0 {
		return errors.New("Obra inválida.")
	}
	if _, err := tx.Exec(`DELETE FROM usuario_obras WHERE usuario_id = ?`, userID); err != nil {
		return errors.New("Erro ao salvar a obra do usuário.")
	}
	if role == "admin" || siteID == 0 {
		return nil
	}

	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM obras WHERE id = ? AND ativo = 1`, siteID).Scan(&exists); err != nil {
		return errors.New("Erro ao salvar a obra do usuário.")
	}
	if exists == 0 {
		return errors.New("Obra não encontrada.")
	}

	if _, err := tx.Exec(`INSERT INTO usuario_obras (usuario_id, obra_id) VALUES (?, ?)`, userID, siteID); err != nil {
		return errors.New("Erro ao salvar a obra do usuário.")
	}
	return nil
}

// isSuperAdmin indica se o usuário é o SuperAdmin reservado (protegido de qualquer
// alteração de permissão ou remoção, mesmo por um administrador).
func isSuperAdmin(user *models.User) bool {
	return strings.EqualFold(strings.TrimSpace(user.Role), "superadmin") ||
		strings.EqualFold(strings.TrimSpace(user.Email), "superadmin@gmail.com")
}

// UpdateUserAccessWeb altera a permissão e a obra de um usuário, juntas.
// O cargo SuperAdmin é exclusivo do superadmin@gmail.com e nunca pode ser alterado —
// nem por outro administrador. Restrito a administradores (validado no
// handler, e o SuperAdmin também conta como administrador).
func UpdateUserAccessWeb(targetID int, newRole string, siteID int) error {
	newRole = strings.TrimSpace(newRole)
	if !validRoles[newRole] {
		return errors.New("Permissão inválida.")
	}

	target, err := GetUserByID(targetID)
	if err != nil {
		return errors.New("Usuário não encontrado.")
	}
	if isSuperAdmin(target) {
		return errors.New("O cargo SuperAdmin é protegido e não pode ser alterado.")
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return errors.New("Erro ao atualizar a permissão.")
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE usuarios SET role = ? WHERE id = ?`, newRole, targetID); err != nil {
		return errors.New("Erro ao atualizar a permissão.")
	}
	if err := setUserSiteTx(tx, targetID, newRole, siteID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return errors.New("Erro ao atualizar a permissão.")
	}
	return nil
}

// DeleteUserWeb remove um usuário de forma lógica (ativo = 0), em vez
// de apagar a linha de verdade. Isso evita o erro de violação de chave
// estrangeira que acontecia com usuários "antigos": eles costumam ter
// movimentações de estoque registradas em nome deles, e um DELETE
// de verdade era barrado pelo banco por causa dessas referências — só
// usuários recém-criados (sem histórico) conseguiam ser removidos. Com a
// remoção lógica, o usuário deixa de aparecer na lista e de conseguir
// logar, mas o histórico de movimentações continua íntegro.
// Restrito a administradores (validado no handler). O SuperAdmin nunca pode ser
// removido por este caminho.
func DeleteUserWeb(targetID int) error {
	target, err := GetUserByID(targetID)
	if err != nil {
		return errors.New("Usuário não encontrado.")
	}
	if isSuperAdmin(target) {
		return errors.New("O SuperAdmin é protegido e não pode ser removido.")
	}

	// Derruba as sessões ativas do usuário — isso não tem relação com
	// histórico de movimentações, então pode ser removido de verdade.
	if _, err := database.DB.Exec(`DELETE FROM sessoes WHERE usuario_id = ?`, targetID); err != nil {
		return errors.New("Erro ao remover usuário.")
	}

	result, err := database.DB.Exec(`UPDATE usuarios SET ativo = 0 WHERE id = ? AND ativo = 1`, targetID)
	if err != nil {
		return errors.New("Erro ao remover usuário.")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return errors.New("Erro ao remover usuário.")
	}
	if rows == 0 {
		return errors.New("Usuário não encontrado.")
	}

	return nil
}
