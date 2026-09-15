package services

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"

	database "uchoastock/backend/database"

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

// SeedDefaultUsers cria as contas padrão que ainda não existem. Conta que
// já existe nunca tem a senha trocada.
//
// A senha temporária só é gerada e mostrada no log quando a conta é
// criada. Antes ela era impressa a cada inicialização, mesmo com a conta
// já existente — e quem lia o log tentava entrar com uma senha que nunca
// tinha sido gravada.
func SeedDefaultUsers() error {
	users := []struct {
		name   string
		email  string
		role   string
		envVar string
		label  string
	}{
		{name: "Gerente", email: "gerente@gmail.com", role: "gerente", envVar: "SEED_GERENTE_PASSWORD", label: "Matheus (gerente)"},
		{name: "SuperAdmin", email: "superadmin@gmail.com", role: "superadmin", envVar: "SEED_SUPERADMIN_PASSWORD", label: "SuperAdmin"},
		{name: "Usuario", email: "usuario@gmail.com", role: "basico", envVar: "SEED_USUARIO_PASSWORD", label: "Usuario (basico)"},
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

		if _, err := database.DB.Exec(`
			INSERT INTO usuarios (nome, email, senha, role)
			VALUES (?, ?, ?, ?)
		`, user.name, user.email, string(hash), user.role); err != nil {
			return fmt.Errorf("inserir usuário %s: %w", user.email, err)
		}

		if generated {
			fmt.Printf("[seed] %s: variável %s não definida; conta criada com a senha temporária: %s\n", user.label, user.envVar, password)
		}
	}

	return nil
}
