package main

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	database "uchoastock/backend/database"
)

func TestIsHTTPS(t *testing.T) {
	cases := []struct {
		name  string
		tls   bool
		proto string
		want  bool
	}{
		{"HTTP direto", false, "", false},
		{"HTTPS direto", true, "", true},
		{"HTTPS pelo proxy do Railway", false, "https", true},
		{"HTTP pelo proxy", false, "http", false},
		{"maiúsculas", false, "HTTPS", true},
		{"lista de proxies, o primeiro vale", false, "https, http", true},
		{"lista começando por http", false, "http, https", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if c.tls {
			r.TLS = &tls.ConnectionState{}
		}
		if c.proto != "" {
			r.Header.Set("X-Forwarded-Proto", c.proto)
		}
		if got := isHTTPS(r); got != c.want {
			t.Errorf("%s: isHTTPS = %v, esperado %v", c.name, got, c.want)
		}
	}
}

// Sessão de usuário desativado não pode mais autenticar.
func TestUserFromSessionRequiresActiveUser(t *testing.T) {
	t.Setenv("DB_PATH", filepath.Join(t.TempDir(), "teste.db"))
	if err := database.Connect(); err != nil {
		t.Fatalf("conectar ao banco de teste: %v", err)
	}
	t.Cleanup(func() { database.DB.Close() })
	if err := database.CreateTables(); err != nil {
		t.Fatalf("criar tabelas: %v", err)
	}

	result, err := database.DB.Exec(`
		INSERT INTO usuarios (nome, email, senha, role)
		VALUES ('Ana', 'ana@empresa.com', 'x', 'almoxarife')
	`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()

	token, err := createSession(int(id))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	request.AddCookie(&http.Cookie{Name: "sessao", Value: token})

	if got, ok := userFromSession(request); !ok || got != int(id) {
		t.Fatalf("usuário ativo: userFromSession = (%d, %v), esperado (%d, true)", got, ok, id)
	}

	if _, err := database.DB.Exec(`UPDATE usuarios SET ativo = 0 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, ok := userFromSession(request); ok {
		t.Error("usuário desativado continuou autenticado pela sessão antiga")
	}
}
