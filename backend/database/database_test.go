package database

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
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
		{"movimentacoes", "obra_id"},
		{"sessoes", "obra_id"},
		{"saldos", "quantidade"},
		{"usuario_obras", "obra_id"},
	} {
		if !columnExists(t, c.table, c.column) {
			t.Errorf("coluna %s.%s não foi criada", c.table, c.column)
		}
	}

	// Rodou duas vezes, mas o almoxarifado central só pode existir uma.
	var centrals int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM obras WHERE tipo = 'CENTRAL'`).Scan(&centrals); err != nil {
		t.Fatalf("contar almoxarifado central: %v", err)
	}
	if centrals != 1 {
		t.Errorf("almoxarifados centrais = %d, esperado 1", centrals)
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

// TestCreateTablesMigratesOldRoles confere que gerente vira gestor e
// basico vira solicitante, uma vez só, e que um superadmin falso (email
// que não é o reservado) cai para solicitante, e não para o cargo antigo.
func TestCreateTablesMigratesOldRoles(t *testing.T) {
	openTestDB(t)
	if err := CreateTables(); err != nil {
		t.Fatalf("criar tabelas: %v", err)
	}
	if _, err := DB.Exec(`
		INSERT INTO usuarios (nome, email, senha, role) VALUES
			('Gerente', 'gerente@gmail.com', 'x', 'gerente'),
			('Básico', 'basico@gmail.com', 'x', ' Basico '),
			('Falso', 'falso@gmail.com', 'x', 'superadmin'),
			('Chefe', 'chefe@gmail.com', 'x', 'admin'),
			('Almoxarife', 'almox@gmail.com', 'x', 'almoxarife')
	`); err != nil {
		t.Fatalf("inserir usuários antigos: %v", err)
	}

	for run := 1; run <= 2; run++ {
		if err := CreateTables(); err != nil {
			t.Fatalf("execução %d falhou: %v", run, err)
		}
	}

	want := map[string]string{
		"gerente@gmail.com": "gestor",
		"basico@gmail.com":  "solicitante",
		"falso@gmail.com":   "solicitante",
		"chefe@gmail.com":   "admin",
		"almox@gmail.com":   "almoxarife",
	}
	for email, role := range want {
		var got string
		if err := DB.QueryRow(`SELECT role FROM usuarios WHERE email = ?`, email).Scan(&got); err != nil {
			t.Fatalf("ler cargo de %s: %v", email, err)
		}
		if got != role {
			t.Errorf("%s ficou com o cargo %q, esperado %q", email, got, role)
		}
	}
}

// TestMigrateStockToSites simula um banco de antes das obras, com estoque
// e histórico, e confere que tudo vai para o almoxarifado central uma
// vez só, mesmo com várias inicializações.
func TestMigrateStockToSites(t *testing.T) {
	openTestDB(t)
	if _, err := DB.Exec(`
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
		t.Fatalf("montar banco antigo: %v", err)
	}

	for run := 1; run <= 3; run++ {
		if err := CreateTables(); err != nil {
			t.Fatalf("execução %d falhou: %v", run, err)
		}
	}

	var centralID int
	if err := DB.QueryRow(`SELECT id FROM obras WHERE tipo = 'CENTRAL'`).Scan(&centralID); err != nil {
		t.Fatalf("ler central: %v", err)
	}

	rows, err := DB.Query(`SELECT p.nome, s.obra_id, s.quantidade FROM saldos s JOIN produtos p ON p.id = s.produto_id ORDER BY p.id`)
	if err != nil {
		t.Fatalf("ler saldos: %v", err)
	}
	defer rows.Close()

	want := map[string]float64{"Cimento": 40, "Areia": 2.5, "Brita": 0}
	found := 0
	for rows.Next() {
		var name string
		var siteID int
		var quantity float64
		if err := rows.Scan(&name, &siteID, &quantity); err != nil {
			t.Fatal(err)
		}
		found++
		if siteID != centralID || quantity != want[name] {
			t.Errorf("saldo de %s = %v na obra %d, esperado %v no central (%d)", name, quantity, siteID, want[name], centralID)
		}
	}
	if found != len(want) {
		t.Errorf("%d saldos criados, esperado %d (a cópia não pode repetir)", found, len(want))
	}

	var withSite, withoutSite int
	if err := DB.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN obra_id = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN obra_id IS NULL THEN 1 ELSE 0 END), 0)
		FROM movimentacoes
	`, centralID).Scan(&withSite, &withoutSite); err != nil {
		t.Fatalf("ler movimentações: %v", err)
	}
	if withSite != 2 || withoutSite != 1 {
		t.Errorf("movimentações no central = %d e sem obra = %d, esperado 2 e 1", withSite, withoutSite)
	}
}

// dumpDatabase copia, como texto, a estrutura (sqlite_master) e todas as
// linhas de todas as tabelas. Dois dumps iguais = banco igual.
func dumpDatabase(t *testing.T) string {
	t.Helper()
	var out strings.Builder
	var tables []string

	rows, err := DB.Query(`SELECT type, name, COALESCE(sql, '') FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var kind, name, definition string
		if err := rows.Scan(&kind, &name, &definition); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(&out, kind, name, definition)
		if kind == "table" {
			tables = append(tables, name)
		}
	}
	rows.Close()

	for _, table := range tables {
		data, err := DB.Query(`SELECT * FROM "` + table + `" ORDER BY rowid`)
		if err != nil {
			t.Fatalf("ler %s: %v", table, err)
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
			fmt.Fprintln(&out, table, values)
		}
		data.Close()
	}
	return out.String()
}

// TestCreateTablesRollsBackOnFailure simula uma falha em dois pontos da
// migração de um banco antigo e confere que o banco fica exatamente como
// antes — sem tabela nova, sem coluna nova, sem saldo copiado pela metade.
// A falha vem de um trigger que aborta o passo, então o código da
// migração roda sem nenhum desvio de teste. Depois, sem o trigger, a
// migração roda do zero e dá certo.
func TestCreateTablesRollsBackOnFailure(t *testing.T) {
	cases := []struct {
		name    string
		trigger string
	}{
		{
			// A migração de email mexe em usuarios depois de criar as
			// tabelas de obras e as colunas novas.
			"no meio, ao atualizar usuários",
			`CREATE TRIGGER falha BEFORE UPDATE ON usuarios BEGIN SELECT RAISE(ABORT, 'falha simulada'); END;`,
		},
		{
			// Ligar as movimentações antigas ao central é o último passo:
			// saldos já foram copiados e a coluna obra_id já existe.
			"no fim, ao ligar as movimentações ao central",
			`CREATE TRIGGER falha BEFORE UPDATE ON movimentacoes BEGIN SELECT RAISE(ABORT, 'falha simulada'); END;`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			openTestDB(t)
			if _, err := DB.Exec(oldSchema); err != nil {
				t.Fatalf("montar banco antigo: %v", err)
			}
			if _, err := DB.Exec(`
				CREATE TABLE movimentacoes (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					produto_id INTEGER NOT NULL,
					usuario_id INTEGER NOT NULL,
					tipo TEXT NOT NULL,
					quantidade INTEGER NOT NULL,
					data DATETIME DEFAULT CURRENT_TIMESTAMP
				);
				INSERT INTO movimentacoes (produto_id, usuario_id, tipo, quantidade) VALUES (1, 1, 'SAIDA', 2);
			`); err != nil {
				t.Fatal(err)
			}
			if _, err := DB.Exec(c.trigger); err != nil {
				t.Fatal(err)
			}

			before := dumpDatabase(t)
			err := CreateTables()
			if err == nil || !strings.Contains(err.Error(), "falha simulada") {
				t.Fatalf("CreateTables deveria falhar com a falha simulada, veio %v", err)
			}
			if after := dumpDatabase(t); after != before {
				t.Errorf("o banco mudou depois da falha.\nantes:\n%s\ndepois:\n%s", before, after)
			}

			// Sem o trigger, a próxima inicialização migra do zero.
			if _, err := DB.Exec(`DROP TRIGGER falha`); err != nil {
				t.Fatal(err)
			}
			if err := CreateTables(); err != nil {
				t.Fatalf("migração depois de tirar a falha: %v", err)
			}
			var balance float64
			var linked int
			if err := DB.QueryRow(`SELECT quantidade FROM saldos WHERE produto_id = 1`).Scan(&balance); err != nil {
				t.Fatalf("ler saldo migrado: %v", err)
			}
			if err := DB.QueryRow(`SELECT COUNT(*) FROM movimentacoes WHERE obra_id IS NOT NULL`).Scan(&linked); err != nil {
				t.Fatal(err)
			}
			if balance != 40 || linked != 1 {
				t.Errorf("depois de migrar de novo: saldo %v e %d movimentação ligada, esperado 40 e 1", balance, linked)
			}
		})
	}
}

// TestEveryConnectionHasForeignKeys abre uma segunda conexão ao mesmo banco
// (o sistema usa uma só, mas o database/sql pode trocá-la) e confere que
// as duas estão com chave estrangeira ligada — e que ela é de fato
// aplicada, recusando um saldo de material que não existe.
func TestEveryConnectionHasForeignKeys(t *testing.T) {
	openTestDB(t)
	if err := CreateTables(); err != nil {
		t.Fatal(err)
	}

	// Libera uma segunda conexão só neste teste.
	DB.SetMaxOpenConns(2)
	t.Cleanup(func() { DB.SetMaxOpenConns(1) })

	ctx := context.Background()
	first, err := DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	for name, conn := range map[string]*sql.Conn{"primeira": first, "segunda": second} {
		var enabled int
		if err := conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&enabled); err != nil {
			t.Fatal(err)
		}
		if enabled != 1 {
			t.Errorf("%s conexão com foreign_keys = %d, esperado 1", name, enabled)
		}
	}

	_, err = second.ExecContext(ctx, `INSERT INTO saldos (produto_id, obra_id, quantidade) VALUES (999, 999, 1)`)
	if err == nil || !strings.Contains(strings.ToUpper(err.Error()), "FOREIGN KEY") {
		t.Errorf("saldo de material inexistente na segunda conexão: erro = %v, esperado FOREIGN KEY", err)
	}
}
