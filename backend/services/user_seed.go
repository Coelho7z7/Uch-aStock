package services

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"

	database "uchoastock/backend/database"
	"uchoastock/backend/utils"

	"golang.org/x/crypto/bcrypt"
)

// seedPassword lê a senha padrão de uma variável de ambiente; se ela não
// estiver definida, gera uma senha aleatória (nunca fica hardcoded no
// código-fonte nem versionada no git). generated indica esse segundo
// caso, para a senha só ir para o log quando a conta for criada de
// verdade.
func seedPassword(envVar string) (password string, generated bool, err error) {
	if pw := os.Getenv(envVar); pw != "" {
		return pw, false, nil
	}

	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", false, fmt.Errorf("gerar senha aleatória (%s): %w", envVar, err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), true, nil
}

// superadminResetEnvVar é a saída de emergência da identidade reservada.
//
// Por que ela existe: a conta superadmin@gmail.com é a única que ninguém
// pode redefinir pelo painel, e isso é de propósito — um admin que
// pudesse trocar a senha dela passaria a agir como SuperAdmin, e o
// histórico de movimentações, que é registrado por usuário, atribuiria a
// ele o que outra pessoa fez. O efeito colateral é que, se essa conta
// perder a senha, não sobra por onde recuperá-la sem acesso ao servidor.
//
// Com esta variável definida, a inicialização regrava a senha dela. Não
// é uma porta nova: quem consegue definir uma variável de ambiente já
// controla o deploy e conseguiria o mesmo por outros caminhos, só que
// com mais trabalho.
const superadminResetEnvVar = "SUPERADMIN_RESET_PASSWORD"

// resetSuperadminPassword regrava a senha da identidade reservada quando
// a variável de emergência está definida, e não faz nada quando ela não
// está.
//
// Valor inválido não derruba a inicialização: o sistema continua no ar
// para todo mundo e o motivo fica explicado no log. Parar o servidor por
// causa de uma variável mal digitada trocaria o problema de uma conta
// pelo problema de todas.
func resetSuperadminPassword() error {
	password := os.Getenv(superadminResetEnvVar)
	if password == "" {
		return nil
	}

	if !utils.ValidatePassword(password) {
		fmt.Printf("[seed] %s tem valor inválido: a senha precisa de no mínimo 6 caracteres e 1 caractere especial. A senha do SuperAdmin NÃO foi alterada.\n", superadminResetEnvVar)
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("gerar senha do SuperAdmin: %w", err)
	}

	result, err := database.DB.Exec(`
		UPDATE usuarios SET senha = ?
		WHERE LOWER(TRIM(email)) = 'superadmin@gmail.com' AND ativo = 1
	`, string(hash))
	if err != nil {
		return fmt.Errorf("redefinir senha do SuperAdmin: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("redefinir senha do SuperAdmin: %w", err)
	}
	if rows == 0 {
		fmt.Printf("[seed] %s está definida, mas não há conta ativa com o email superadmin@gmail.com. Nada foi alterado.\n", superadminResetEnvVar)
		return nil
	}

	// O aviso é obrigatório: enquanto a variável existir, a senha volta a
	// esse valor a cada inicialização. Sem ele, uma troca de senha feita
	// depois seria desfeita no deploy seguinte, sem explicação aparente.
	fmt.Printf("[seed] senha do SuperAdmin redefinida pela variável %s. REMOVA essa variável do ambiente agora: enquanto ela estiver definida, a senha volta a esse valor a cada inicialização.\n", superadminResetEnvVar)
	return nil
}

// SeedDefaultUsers cria as contas padrão que ainda não existem. Conta que
// já existe nunca tem a senha trocada — a única exceção é a identidade
// reservada quando a variável de emergência está definida, tratada no
// fim por resetSuperadminPassword.
//
// A senha temporária só é gerada e mostrada no log quando a conta é
// criada. Antes ela era impressa a cada inicialização, mesmo com a conta
// já existente — e quem lia o log tentava entrar com uma senha que nunca
// tinha sido gravada.
// A conta admin@gmail.com entrou nesta lista depois das outras. Ela já
// existia em produção como sobra da migração de email: quando ceo@ e
// admin@ conviviam, só uma podia virar superadmin@ (o email é UNIQUE), e
// a admin@ ficou para trás como conta comum, sem ninguém cuidando dela.
// Estando no seed, ela volta a existir se o banco for recriado e a senha
// dela passa a sair de uma variável, como as demais.
//
// Ela tem o mesmo poder do SuperAdmin — a autorização aceita os dois
// cargos igualmente. O que a identidade reservada tem a mais é não poder
// ser rebaixada nem removida pela tela de usuários.
func SeedDefaultUsers() error {
	// Cada cargo tem uma conta padrão, além de usuario@gmail.com, que é a
	// conta de demonstração somente-leitura (RoleBasic). bindSite diz se a
	// conta é vinculada ao "Almoxarifado central": admin e superadmin não
	// têm obra (agem em todas), os demais precisam de uma para operar.
	users := []struct {
		name     string
		email    string
		role     string
		envVar   string
		label    string
		bindSite bool
	}{
		{name: "SuperAdmin", email: "superadmin@gmail.com", role: RoleSuperadmin, envVar: "SEED_SUPERADMIN_PASSWORD", label: "SuperAdmin", bindSite: false},
		{name: "Admin", email: "admin@gmail.com", role: RoleAdmin, envVar: "SEED_ADMIN_PASSWORD", label: "Admin", bindSite: false},
		{name: "Gestor", email: "gerente@gmail.com", role: RoleManager, envVar: "SEED_GESTOR_PASSWORD", label: "Gestor", bindSite: true},
		{name: "Almoxarife", email: "almoxarife@gmail.com", role: RoleStorekeeper, envVar: "SEED_ALMOXARIFE_PASSWORD", label: "Almoxarife", bindSite: true},
		{name: "Solicitante", email: "solicitante@gmail.com", role: RoleRequester, envVar: "SEED_SOLICITANTE_PASSWORD", label: "Solicitante", bindSite: true},
		{name: "Auditor", email: "auditor@gmail.com", role: RoleAuditor, envVar: "SEED_AUDITOR_PASSWORD", label: "Auditor", bindSite: true},
		{name: "Usuario", email: "usuario@gmail.com", role: RoleBasic, envVar: "SEED_USUARIO_PASSWORD", label: "Usuário (somente leitura)", bindSite: true},
	}

	// A obra central é procurada uma vez. Ela é criada em
	// database.CreateTables, que roda antes do seed, então já existe aqui.
	var centralID int
	if err := database.DB.QueryRow(`SELECT id FROM obras WHERE tipo = 'CENTRAL'`).Scan(&centralID); err != nil {
		return fmt.Errorf("localizar o almoxarifado central: %w", err)
	}

	for _, user := range users {
		var exists bool
		if err := database.DB.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM usuarios WHERE LOWER(TRIM(email)) = LOWER(TRIM(?)))
		`, user.email).Scan(&exists); err != nil {
			return err
		}

		if exists {
			// A conta SuperAdmin existente recebe somente a correção de
			// identidade/cargo; a senha dela não é sobrescrita.
			if user.email == "superadmin@gmail.com" {
				if _, err := database.DB.Exec(`
					UPDATE usuarios
					SET role = 'superadmin', nome = 'SuperAdmin'
					WHERE LOWER(TRIM(email)) = 'superadmin@gmail.com'
				`); err != nil {
					return fmt.Errorf("proteger usuário %s: %w", user.email, err)
				}
			}
			continue
		}

		password, generated, err := seedPassword(user.envVar)
		if err != nil {
			return err
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("gerar senha de %s: %w", user.email, err)
		}

		siteID := 0
		if user.bindSite {
			siteID = centralID
		}
		if err := createSeedUser(user.name, user.email, user.role, string(hash), siteID); err != nil {
			return fmt.Errorf("inserir usuário %s: %w", user.email, err)
		}

		if generated {
			fmt.Printf("[seed] %s: variável %s não definida; conta criada com a senha temporária: %s\n", user.label, user.envVar, password)
		}
	}

	return resetSuperadminPassword()
}

// createSeedUser insere a conta e vincula a obra dentro de uma única
// transação: ou os dois passos entram, ou nenhum. Assim uma conta
// vinculada a uma obra nunca fica pela metade (usuário sem obra ou obra
// apontando para um usuário que não gravou). setUserSiteTx ignora a obra
// quando o cargo é admin ou quando siteID é 0.
func createSeedUser(name, email, role, hash string, siteID int) error {
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		INSERT INTO usuarios (nome, email, senha, role)
		VALUES (?, ?, ?, ?)
	`, name, email, hash, role)
	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	if err := setUserSiteTx(tx, int(id), role, siteID); err != nil {
		return err
	}

	return tx.Commit()
}
