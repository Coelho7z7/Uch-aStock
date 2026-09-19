package services

import (
	"fmt"
	"testing"
	"time"
)

// resetLoginAttempts começa o teste com a contagem de tentativas vazia (ela
// é global, do processo).
func resetLoginAttempts(t *testing.T) {
	t.Helper()
	loginAttemptsMutex.Lock()
	loginAttempts = map[string]*loginAttempt{}
	loginAttemptsMutex.Unlock()
}

// releaseLock encerra a espera de uma chave, como se o tempo tivesse
// passado, sem o teste precisar dormir.
func releaseLock(t *testing.T, key string) {
	t.Helper()
	loginAttemptsMutex.Lock()
	defer loginAttemptsMutex.Unlock()
	if attempt := loginAttempts[key]; attempt != nil {
		attempt.releasedAt = time.Now().Add(-time.Second)
	}
}

func failLogins(email, ip string, times int) int {
	seconds := 0
	for i := 0; i < times; i++ {
		seconds = RegisterFailedLogin(email, ip)
	}
	return seconds
}

// Cada bloqueio seguido dobra a espera, até o teto.
func TestLoginLockGrowsWithEachLock(t *testing.T) {
	resetLoginAttempts(t)
	email := "ana@empresa.com"

	if seconds := failLogins(email, "", maxLoginAttempts-1); seconds != 0 {
		t.Fatalf("antes do limite: espera de %ds, esperado 0", seconds)
	}
	want := int(loginLockWindow / time.Second)
	for round := 1; round <= 8; round++ {
		if seconds := RegisterFailedLogin(email, ""); seconds != want {
			t.Fatalf("bloqueio %d: espera de %ds, esperado %ds", round, seconds, want)
		}
		if LoginLockSeconds(email, "") == 0 {
			t.Fatalf("bloqueio %d: o email não ficou bloqueado", round)
		}
		releaseLock(t, "email:"+email)
		failLogins(email, "", maxLoginAttempts-1)
		want = min(want*2, int(maxLoginLockWindow/time.Second))
	}

	// Um login certo zera a contagem do email.
	ClearLoginAttempts(email)
	if seconds := failLogins(email, "", maxLoginAttempts); seconds != int(loginLockWindow/time.Second) {
		t.Errorf("depois do login certo: espera de %ds, esperado a inicial", seconds)
	}
}

// Quem testa uma senha em muitas contas diferentes a partir do mesmo
// endereço é barrado pelo limite do endereço, mesmo sem errar 5 vezes em
// nenhuma conta.
func TestLoginLockPerAddress(t *testing.T) {
	resetLoginAttempts(t)
	ip := "203.0.113.7"

	for i := 0; i < maxIPLoginAttempts-1; i++ {
		if seconds := RegisterFailedLogin(fmt.Sprintf("conta%d@empresa.com", i), ip); seconds != 0 {
			t.Fatalf("tentativa %d: bloqueou antes do limite do endereço", i+1)
		}
	}
	if seconds := RegisterFailedLogin("outra@empresa.com", ip); seconds == 0 {
		t.Fatal("o endereço não foi bloqueado ao chegar no limite")
	}
	if LoginLockSeconds("nova@empresa.com", ip) == 0 {
		t.Error("o endereço bloqueado conseguiu tentar com outro email")
	}
	if LoginLockSeconds("nova@empresa.com", "198.51.100.9") != 0 {
		t.Error("outro endereço ficou bloqueado junto")
	}

	// Entrar numa conta válida não zera o limite do endereço.
	ClearLoginAttempts("outra@empresa.com")
	if LoginLockSeconds("outra@empresa.com", ip) == 0 {
		t.Error("o login certo liberou o endereço bloqueado")
	}
}

// Email inexistente e senha errada dão a mesma resposta.
func TestAuthenticateUserUnknownEmail(t *testing.T) {
	setupTestDB(t)
	if _, ok := AuthenticateUser("ninguem@empresa.com", "qualquer!1"); ok {
		t.Error("email inexistente autenticou")
	}
	if len(missingUserHash()) == 0 {
		t.Error("o hash de comparação para email inexistente não foi gerado")
	}
}
