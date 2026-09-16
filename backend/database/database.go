package database

import (
	"database/sql"
	"fmt"
	"os"

	_ "gosqlite.org"
)

var DB *sql.DB

// DefaultMinimumStock é o limite de aviso padrão de um material: com
// essa quantidade ou menos, ele aparece como "acabando". Mora aqui, e não
// em services, porque a migração precisa dele como valor padrão da
// coluna — e database não pode importar services (é services que importa
// database). services.LowStockThreshold aponta para cá.
const DefaultMinimumStock = 10

// Path é o caminho do banco em uso, para os comandos de linha mostrarem
// em qual arquivo estão mexendo.
func Path() string {
	return dbPath()
}

// dbPath retorna o caminho do arquivo SQLite. Em produção (Railway), a
// variável DB_PATH aponta para dentro do volume persistente (ex: /data/uchoastock.db),
// evitando que os dados sumam a cada deploy. Sem a variável, usa o caminho
// local de sempre (dev).
func dbPath() string {
	if p := os.Getenv("DB_PATH"); p != "" {
		return p
	}
	return "backend/data/uchoastock.db"
}

// dsn é o endereço de conexão: o caminho do banco mais os PRAGMAs que
// toda conexão precisa. foreign_keys vale por conexão, e o SQLite nasce
// com ela desligada. Antes o PRAGMA era rodado uma vez só, depois de
// abrir; se o database/sql trocasse a conexão (por exemplo, depois de um
// erro do driver), a nova ficaria sem conferir chave estrangeira, sem
// aviso. Na string de conexão, o driver roda o PRAGMA em toda conexão que
// abre.
//
// O driver corta no primeiro "?" e usa o que vem antes como caminho, sem
// tratar como URI: caminho relativo, "C:/..." e acentos funcionam como
// estão.
func dsn() string {
	return dbPath() + "?_pragma=foreign_keys(1)"
}

func Connect() error {
	var err error

	DB, err = sql.Open("sqlite", dsn())
	if err != nil {
		return err
	}
	DB.SetMaxOpenConns(1)

	return DB.Ping()
}

// CreateTables cria as tabelas e roda todas as migrações numa transação
// só. Se qualquer passo falhar no meio (uma coluna nova, a cópia do
// estoque para as obras, a troca de cargos), o ROLLBACK desfaz tudo o que
// veio antes, inclusive CREATE, ALTER e DROP: no SQLite, comandos de
// estrutura também respeitam a transação. O banco fica exatamente como
// estava, e a próxima inicialização tenta de novo do zero.
//
// Todo comando aqui dentro usa tx, nunca DB: o banco tem uma conexão só
// (SetMaxOpenConns(1)), e ela está presa na transação. Um DB.Exec no meio
// ficaria esperando essa conexão para sempre.
func CreateTables() error {
	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := createTablesTx(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// StockMigrated indica se o banco já passou pela migração para o saldo
// por obra: a tabela saldos existe e movimentacoes tem a coluna obra_id
// (que nasce na mesma migração). Só lê, não altera nada.
func StockMigrated() (bool, error) {
	var tables, columns int
	if err := DB.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'saldos'
	`).Scan(&tables); err != nil {
		return false, err
	}
	if err := DB.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('movimentacoes') WHERE name = 'obra_id'
	`).Scan(&columns); err != nil {
		return false, err
	}
	return tables > 0 && columns > 0, nil
}

// ForeignKeyViolation é uma linha que aponta para um registro que não
// existe, como PRAGMA foreign_key_check devolve.
type ForeignKeyViolation struct {
	Table  string
	RowID  int64 // 0 quando a tabela não tem rowid
	Parent string
}

// ForeignKeyViolations roda PRAGMA foreign_key_check no banco inteiro e
// devolve as linhas com chave estrangeira quebrada. Com as chaves ligadas,
// o SQLite recusa escrita nova que quebre uma chave, mas não confere o que
// já estava gravado: linhas antigas, de antes de a chave existir, ou
// gravadas com ela desligada. A migração também não pega essas linhas (o
// UPDATE só confere as chaves das colunas que ele muda). Só lê.
func ForeignKeyViolations() ([]ForeignKeyViolation, error) {
	rows, err := DB.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var violations []ForeignKeyViolation
	for rows.Next() {
		var v ForeignKeyViolation
		var rowID sql.NullInt64
		var fkIndex int
		if err := rows.Scan(&v.Table, &rowID, &v.Parent, &fkIndex); err != nil {
			return nil, err
		}
		v.RowID = rowID.Int64
		violations = append(violations, v)
	}
	return violations, rows.Err()
}

// TablesWithForeignKeys lista, em ordem alfabética, as tabelas que têm
// chave estrangeira — as que PRAGMA foreign_key_check confere. Serve para
// o verify-stock mostrar o que foi conferido. Só lê.
func TablesWithForeignKeys() ([]string, error) {
	rows, err := DB.Query(`
		SELECT DISTINCT m.name
		FROM sqlite_master m
		JOIN pragma_foreign_key_list(m.name) f
		WHERE m.type = 'table'
		ORDER BY m.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

// createTablesTx é o corpo de CreateTables, dentro da transação.
func createTablesTx(tx *sql.Tx) error {
	query := `
		CREATE TABLE IF NOT EXISTS produtos (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			nome TEXT NOT NULL,
			quantidade INTEGER NOT NULL,
			ativo INTEGER NOT NULL DEFAULT 1
		);
		CREATE TABLE IF NOT EXISTS usuarios (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		nome TEXT NOT NULL,
		email TEXT UNIQUE NOT NULL,
		senha TEXT NOT NULL,
		-- O DEFAULT 'basico' é de um cargo que não existe mais (virou
		-- 'solicitante'). O SQLite não troca o DEFAULT de uma coluna sem
		-- recriar a tabela, então todo INSERT em usuarios informa o role.
		role TEXT NOT NULL DEFAULT 'basico',
		ativo INTEGER NOT NULL DEFAULT 1
		);

	CREATE TABLE IF NOT EXISTS movimentacoes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		produto_id INTEGER NOT NULL,
		usuario_id INTEGER NOT NULL,
		tipo TEXT NOT NULL,
		quantidade INTEGER NOT NULL,
		data DATETIME DEFAULT CURRENT_TIMESTAMP,

		FOREIGN KEY (produto_id) REFERENCES produtos(id),
		FOREIGN KEY (usuario_id) REFERENCES usuarios(id)
);

	CREATE TABLE IF NOT EXISTS sessoes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    usuario_id INTEGER NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    expira_em DATETIME NOT NULL,
    criado_em DATETIME DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (usuario_id) REFERENCES usuarios(id)
);

	-- Obras e locais onde fica material. tipo é 'CENTRAL' (o almoxarifado
	-- central, que abastece as obras) ou 'OBRA'. situacao é 'ANDAMENTO',
	-- 'PARALISADA' ou 'CONCLUIDA': obra nunca é apagada, porque o
	-- histórico vai apontar para ela.
	CREATE TABLE IF NOT EXISTS obras (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		nome TEXT NOT NULL,
		tipo TEXT NOT NULL DEFAULT 'OBRA',
		cidade TEXT NOT NULL DEFAULT '',
		responsavel TEXT NOT NULL DEFAULT '',
		situacao TEXT NOT NULL DEFAULT 'ANDAMENTO',
		ativo INTEGER NOT NULL DEFAULT 1,
		criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- Quanto existe de cada material em cada obra. O catálogo (produtos) é
	-- um só para a empresa; o que muda de uma obra para outra é o saldo.
	CREATE TABLE IF NOT EXISTS saldos (
		produto_id INTEGER NOT NULL REFERENCES produtos(id),
		obra_id INTEGER NOT NULL REFERENCES obras(id),
		quantidade REAL NOT NULL DEFAULT 0,
		PRIMARY KEY (produto_id, obra_id)
	);

	-- Em qual obra cada usuário atua. Hoje é uma obra por usuário (regra
	-- garantida em services); a chave composta deixa o banco pronto caso
	-- isso vire mais de uma. Administradores não têm vínculo: veem todas.
	CREATE TABLE IF NOT EXISTS usuario_obras (
		usuario_id INTEGER NOT NULL REFERENCES usuarios(id),
		obra_id INTEGER NOT NULL REFERENCES obras(id),
		PRIMARY KEY (usuario_id, obra_id)
	);

	-- Requisição de material: alguém da obra pede, o gestor aprova e o
	-- almoxarife atende (o atendimento gera as saídas de estoque). status
	-- é 'PENDENTE', 'APROVADA', 'REJEITADA', 'PARCIAL', 'ATENDIDA' ou
	-- 'CANCELADA'; as regras de transição ficam em services. aprovado_por e
	-- aprovado_em só são preenchidos na aprovação. Datas em UTC, como o
	-- resto do banco: na tela, sempre com 'localtime'.
	CREATE TABLE IF NOT EXISTS requisicoes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		obra_id INTEGER NOT NULL REFERENCES obras(id),
		solicitante_id INTEGER NOT NULL REFERENCES usuarios(id),
		status TEXT NOT NULL DEFAULT 'PENDENTE',
		observacao TEXT NOT NULL DEFAULT '',
		aprovado_por INTEGER REFERENCES usuarios(id),
		aprovado_em DATETIME,
		motivo_rejeicao TEXT NOT NULL DEFAULT '',
		criado_em DATETIME DEFAULT CURRENT_TIMESTAMP,
		atualizado_em DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- Itens da requisição. quantidade_atendida cresce a cada atendimento,
	-- até chegar em quantidade_solicitada. REAL, como o saldo: há material
	-- medido em fração (2,5 m³).
	CREATE TABLE IF NOT EXISTS requisicao_itens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		requisicao_id INTEGER NOT NULL REFERENCES requisicoes(id),
		produto_id INTEGER NOT NULL REFERENCES produtos(id),
		quantidade_solicitada REAL NOT NULL,
		quantidade_atendida REAL NOT NULL DEFAULT 0,
		UNIQUE (requisicao_id, produto_id)
	);

	-- Histórico da requisição: quem fez o quê e quando. acao é 'CRIADA',
	-- 'APROVADA', 'REJEITADA', 'ATENDIMENTO' ou 'CANCELADA'.
	CREATE TABLE IF NOT EXISTS requisicao_eventos (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		requisicao_id INTEGER NOT NULL REFERENCES requisicoes(id),
		usuario_id INTEGER NOT NULL REFERENCES usuarios(id),
		acao TEXT NOT NULL,
		detalhe TEXT NOT NULL DEFAULT '',
		criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_requisicoes_obra_status ON requisicoes (obra_id, status);
	CREATE INDEX IF NOT EXISTS idx_requisicoes_solicitante ON requisicoes (solicitante_id);
	CREATE INDEX IF NOT EXISTS idx_requisicao_itens_requisicao ON requisicao_itens (requisicao_id);
	`

	_, err := tx.Exec(query)
	if err != nil {
		return err
	}

	var activeColumn int
	err = tx.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info('produtos')
		WHERE name = 'ativo'
	`).Scan(&activeColumn)
	if err != nil {
		return err
	}
	if activeColumn == 0 {
		if _, err = tx.Exec(`ALTER TABLE produtos ADD COLUMN ativo INTEGER NOT NULL DEFAULT 1`); err != nil {
			return err
		}
	}

	var roleColumn int
	err = tx.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info('usuarios')
		WHERE name = 'role'
	`).Scan(&roleColumn)
	if err != nil {
		return err
	}
	if roleColumn == 0 {
		if _, err = tx.Exec(`ALTER TABLE usuarios ADD COLUMN role TEXT NOT NULL DEFAULT 'basico'`); err != nil {
			return err
		}
	}

	var userActiveColumn int
	err = tx.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info('usuarios')
		WHERE name = 'ativo'
	`).Scan(&userActiveColumn)
	if err != nil {
		return err
	}
	if userActiveColumn == 0 {
		if _, err = tx.Exec(`ALTER TABLE usuarios ADD COLUMN ativo INTEGER NOT NULL DEFAULT 1`); err != nil {
			return err
		}
	}

	// Colunas do controle de materiais de obra. Ficam fora do CREATE
	// TABLE de propósito: assim o banco novo e o de produção passam pelo
	// mesmo caminho. Cada uma só é criada se ainda não existir, então
	// rodar isto a cada inicialização é seguro.
	//
	// quantidade e limite_minimo são INTEGER no schema, mas guardam
	// decimais (2,5 m³) sem perda: no SQLite, um valor que não cabe em
	// inteiro fica armazenado como REAL mesmo numa coluna INTEGER. Por
	// isso não foi preciso recriar a tabela para aceitar frações.
	newColumns := []struct{ table, column, definition string }{
		{"produtos", "unidade", "TEXT NOT NULL DEFAULT 'un'"},
		{"produtos", "limite_minimo", fmt.Sprintf("INTEGER NOT NULL DEFAULT %d", DefaultMinimumStock)},
		{"movimentacoes", "observacao", "TEXT NOT NULL DEFAULT ''"},
		// Obra escolhida no seletor do topo. NULL é "Todas as obras".
		{"sessoes", "obra_id", "INTEGER REFERENCES obras(id)"},
		// Requisição que originou a saída. NULL para entrada, saída avulsa
		// e atualização de cadastro.
		{"movimentacoes", "requisicao_id", "INTEGER REFERENCES requisicoes(id)"},
	}
	for _, c := range newColumns {
		if err = addColumnIfMissing(tx, c.table, c.column, c.definition); err != nil {
			return err
		}
	}

	// Migração do email da identidade reservada. O endereço já foi
	// admin@gmail.com e depois ceo@gmail.com; hoje é superadmin@gmail.com.
	// Renomeia a conta existente uma única vez (nada mais nela muda) antes
	// da correção de cargo abaixo, para que ela continue sendo reconhecida
	// sem duplicar nem perder a permissão.
	//
	// Se as duas contas antigas existirem, só uma pode ficar com o
	// endereço novo, porque email é UNIQUE: a ceo@gmail.com, que era a
	// identidade reservada até agora. A admin@gmail.com continua como
	// uma conta comum. Sem o "LIMIT 1" o UPDATE tentava dar o mesmo
	// email às duas e o sistema não subia.
	if _, err = tx.Exec(`
		UPDATE usuarios SET email = 'superadmin@gmail.com'
		WHERE id = (
			SELECT id FROM usuarios
			WHERE LOWER(TRIM(email)) IN ('ceo@gmail.com', 'admin@gmail.com')
			ORDER BY CASE LOWER(TRIM(email)) WHEN 'ceo@gmail.com' THEN 0 ELSE 1 END
			LIMIT 1
		)
		AND NOT EXISTS (SELECT 1 FROM usuarios WHERE LOWER(TRIM(email)) = 'superadmin@gmail.com')
	`); err != nil {
		return err
	}

	// O cargo "ceo" foi renomeado para "superadmin". Converte quem ainda
	// estiver com o nome antigo antes da checagem de exclusividade.
	if _, err = tx.Exec(`
		UPDATE usuarios SET role = 'superadmin'
		WHERE LOWER(TRIM(role)) = 'ceo'
	`); err != nil {
		return err
	}

	// O SuperAdmin é uma identidade reservada: somente superadmin@gmail.com
	// pode possuir esse cargo. Isso corrige o banco a cada inicialização,
	// mesmo que alguém tenha mexido direto nele.
	if _, err = tx.Exec(`
		UPDATE usuarios
		SET role = CASE
			WHEN LOWER(TRIM(email)) = 'superadmin@gmail.com' THEN 'superadmin'
			WHEN LOWER(TRIM(role)) = 'superadmin' THEN 'solicitante'
			ELSE role
		END
	`); err != nil {
		return err
	}

	// Com as permissões por ação, "gerente" virou "gestor" e "basico"
	// virou "solicitante". Idempotente: depois da primeira vez não sobra
	// ninguém com o nome antigo para converter.
	if _, err = tx.Exec(`UPDATE usuarios SET role = 'gestor' WHERE LOWER(TRIM(role)) = 'gerente'`); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE usuarios SET role = 'solicitante' WHERE LOWER(TRIM(role)) = 'basico'`); err != nil {
		return err
	}

	// O UchôaStock virou um sistema de controle de materiais de obra:
	// não há mais venda nem preço, só entrada e saída de material.
	// As duas migrações abaixo removem o que sobrou do modelo antigo e
	// são idempotentes — rodam uma vez e depois não encontram mais nada.
	if _, err = tx.Exec(`DROP TABLE IF EXISTS vendas`); err != nil {
		return err
	}

	var priceColumn int
	err = tx.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info('produtos')
		WHERE name = 'preco'
	`).Scan(&priceColumn)
	if err != nil {
		return err
	}
	if priceColumn > 0 {
		if _, err = tx.Exec(`ALTER TABLE produtos DROP COLUMN preco`); err != nil {
			return err
		}
	}

	// Todo banco tem um almoxarifado central: é de onde o material sai
	// para as obras. O NOT EXISTS faz o INSERT rodar uma vez só, e não
	// recria o central se alguém tiver mudado o nome dele.
	if _, err = tx.Exec(`
		INSERT INTO obras (nome, tipo)
		SELECT 'Almoxarifado central', 'CENTRAL'
		WHERE NOT EXISTS (SELECT 1 FROM obras WHERE tipo = 'CENTRAL')
	`); err != nil {
		return err
	}

	return migrateStockToSites(tx)
}

// migrateStockToSites passa o estoque para o modelo por obra: a
// quantidade de cada material vira saldo no almoxarifado central, e as
// entradas e saídas antigas passam a apontar para ele. As atualizações
// de cadastro ficam sem obra, porque o catálogo é da empresa toda.
//
// O sinal de que a migração já rodou é a coluna movimentacoes.obra_id:
// ela nasce aqui, junto com a cópia. Assim a cópia nunca roda duas vezes
// (o que dobraria o estoque). Roda dentro da transação de CreateTables:
// se algo falhar, o ROLLBACK desfaz a coluna e a cópia junto com o resto.
//
// produtos.quantidade continua existindo, mas o sistema não lê nem grava
// mais nela; fica só como cópia do estoque de antes da migração.
func migrateStockToSites(tx *sql.Tx) error {
	var done int
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info('movimentacoes')
		WHERE name = 'obra_id'
	`).Scan(&done); err != nil {
		return err
	}
	if done > 0 {
		return nil
	}

	steps := []string{
		`ALTER TABLE movimentacoes ADD COLUMN obra_id INTEGER REFERENCES obras(id)`,
		`INSERT INTO saldos (produto_id, obra_id, quantidade)
		 SELECT p.id, (SELECT id FROM obras WHERE tipo = 'CENTRAL'), ROUND(p.quantidade, 3)
		 FROM produtos p`,
		`UPDATE movimentacoes
		 SET obra_id = (SELECT id FROM obras WHERE tipo = 'CENTRAL')
		 WHERE tipo IN ('ENTRADA', 'SAIDA')`,
	}
	for _, step := range steps {
		if _, err := tx.Exec(step); err != nil {
			return err
		}
	}
	return nil
}

// addColumnIfMissing adiciona uma coluna à tabela só se ela ainda não
// existir. table, column e definition vêm sempre de constantes do
// código, nunca do usuário: comando de estrutura (DDL) não aceita
// placeholder "?", por isso ele é montado com Sprintf.
func addColumnIfMissing(tx *sql.Tx, table, column, definition string) error {
	var count int
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info(?)
		WHERE name = ?
	`, table, column).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	_, err := tx.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	return err
}
