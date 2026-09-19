package services

import (
	"errors"
	"strings"

	database "uchoastock/backend/database"
	"uchoastock/backend/utils"

	"golang.org/x/crypto/bcrypt"
)

// ResetUserPasswordWeb troca a senha de um usuário ativo a partir do id,
// para a tela de usuários. Recebe id, e não email, porque é o que a
// tabela tem em mãos; o ResetPassword por email continua servindo à
// linha de comando.
//
// requesterID é quem está pedindo a troca, e existe por causa da
// identidade reservada: o SuperAdmin só pode ter a senha trocada por ele
// mesmo. Deixar um admin redefini-la permitiria que ele agisse como
// SuperAdmin, e o histórico de movimentações, que é registrado por
// usuário, atribuiria a ele o que outra pessoa fez.
//
// A troca derruba as sessões abertas da conta: quem entrou com a senha
// antiga perde o acesso na hora, e não só quando a sessão vencesse (até
// 30 dias depois). keepTokenHash é a sessão de quem pede, que fica de pé
// quando a pessoa troca a própria senha ("" derruba todas).
func ResetUserPasswordWeb(targetID, requesterID int, password, keepTokenHash string) error {
	if !utils.ValidatePassword(password) {
		return errors.New("a senha deve ter no mínimo 6 caracteres e 1 caractere especial")
	}

	var role string
	if err := database.DB.QueryRow(`
		SELECT role FROM usuarios WHERE id = ? AND ativo = 1
	`, targetID).Scan(&role); err != nil {
		return errors.New("nenhum usuário ativo encontrado")
	}

	if role == "superadmin" && targetID != requesterID {
		return errors.New("a senha do SuperAdmin só pode ser trocada por ele mesmo")
	}

	// A sessão de quem pede só fica quando a conta é a dele: ao trocar a
	// senha de outra pessoa, todas as sessões dela caem.
	if targetID != requesterID {
		keepTokenHash = ""
	}
	return setPasswordByID(targetID, password, keepTokenHash)
}

// ChangeOwnPassword troca a senha da própria conta, conferindo a senha
// atual antes. Pedir a senha atual protege quem esqueceu a sessão aberta
// num computador da obra: sem ela, qualquer um que sentasse ali trocaria
// a senha e tomaria a conta. As outras sessões da conta caem; a atual
// (keepTokenHash) continua.
func ChangeOwnPassword(userID int, current, password, keepTokenHash string) error {
	var hash string
	if err := database.DB.QueryRow(`
		SELECT senha FROM usuarios WHERE id = ? AND ativo = 1
	`, userID).Scan(&hash); err != nil {
		return errors.New("nenhum usuário ativo encontrado")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return errors.New("a senha atual não confere")
	}
	if current == password {
		return errors.New("a senha nova precisa ser diferente da atual")
	}
	if !utils.ValidatePassword(password) {
		return errors.New("a senha deve ter no mínimo 6 caracteres e 1 caractere especial")
	}
	return setPasswordByID(userID, password, keepTokenHash)
}

// setPasswordByID grava a senha nova de uma conta ativa e derruba as
// sessões dela (menos keepTokenHash), numa transação: ou as duas coisas
// acontecem, ou nenhuma.
func setPasswordByID(userID int, password, keepTokenHash string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return errors.New("erro ao proteger a senha")
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return errors.New("erro ao atualizar a senha")
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		UPDATE usuarios SET senha = ? WHERE id = ? AND ativo = 1
	`, string(hash), userID)
	if err != nil {
		return errors.New("erro ao atualizar a senha")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return errors.New("erro ao atualizar a senha")
	}
	if rows == 0 {
		return errors.New("nenhum usuário ativo encontrado")
	}

	if err := deleteUserSessionsTx(tx, userID, keepTokenHash); err != nil {
		return errors.New("erro ao atualizar a senha")
	}
	if err := tx.Commit(); err != nil {
		return errors.New("erro ao atualizar a senha")
	}
	return nil
}

// ResetPassword troca a senha de uma conta ativa já existente, pelo email.
// Usado pelo comando de linha de comando "reset-password": ainda não existe
// uma tela no painel para trocar a senha de um usuário já criado (só na
// criação e no seed inicial), então isso cobre essa operação enquanto essa
// tela não existe.
func ResetPassword(email, password string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if !utils.ValidateEmail(email) {
		return errors.New("email inválido")
	}
	if !utils.ValidatePassword(password) {
		return errors.New("a senha deve ter no mínimo 6 caracteres e 1 caractere especial")
	}

	var userID int
	if err := database.DB.QueryRow(`
		SELECT id FROM usuarios WHERE LOWER(TRIM(email)) = ? AND ativo = 1
	`, email).Scan(&userID); err != nil {
		return errors.New("nenhum usuário ativo encontrado com esse email")
	}

	// Pela linha de comando não há sessão de quem pede: todas caem.
	return setPasswordByID(userID, password, "")
}

// RenameUser troca o nome de exibição de uma conta ativa já existente,
// pelo email. Usado pelo comando de linha de comando "rename-user" pelo
// mesmo motivo do ResetPassword: o seed só define o nome na criação da
// conta, nunca em contas que já existem.
func RenameUser(email, name string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)

	if !utils.ValidateEmail(email) {
		return errors.New("email inválido")
	}
	if name == "" {
		return errors.New("informe o novo nome")
	}

	result, err := database.DB.Exec(`
		UPDATE usuarios SET nome = ?
		WHERE LOWER(TRIM(email)) = ? AND ativo = 1
	`, name, email)
	if err != nil {
		return errors.New("erro ao atualizar o nome")
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return errors.New("erro ao atualizar o nome")
	}
	if rows == 0 {
		return errors.New("nenhum usuário ativo encontrado com esse email")
	}

	return nil
}

// ChangeUserEmail troca o email de uma conta ativa já existente. Usado
// pelo comando "change-email" pelo mesmo motivo do ResetPassword: o seed
// só define o email na criação da conta. O email superadmin@gmail.com é
// reservado e não pode ser assumido por outra conta por aqui.
func ChangeUserEmail(currentEmail, newEmail string) error {
	currentEmail = strings.ToLower(strings.TrimSpace(currentEmail))
	newEmail = strings.ToLower(strings.TrimSpace(newEmail))

	if !utils.ValidateEmail(currentEmail) || !utils.ValidateEmail(newEmail) {
		return errors.New("email inválido")
	}
	if newEmail == "superadmin@gmail.com" {
		return errors.New("o email superadmin@gmail.com é reservado ao SuperAdmin")
	}

	result, err := database.DB.Exec(`
		UPDATE usuarios SET email = ?
		WHERE LOWER(TRIM(email)) = ? AND ativo = 1
		AND NOT EXISTS (SELECT 1 FROM usuarios WHERE LOWER(TRIM(email)) = ?)
	`, newEmail, currentEmail, newEmail)
	if err != nil {
		return errors.New("erro ao atualizar o email")
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return errors.New("erro ao atualizar o email")
	}
	if rows == 0 {
		return errors.New("nenhum usuário ativo encontrado com esse email, ou o novo email já está em uso")
	}

	return nil
}
