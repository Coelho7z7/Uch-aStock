package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	database "uchoastock/backend/database"
)

// openOldStockDB cria um banco de antes das obras, com estoque (inclusive
// fração) e movimentações dos três tipos.
func openOldStockDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "copia.db")
	t.Setenv("DB_PATH", path)
	if err := database.Connect(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.DB.Close() })

	if _, err := database.DB.Exec(`
		CREATE TABLE produtos (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			nome TEXT NOT NULL,
			quantidade INTEGER NOT NULL,
			ativo INTEGER NOT NULL DEFAULT 1
		);
		CREATE TABLE movimentacoes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			produto_id INTEGER NOT NULL,
			usuario_id INTEGER NOT NULL,
			tipo TEXT NOT NULL,
			quantidade INTEGER NOT NULL,
			data DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO produtos (nome, quantidade) VALUES ('Cimento', 40), ('Areia', 2.5), ('Brita', 0);
		INSERT INTO movimentacoes (produto_id, usuario_id, tipo, quantidade) VALUES
			(1, 1, 'ENTRADA', 50), (1, 1, 'SAIDA', 10), (2, 1, 'ATUALIZACAO', 0);
	`); err != nil {
		t.Fatal(err)
	}
	return path
}

// schemaAndData resume a estrutura e as linhas das tabelas do banco antigo.
func schemaAndData(t *testing.T) string {
	t.Helper()
	var out strings.Builder
	rows, err := database.DB.Query(`SELECT name, COALESCE(sql, '') FROM sqlite_master ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(&out, name, definition)
	}
	rows.Close()
	for _, query := range []string{
		`SELECT id, nome, quantidade FROM produtos ORDER BY id`,
		`SELECT id, produto_id, tipo, quantidade FROM movimentacoes ORDER BY id`,
	} {
		data, err := database.DB.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := data.Columns()
		for data.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := data.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(&out, values...)
		}
		data.Close()
	}
	return out.String()
}

func TestVerifyStockWithoutMigrateDoesNotTouchOldDatabase(t *testing.T) {
	openOldStockDB(t)
	before := schemaAndData(t)

	var out bytes.Buffer
	if code := verifyStock(nil, &out); code != 1 {
		t.Errorf("código de saída = %d, esperado 1\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "ainda não foi migrado") || !strings.Contains(out.String(), "--migrate") {
		t.Errorf("mensagem sem a explicação:\n%s", out.String())
	}
	if after := schemaAndData(t); after != before {
		t.Errorf("verify-stock sem --migrate alterou o banco.\nantes:\n%s\ndepois:\n%s", before, after)
	}
}

func TestVerifyStockMigrateChecksAgainstSnapshot(t *testing.T) {
	path := openOldStockDB(t)

	var out bytes.Buffer
	if code := verifyStock([]string{"--migrate"}, &out); code != 0 {
		t.Fatalf("código de saída = %d, esperado 0\n%s", code, out.String())
	}
	for _, want := range []string{"ATENÇÃO: --migrate ALTERA O BANCO", "Retrato de antes da migração: 3 material(is)", "Nenhum problema"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("saída sem %q:\n%s", want, out.String())
		}
	}

	csvData, err := os.ReadFile(path + ".antes-da-migracao.csv")
	if err != nil {
		t.Fatalf("CSV do retrato não foi salvo: %v", err)
	}
	if got := string(csvData); got != "id;nome;quantidade\n1;Cimento;40\n2;Areia;2.5\n3;Brita;0\n" {
		t.Errorf("CSV do retrato = %q", got)
	}

	migrated, err := database.StockMigrated()
	if err != nil || !migrated {
		t.Fatalf("depois de --migrate, StockMigrated = %v, %v", migrated, err)
	}

	// Já migrado e ainda sem uso: a conferência simples passa.
	out.Reset()
	if code := verifyStock(nil, &out); code != 0 {
		t.Errorf("conferência simples depois de migrar: código %d\n%s", code, out.String())
	}

	// Flag desconhecida.
	out.Reset()
	if code := verifyStock([]string{"--migrar"}, &out); code != 2 {
		t.Errorf("flag inválida: código %d, esperado 2", code)
	}
}

// Um banco antigo com uma sessão órfã (de um usuário que não existe mais):
// a migração passa, mas o verify-stock --migrate aponta a chave quebrada e
// sai com código 1.
func TestVerifyStockMigrateReportsForeignKeyViolations(t *testing.T) {
	openOldStockDB(t)
	if _, err := database.DB.Exec(`
		PRAGMA foreign_keys = OFF;
		CREATE TABLE usuarios (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			nome TEXT NOT NULL,
			email TEXT UNIQUE NOT NULL,
			senha TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'basico',
			ativo INTEGER NOT NULL DEFAULT 1
		);
		CREATE TABLE sessoes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			usuario_id INTEGER NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			expira_em DATETIME NOT NULL,
			criado_em DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (usuario_id) REFERENCES usuarios(id)
		);
		INSERT INTO sessoes (usuario_id, token_hash, expira_em) VALUES (999, 'x', '2030-01-01');
		PRAGMA foreign_keys = ON;
	`); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if code := verifyStock([]string{"--migrate"}, &out); code != 1 {
		t.Errorf("código de saída = %d, esperado 1\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "chave estrangeira quebrada: sessoes, linha 1, aponta para um registro de usuarios") {
		t.Errorf("saída sem a violação:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Nenhum problema") {
		t.Errorf("com violação, não pode dizer que não há problema:\n%s", out.String())
	}

	// --migrate de novo, já migrado: a conferência simples passa, mas a
	// violação continua lá e o código continua 1.
	out.Reset()
	if code := verifyStock([]string{"--migrate"}, &out); code != 1 || !strings.Contains(out.String(), "chave(s) estrangeira(s) quebrada(s)") {
		t.Errorf("banco já migrado com violação: código %d\n%s", code, out.String())
	}
}
