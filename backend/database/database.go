package database

import (
	"database/sql"
	"os"

	_ "gosqlite.org"
)

var DB *sql.DB

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

func Connect() error {
	var err error

	DB, err = sql.Open("sqlite", dbPath())
	if err != nil {
		return err
	}
	DB.SetMaxOpenConns(1)

	if err := DB.Ping(); err != nil {
		return err
	}

	_, err = DB.Exec("PRAGMA foreign_keys = ON")
	return err
}

func CreateTables() error {
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
	`

	_, err := DB.Exec(query)
	if err != nil {
		return err
	}

	var activeColumn int
	err = DB.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info('produtos')
		WHERE name = 'ativo'
	`).Scan(&activeColumn)
	if err != nil {
		return err
	}
	if activeColumn == 0 {
		if _, err = DB.Exec(`ALTER TABLE produtos ADD COLUMN ativo INTEGER NOT NULL DEFAULT 1`); err != nil {
			return err
		}
	}

	var roleColumn int
	err = DB.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info('usuarios')
		WHERE name = 'role'
	`).Scan(&roleColumn)
	if err != nil {
		return err
	}
	if roleColumn == 0 {
		if _, err = DB.Exec(`ALTER TABLE usuarios ADD COLUMN role TEXT NOT NULL DEFAULT 'basico'`); err != nil {
			return err
		}
	}

	var userActiveColumn int
	err = DB.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info('usuarios')
		WHERE name = 'ativo'
	`).Scan(&userActiveColumn)
	if err != nil {
		return err
	}
	if userActiveColumn == 0 {
		if _, err = DB.Exec(`ALTER TABLE usuarios ADD COLUMN ativo INTEGER NOT NULL DEFAULT 1`); err != nil {
			return err
		}
	}

	// Migração do email da identidade reservada. O endereço já foi
	// admin@gmail.com e depois ceo@gmail.com; hoje é superadmin@gmail.com.
	// Renomeia a conta existente uma única vez (nada mais nela muda) antes
	// da correção de cargo abaixo, para que ela continue sendo reconhecida
	// sem duplicar nem perder a permissão.
	if _, err = DB.Exec(`
		UPDATE usuarios SET email = 'superadmin@gmail.com'
		WHERE LOWER(TRIM(email)) IN ('admin@gmail.com', 'ceo@gmail.com')
		AND NOT EXISTS (SELECT 1 FROM usuarios WHERE LOWER(TRIM(email)) = 'superadmin@gmail.com')
	`); err != nil {
		return err
	}

	// O cargo "ceo" foi renomeado para "superadmin". Converte quem ainda
	// estiver com o nome antigo antes da checagem de exclusividade.
	if _, err = DB.Exec(`
		UPDATE usuarios SET role = 'superadmin'
		WHERE LOWER(TRIM(role)) = 'ceo'
	`); err != nil {
		return err
	}

	// O SuperAdmin é uma identidade reservada: somente superadmin@gmail.com
	// pode possuir esse cargo. Isso corrige o banco a cada inicialização,
	// mesmo que alguém tenha mexido direto nele.
	if _, err = DB.Exec(`
		UPDATE usuarios
		SET role = CASE
			WHEN LOWER(TRIM(email)) = 'superadmin@gmail.com' THEN 'superadmin'
			WHEN LOWER(TRIM(role)) = 'superadmin' THEN 'basico'
			ELSE role
		END
	`); err != nil {
		return err
	}

	// O UchôaStock virou um sistema de controle de materiais de obra:
	// não há mais venda nem preço, só entrada e saída de material.
	// As duas migrações abaixo removem o que sobrou do modelo antigo e
	// são idempotentes — rodam uma vez e depois não encontram mais nada.
	if _, err = DB.Exec(`DROP TABLE IF EXISTS vendas`); err != nil {
		return err
	}

	var priceColumn int
	err = DB.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info('produtos')
		WHERE name = 'preco'
	`).Scan(&priceColumn)
	if err != nil {
		return err
	}
	if priceColumn > 0 {
		if _, err = DB.Exec(`ALTER TABLE produtos DROP COLUMN preco`); err != nil {
			return err
		}
	}

	return nil
}
