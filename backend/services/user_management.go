package services

import (
	"errors"
	"strings"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
	"uchoastock/backend/utils"

	"golang.org/x/crypto/bcrypt"
)

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
		SELECT id, nome, email, role
		FROM usuarios
		WHERE ativo = 1 AND (nome LIKE ? OR email LIKE ?)
		ORDER BY nome
		LIMIT ? OFFSET ?
	`, filter, filter, perPage, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	users := make([]models.User, 0, perPage)
	for rows.Next() {
		var user models.User
		if err := rows.Scan(&user.ID, &user.Name, &user.Email, &user.Role); err != nil {
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
// administração. Um gerente só pode cadastrar usuários com permissão
// "basico" — quem chama esta função decide isso antes, passando a role
// já validada (o handler força "basico" quando quem cria não é admin).
func CreateUserWeb(name, email, password, role string) error {
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

	if _, err := database.DB.Exec(`
		INSERT INTO usuarios (nome, email, senha, role, ativo)
		VALUES (?, ?, ?, ?, 1)
	`, name, email, string(hash), role); err != nil {
		return errors.New("Esse email já está cadastrado.")
	}

	return nil
}

// isSuperAdmin indica se o usuário é o SuperAdmin reservado (protegido de qualquer
// alteração de permissão ou remoção, mesmo por um administrador).
func isSuperAdmin(user *models.User) bool {
	return strings.EqualFold(strings.TrimSpace(user.Role), "superadmin") ||
		strings.EqualFold(strings.TrimSpace(user.Email), "superadmin@gmail.com")
}

// UpdateUserRoleWeb altera a permissão de um usuário.
// O cargo SuperAdmin é exclusivo do superadmin@gmail.com e nunca pode ser alterado —
// nem por outro administrador. Restrito a administradores (validado no
// handler, e o SuperAdmin também conta como administrador).
func UpdateUserRoleWeb(targetID int, newRole string) error {
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

	if _, err := database.DB.Exec(`UPDATE usuarios SET role = ? WHERE id = ?`, newRole, targetID); err != nil {
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
