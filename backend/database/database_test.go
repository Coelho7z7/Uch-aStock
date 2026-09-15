package database

import (
	"path/filepath"
	"testing"
)

// openTestDB aponta DB_PATH para um arquivo novo numa pasta temporária
// (o Go apaga a pasta sozinho no fim do teste) e conecta. Nunca toca no
// banco real.
func openTestDB(t *testing.T) {
	t.Helper()
	t.Setenv("DB_PATH", filepath.Join(t.TempDir(), "teste.db"))
	if err := Connect(); err != nil {
		t.Fatalf("conectar ao banco de teste: %v", err)
	}
	t.Cleanup(func() { DB.Close() })
}

func columnExists(t *testing.T, table, column string) bool {
	t.Helper()
	var count int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&count); err != nil {
		t.Fatalf("ler colunas de %s: %v", table, err)
	}
	return count > 0
}

func TestCreateTablesIsIdempotent(t *testing.T) {
	openTestDB(t)

	for run := 1; run <= 2; run++ {
		if err := CreateTables(); err != nil {
			t.Fatalf("execução %d falhou: %v", run, err)
		}
	}

	for _, c := range []struct{ table, column string }{
		{"produtos", "unidade"},
		{"produtos", "limite_minimo"},
		{"movimentacoes", "observacao"},
	} {
		if !columnExists(t, c.table, c.column) {
			t.Errorf("coluna %s.%s não foi criada", c.table, c.column)
		}
	}
}

// oldSchema reproduz o banco da versão com vendas e preço — o que existia
// em produção antes desta migração —, com as duas contas antigas que
// derrubaram o primeiro deploy: ceo@gmail.com e admin@gmail.com.
const oldSchema = `
CREATE TABLE produtos (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	nome TEXT NOT NULL,
	preco REAL NOT NULL,
	quantidade INTEGER NOT NULL,
	ativo INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE usuarios (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	nome TEXT NOT NULL,
	email TEXT UNIQUE NOT NULL,
	senha TEXT NOT NULL,
	role TEXT NOT NULL DEFAULT 'basico',
	ativo INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE vendas (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	produto_id INTEGER NOT NULL,
	usuario_id INTEGER NOT NULL,
	quantidade INTEGER NOT NULL,
	valor_unitario REAL NOT NULL,
	valor_total REAL NOT NULL
);
INSERT INTO produtos (nome, preco, quantidade) VALUES ('Cimento', 39.9, 40);
INSERT INTO usuarios (nome, email, senha, role) VALUES
	('CEO', 'ceo@gmail.com', 'hash-ceo', 'ceo'),
	('Admin', 'admin@gmail.com', 'hash-admin', 'admin');
INSERT INTO vendas (produto_id, usuario_id, quantidade, valor_unitario, valor_total)
VALUES (1, 1, 2, 39.9, 79.8);
`

func TestCreateTablesMigratesOldSchema(t *testing.T) {
	openTestDB(t)
	if _, err := DB.Exec(oldSchema); err != nil {
		t.Fatalf("montar banco antigo: %v", err)
	}

	if err := CreateTables(); err != nil {
		t.Fatalf("migração falhou: %v", err)
	}

	var salesTables int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'vendas'`).Scan(&salesTables); err != nil {
		t.Fatal(err)
	}
	if salesTables != 0 {
		t.Error("a tabela vendas deveria ter sido removida")
	}
	if columnExists(t, "produtos", "preco") {
		t.Error("a coluna preco deveria ter sido removida")
	}

	// O material continua lá, com os valores padrão nas colunas novas.
	var name, unit string
	var quantity, minimum float64
	if err := DB.QueryRow(`SELECT nome, quantidade, unidade, limite_minimo FROM produtos`).Scan(&name, &quantity, &unit, &minimum); err != nil {
		t.Fatalf("ler material migrado: %v", err)
	}
	if name != "Cimento" || quantity != 40 || unit != "un" || minimum != DefaultMinimumStock {
		t.Errorf("material migrado = (%s, %v, %s, %v), esperado (Cimento, 40, un, %d)", name, quantity, unit, minimum, DefaultMinimumStock)
	}

	// Só a conta do CEO vira superadmin, e com a senha que já tinha.
	var superRole, superPassword string
	if err := DB.QueryRow(`SELECT role, senha FROM usuarios WHERE email = 'superadmin@gmail.com'`).Scan(&superRole, &superPassword); err != nil {
		t.Fatalf("ler superadmin: %v", err)
	}
	if superRole != "superadmin" || superPassword != "hash-ceo" {
		t.Errorf("superadmin = (%s, %s), esperado (superadmin, hash-ceo)", superRole, superPassword)
	}

	var adminRole string
	if err := DB.QueryRow(`SELECT role FROM usuarios WHERE email = 'admin@gmail.com'`).Scan(&adminRole); err != nil {
		t.Fatalf("a conta admin@gmail.com deveria continuar existindo: %v", err)
	}
	if adminRole != "admin" {
		t.Errorf("admin@gmail.com ficou com o cargo %q, esperado admin", adminRole)
	}
}
