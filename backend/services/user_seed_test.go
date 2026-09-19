package services

import (
	"testing"

	database "uchoastock/backend/database"
)

// Sem a variável de senha, a conta padrão não é criada (e nenhuma senha
// vai para o log); com ela, é criada com essa senha.
func TestSeedCreatesOnlyAccountsWithPassword(t *testing.T) {
	setupTestDB(t)
	for _, envVar := range []string{
		"SEED_SUPERADMIN_PASSWORD", "SEED_ADMIN_PASSWORD", "SEED_GESTOR_PASSWORD",
		"SEED_ALMOXARIFE_PASSWORD", "SEED_SOLICITANTE_PASSWORD", "SEED_AUDITOR_PASSWORD",
		"SEED_USUARIO_PASSWORD", superadminResetEnvVar,
	} {
		t.Setenv(envVar, "")
	}
	t.Setenv("SEED_ADMIN_PASSWORD", "admin!123")

	if err := SeedDefaultUsers(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var seeded int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM usuarios WHERE email LIKE '%@gmail.com' AND email <> 'teste@gmail.com'`).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	if seeded != 1 {
		t.Errorf("%d contas padrão criadas, esperado 1 (só a que tem senha definida)", seeded)
	}
	if _, ok := AuthenticateUser("admin@gmail.com", "admin!123"); !ok {
		t.Error("a conta admin@gmail.com não entra com a senha da variável")
	}
}
