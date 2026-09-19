package database

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

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
// busy_timeout faz a conexão esperar até 5 segundos quando outro processo
// está escrevendo no banco, em vez de falhar na hora com "database is
// locked". Acontece ao rodar um comando de linha (reset-password, por
// exemplo) com o servidor ligado.
//
// O driver corta no primeiro "?" e usa o que vem antes como caminho, sem
// tratar como URI: caminho relativo, "C:/..." e acentos funcionam como
// estão.
func dsn() string {
	return dbPath() + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
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

// InvalidText é um texto gravado com bytes que não são UTF-8 válido.
type InvalidText struct {
	Table  string
	RowID  int64
	Column string
	Raw    []byte
}

// Latin1 lê o texto como Latin-1 (ISO-8859-1): cada byte que não forma
// UTF-8 vira o caractere de mesmo número. É o palpite do texto certo
// quando ele veio de um programa do Windows em português ("ã" = 0xE3).
func (t InvalidText) Latin1() string {
	var out []rune
	for raw := t.Raw; len(raw) > 0; {
		r, size := utf8.DecodeRune(raw)
		if r == utf8.RuneError && size <= 1 {
			r, size = rune(raw[0]), 1
		}
		out = append(out, r)
		raw = raw[size:]
	}
	return string(out)
}

// invalidTextColumns são as colunas de texto conferidas por
// InvalidUTF8Texts: as que alguém digita e aparecem na tela.
var invalidTextColumns = []struct {
	table   string
	columns []string
}{
	{"produtos", []string{"nome", "unidade"}},
	{"obras", []string{"nome", "cidade", "responsavel"}},
	{"usuarios", []string{"nome", "email"}},
}

// InvalidUTF8Texts lista os textos de produtos, obras e usuarios que não
// são UTF-8 válido. O SQLite guarda o que recebe, byte a byte: um programa
// que mande "ã" em Latin-1 grava um texto que o navegador mostra como "�".
// Tabela que ainda não existe (banco antigo) é pulada. Só lê.
func InvalidUTF8Texts() ([]InvalidText, error) {
	var found []InvalidText
	for _, target := range invalidTextColumns {
		var exists int
		if err := DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, target.table).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			continue
		}

		// Nomes de tabela e coluna vêm da lista fixa acima, nunca do
		// usuário. CAST AS BLOB devolve os bytes como estão gravados.
		selects := make([]string, len(target.columns))
		for i, column := range target.columns {
			selects[i] = "CAST(" + column + " AS BLOB)"
		}
		rows, err := DB.Query(fmt.Sprintf("SELECT id, %s FROM %s ORDER BY id", strings.Join(selects, ", "), target.table))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			values := make([][]byte, len(target.columns))
			pointers := []any{&id}
			for i := range values {
				pointers = append(pointers, &values[i])
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				return nil, err
			}
			for i, value := range values {
				if !utf8.Valid(value) {
					found = append(found, InvalidText{Table: target.table, RowID: id, Column: target.columns[i], Raw: value})
				}
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return found, nil
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
	// Antes de qualquer CREATE TABLE: renomeia as tabelas de requisição
	// para solicitação. Se rodasse depois, o CREATE criaria as tabelas
	// novas vazias e os dados antigos ficariam órfãos. Ver a função.
	if err := renameRequestTablesTx(tx); err != nil {
		return err
	}

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

	-- Solicitação de material: alguém da obra pede, o gestor aprova e o
	-- almoxarife atende (o atendimento gera as saídas de estoque). status
	-- é 'PENDENTE', 'APROVADA', 'REJEITADA', 'PARCIAL', 'ATENDIDA' ou
	-- 'CANCELADA'; as regras de transição ficam em services. aprovado_por e
	-- aprovado_em só são preenchidos na aprovação. Datas em UTC, como o
	-- resto do banco: na tela, sempre com 'localtime'.
	CREATE TABLE IF NOT EXISTS solicitacoes (
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

	-- Itens da solicitação. quantidade_atendida cresce a cada atendimento,
	-- até chegar em quantidade_solicitada. REAL, como o saldo: há material
	-- medido em fração (2,5 m³).
	CREATE TABLE IF NOT EXISTS solicitacao_itens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		solicitacao_id INTEGER NOT NULL REFERENCES solicitacoes(id),
		produto_id INTEGER NOT NULL REFERENCES produtos(id),
		quantidade_solicitada REAL NOT NULL,
		quantidade_atendida REAL NOT NULL DEFAULT 0,
		UNIQUE (solicitacao_id, produto_id)
	);

	-- Histórico da solicitação: quem fez o quê e quando. acao é 'CRIADA',
	-- 'APROVADA', 'REJEITADA', 'ATENDIMENTO' ou 'CANCELADA'.
	CREATE TABLE IF NOT EXISTS solicitacao_eventos (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		solicitacao_id INTEGER NOT NULL REFERENCES solicitacoes(id),
		usuario_id INTEGER NOT NULL REFERENCES usuarios(id),
		acao TEXT NOT NULL,
		detalhe TEXT NOT NULL DEFAULT '',
		criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- Fornecedores de material da empresa (um cadastro só, para todas as
	-- obras). cnpj fica só com os 14 caracteres, sem pontuação, ou vazio.
	-- Fornecedor nunca é apagado, só desativado (ativo = 0): se um dia uma
	-- entrada apontar para ele, o histórico não perde a referência.
	CREATE TABLE IF NOT EXISTS fornecedores (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		nome TEXT NOT NULL,
		cnpj TEXT NOT NULL DEFAULT '',
		contato TEXT NOT NULL DEFAULT '',
		telefone TEXT NOT NULL DEFAULT '',
		email TEXT NOT NULL DEFAULT '',
		cidade TEXT NOT NULL DEFAULT '',
		observacao TEXT NOT NULL DEFAULT '',
		ativo INTEGER NOT NULL DEFAULT 1,
		criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- Inventário: a contagem física de uma obra, comparada com o saldo.
	-- status é 'EM_CONTAGEM', 'AGUARDANDO_APROVACAO', 'APROVADO' ou
	-- 'CANCELADO'; enquanto está em contagem ou aguardando aprovação, a
	-- obra não aceita entrada nem saída. Rejeitar devolve para contagem,
	-- com o motivo em motivo_rejeicao. Datas em UTC, como o resto do banco.
	CREATE TABLE IF NOT EXISTS inventarios (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		obra_id INTEGER NOT NULL REFERENCES obras(id),
		status TEXT NOT NULL DEFAULT 'EM_CONTAGEM',
		aberto_por INTEGER NOT NULL REFERENCES usuarios(id),
		aberto_em DATETIME DEFAULT CURRENT_TIMESTAMP,
		enviado_por INTEGER REFERENCES usuarios(id),
		enviado_em DATETIME,
		decidido_por INTEGER REFERENCES usuarios(id),
		decidido_em DATETIME,
		motivo_rejeicao TEXT NOT NULL DEFAULT '',
		atualizado_em DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- Itens do inventário. saldo_esperado é o saldo congelado no início
	-- (ou quando o material foi acrescentado). quantidade_contada NULL é
	-- item ainda não contado. contado_por é quem registrou a contagem: essa
	-- pessoa não aprova o ajuste. REAL, como o saldo.
	CREATE TABLE IF NOT EXISTS inventario_itens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		inventario_id INTEGER NOT NULL REFERENCES inventarios(id),
		produto_id INTEGER NOT NULL REFERENCES produtos(id),
		saldo_esperado REAL NOT NULL,
		quantidade_contada REAL,
		justificativa TEXT NOT NULL DEFAULT '',
		contado_por INTEGER REFERENCES usuarios(id),
		UNIQUE (inventario_id, produto_id)
	);

	CREATE INDEX IF NOT EXISTS idx_inventarios_obra_status ON inventarios (obra_id, status);
	CREATE INDEX IF NOT EXISTS idx_inventario_itens_inventario ON inventario_itens (inventario_id);
	-- No máximo um inventário aberto por obra. Índice parcial: só as linhas
	-- em aberto entram nele, então o histórico pode ter vários da mesma obra.
	CREATE UNIQUE INDEX IF NOT EXISTS idx_inventarios_um_aberto_por_obra
		ON inventarios (obra_id) WHERE status IN ('EM_CONTAGEM', 'AGUARDANDO_APROVACAO');

	CREATE INDEX IF NOT EXISTS idx_solicitacoes_obra_status ON solicitacoes (obra_id, status);
	CREATE INDEX IF NOT EXISTS idx_solicitacoes_solicitante ON solicitacoes (solicitante_id);
	CREATE INDEX IF NOT EXISTS idx_solicitacao_itens_solicitacao ON solicitacao_itens (solicitacao_id);
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
		// Solicitação que originou a saída. NULL para entrada, saída avulsa
		// e atualização de cadastro.
		{"movimentacoes", "solicitacao_id", "INTEGER REFERENCES solicitacoes(id)"},
		// Inventário que originou o ajuste (tipo AJUSTE). NULL para o resto.
		{"movimentacoes", "inventario_id", "INTEGER REFERENCES inventarios(id)"},
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
	//
	// Exceção: usuario@gmail.com é mantido em "basico" de propósito. Esse
	// cargo não tem permissão nenhuma (não está em rolePermissions), então
	// a conta serve de demonstração somente-leitura — vê as telas sem
	// poder alterar nada nem criar solicitação. Sem esta carve-out a
	// conta viraria um solicitante comum a cada inicialização. As demais
	// contas basico (sobras da migração antiga) continuam virando
	// solicitante.
	if _, err = tx.Exec(`UPDATE usuarios SET role = 'gestor' WHERE LOWER(TRIM(role)) = 'gerente'`); err != nil {
		return err
	}
	if _, err = tx.Exec(`
		UPDATE usuarios SET role = 'solicitante'
		WHERE LOWER(TRIM(role)) = 'basico'
		  AND LOWER(TRIM(email)) != 'usuario@gmail.com'
	`); err != nil {
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

	if err := migrateStockToSites(tx); err != nil {
		return err
	}

	// Índices das consultas que mais crescem com o tempo. Ficam depois de
	// migrateStockToSites porque movimentacoes.obra_id nasce lá; num banco
	// antigo, o índice criado antes dela falharia. IF NOT EXISTS: rodar a
	// cada inicialização é seguro. Índice não muda dado nenhum, só deixa a
	// busca mais rápida.
	//   - produto + obra + data: o "último a movimentar" do alerta de
	//     estoque baixo, que procura a movimentação mais recente de cada
	//     material em cada obra;
	//   - obra + data: o histórico, o dashboard e o relatório de uma obra.
	_, err = tx.Exec(`
		CREATE INDEX IF NOT EXISTS idx_movimentacoes_produto_obra_data ON movimentacoes (produto_id, obra_id, data);
		CREATE INDEX IF NOT EXISTS idx_movimentacoes_obra_data ON movimentacoes (obra_id, data);
	`)
	return err
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

// renameRequestTablesTx troca os nomes de "requisição" para "solicitação"
// no banco: requisicoes, requisicao_itens e requisicao_eventos viram
// solicitacoes, solicitacao_itens e solicitacao_eventos, e a coluna
// requisicao_id vira solicitacao_id nas tabelas que a usam.
//
// Roda ANTES dos CREATE TABLE IF NOT EXISTS de createTablesTx, e a ordem
// é o ponto todo: se o CREATE viesse primeiro, ele criaria uma tabela
// solicitacoes vazia, o rename seria pulado (o destino já existiria) e as
// solicitações antigas ficariam presas numa tabela que ninguém mais
// consulta — sem erro nenhum, que é o pior tipo de bug de migração.
//
// Cada passo confere antes de agir, então rodar a cada inicialização é
// seguro: em banco novo, ou já migrado, a função não faz nada. O RENAME TO
// do SQLite ainda acerta sozinho as REFERENCES das outras tabelas que
// apontavam para a tabela renomeada.
func renameRequestTablesTx(tx *sql.Tx) error {
	tables := []struct{ from, to string }{
		{"requisicoes", "solicitacoes"},
		{"requisicao_itens", "solicitacao_itens"},
		{"requisicao_eventos", "solicitacao_eventos"},
	}
	for _, t := range tables {
		hasOld, err := tableExistsTx(tx, t.from)
		if err != nil {
			return err
		}
		hasNew, err := tableExistsTx(tx, t.to)
		if err != nil {
			return err
		}
		if !hasOld || hasNew {
			continue
		}
		if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE %s RENAME TO %s", t.from, t.to)); err != nil {
			return err
		}
	}

	// As tabelas já estão com o nome novo aqui, então a coluna é
	// procurada nelas. movimentacoes guarda a solicitação que originou a
	// saída de estoque.
	columns := []struct{ table, from, to string }{
		{"solicitacao_itens", "requisicao_id", "solicitacao_id"},
		{"solicitacao_eventos", "requisicao_id", "solicitacao_id"},
		{"movimentacoes", "requisicao_id", "solicitacao_id"},
	}
	for _, c := range columns {
		hasOld, err := columnExistsTx(tx, c.table, c.from)
		if err != nil {
			return err
		}
		hasNew, err := columnExistsTx(tx, c.table, c.to)
		if err != nil {
			return err
		}
		if !hasOld || hasNew {
			continue
		}
		if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", c.table, c.from, c.to)); err != nil {
			return err
		}
	}

	// O RENAME TO leva o índice junto, mas mantendo o nome antigo. Apagar
	// aqui deixa o CREATE INDEX IF NOT EXISTS de createTablesTx recriar
	// cada um com o nome novo, sobre a mesma tabela.
	for _, index := range []string{
		"idx_requisicoes_obra_status",
		"idx_requisicoes_solicitante",
		"idx_requisicao_itens_requisicao",
	} {
		if _, err := tx.Exec("DROP INDEX IF EXISTS " + index); err != nil {
			return err
		}
	}

	// Histórico já gravado: a saída de estoque criada por um atendimento
	// guarda "Requisição #12" em movimentacoes.observacao. Sem este UPDATE
	// a tela de movimentações mostraria o nome velho nas linhas antigas e
	// o novo nas próximas, para sempre. O filtro por solicitacao_id
	// garante que só mudem as observações escritas pelo sistema, nunca uma
	// que alguém digitou.
	hasColumn, err := columnExistsTx(tx, "movimentacoes", "solicitacao_id")
	if err != nil {
		return err
	}
	if !hasColumn {
		return nil
	}
	_, err = tx.Exec(`
		UPDATE movimentacoes
		SET observacao = REPLACE(observacao, 'Requisição #', 'Solicitação #')
		WHERE solicitacao_id IS NOT NULL AND observacao LIKE 'Requisição #%'
	`)
	return err
}

// tableExistsTx diz se a tabela existe no banco.
func tableExistsTx(tx *sql.Tx, table string) (bool, error) {
	var count int
	err := tx.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?
	`, table).Scan(&count)
	return count > 0, err
}

// columnExistsTx diz se a coluna existe na tabela. Tabela que não existe
// devolve false, sem erro: o pragma só não traz linha nenhuma.
func columnExistsTx(tx *sql.Tx, table, column string) (bool, error) {
	var count int
	err := tx.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?
	`, table, column).Scan(&count)
	return count > 0, err
}
