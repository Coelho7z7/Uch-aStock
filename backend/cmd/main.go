package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	database "uchoastock/backend/database"
	"uchoastock/backend/services"
	"uchoastock/backend/utils"
)

func main() {
	if err := prepareProjectDirectory(); err != nil {
		fmt.Println("Erro ao localizar os arquivos do projeto:", err)
		os.Exit(1)
	}

	if err := database.Connect(); err != nil {
		fmt.Println("Erro ao conectar ao banco de dados:", err)
		os.Exit(1)
	}
	defer database.DB.Close()

	// verify-stock roda antes de CreateTables: por padrão ele não pode
	// migrar nada, só com --migrate.
	if len(os.Args) > 1 && os.Args[1] == "verify-stock" {
		code := verifyStock(os.Args[2:], os.Stdout)
		database.DB.Close()
		os.Exit(code)
	}

	if err := database.CreateTables(); err != nil {
		fmt.Println("Erro ao preparar as tabelas do banco de dados:", err)
		os.Exit(1)
	}

	if len(os.Args) > 1 && os.Args[1] == "reset-password" {
		runResetPasswordCommand(os.Args[2:])
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "create-user" {
		runCreateUserCommand(os.Args[2:])
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "rename-user" {
		runRenameUserCommand(os.Args[2:])
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "change-email" {
		runChangeEmailCommand(os.Args[2:])
		return
	}

	if err := services.SeedDefaultUsers(); err != nil {
		fmt.Println("Erro ao criar usuários padrão:", err)
		os.Exit(1)
	}

	registerRoutes()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Println("Servidor web disponível na porta", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Println("Erro no servidor web:", err)
		os.Exit(1)
	}
}

// runResetPasswordCommand troca a senha de uma conta já existente via
// linha de comando, ex.:
//
//	go run ./backend/cmd reset-password superadmin@gmail.com NovaSenha!123
//
// Encerra o processo sem subir o servidor web. Existe porque ainda não há
// uma tela no painel para trocar a senha de um usuário já criado (só na
// criação e no seed inicial).
func runResetPasswordCommand(args []string) {
	if len(args) != 2 {
		fmt.Println("Uso: reset-password <email> <nova-senha>")
		os.Exit(1)
	}

	if err := services.ResetPassword(args[0], args[1]); err != nil {
		fmt.Println("Erro ao trocar a senha:", err)
		os.Exit(1)
	}

	fmt.Println("Senha atualizada com sucesso para", args[0])
}

// runCreateUserCommand cadastra um usuário via linha de comando, ex.:
//
//	go run ./backend/cmd create-user "Nome" email@gmail.com "Senha!123" admin
//
// Cargos aceitos: admin, gestor, almoxarife, solicitante, auditor (nunca "superadmin" — reservado a
// superadmin@gmail.com e criado apenas pelo seed). Encerra o processo sem subir
// o servidor web.
func runCreateUserCommand(args []string) {
	if len(args) != 4 {
		fmt.Println("Uso: create-user <nome> <email> <senha> <admin|gestor|almoxarife|solicitante|auditor>")
		os.Exit(1)
	}

	if err := services.CreateUserWeb(args[0], args[1], args[2], args[3], 0); err != nil {
		fmt.Println("Erro ao criar usuário:", err)
		os.Exit(1)
	}

	fmt.Println("Usuário criado com sucesso:", args[1])
}

// runRenameUserCommand troca o nome de exibição de uma conta já existente
// via linha de comando, ex.:
//
//	go run ./backend/cmd rename-user matheus@gmail.com "Novo Nome"
//
// Encerra o processo sem subir o servidor web.
func runRenameUserCommand(args []string) {
	if len(args) != 2 {
		fmt.Println("Uso: rename-user <email> <novo-nome>")
		os.Exit(1)
	}

	if err := services.RenameUser(args[0], args[1]); err != nil {
		fmt.Println("Erro ao renomear usuário:", err)
		os.Exit(1)
	}

	fmt.Println("Nome atualizado com sucesso para", args[0])
}

// runChangeEmailCommand troca o email de uma conta já existente via linha
// de comando, ex.:
//
//	go run ./backend/cmd change-email matheus@gmail.com gerente@gmail.com
//
// Encerra o processo sem subir o servidor web.
func runChangeEmailCommand(args []string) {
	if len(args) != 2 {
		fmt.Println("Uso: change-email <email-atual> <novo-email>")
		os.Exit(1)
	}

	if err := services.ChangeUserEmail(args[0], args[1]); err != nil {
		fmt.Println("Erro ao trocar email:", err)
		os.Exit(1)
	}

	fmt.Println("Email atualizado com sucesso:", args[0], "->", args[1])
}

// registerRoutes conecta cada rota HTTP ao seu handler correspondente
// e configura os servidores de arquivos estáticos (CSS/JS).
func registerRoutes() {
	http.Handle("/css/", http.StripPrefix("/css/", http.FileServer(http.Dir("frontend/css"))))
	http.Handle("/js/", http.StripPrefix("/js/", http.FileServer(http.Dir("frontend/js"))))
	http.Handle("/images/", http.StripPrefix("/images/", http.FileServer(http.Dir("images"))))

	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/login", loginHandler)
	http.HandleFunc("/logout", logoutHandler)

	http.HandleFunc("/dashboard", withUser(dashboardHandler))

	http.HandleFunc("/obras", withUser(siteHandler))
	http.HandleFunc("/obra-atual", withUser(siteSwitchHandler))

	http.HandleFunc("/materiais", withUser(materialHandler))
	http.HandleFunc("/alterar-material", withUser(editMaterialHandler))

	http.HandleFunc("/estoque", withUser(stockHandler))

	http.HandleFunc("/movimentacoes", withUser(movementHandler))
	http.HandleFunc("/movimentacoes/exportar", withUser(movementExportHandler))

	http.HandleFunc("/usuarios", withUser(userHandler))
}

// verifyStock é o comando verify-stock, que confere a migração para o
// saldo por obra. Devolve o código de saída (0 = tudo certo), para o teste
// conseguir chamar sem encerrar o processo.
//
//	go run ./backend/cmd verify-stock            (só confere; não altera o banco)
//	go run ./backend/cmd verify-stock --migrate  (migra e confere: só numa CÓPIA)
//
// Sem --migrate, o banco precisa já estar migrado. A conferência compara
// produtos.quantidade com a soma dos saldos, o que só pega migração pela
// metade: logo depois de migrar, as duas batem por construção.
//
// Com --migrate num banco ainda não migrado, o comando tira um retrato do
// estoque antes (e salva em CSV ao lado do banco), roda as migrações e
// confere o resultado contra o retrato.
func verifyStock(args []string, out io.Writer) int {
	flags := flag.NewFlagSet("verify-stock", flag.ContinueOnError)
	flags.SetOutput(out)
	migrate := flags.Bool("migrate", false, "roda as migrações antes de conferir (ALTERA o banco: só numa cópia)")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	fmt.Fprintln(out, "Banco:", database.Path())

	migrated, err := database.StockMigrated()
	if err != nil {
		fmt.Fprintln(out, "Erro ao ler o banco:", err)
		return 1
	}

	if !*migrate {
		if !migrated {
			fmt.Fprintln(out, "ERRO: este banco ainda não foi migrado para o saldo por obra (falta a tabela saldos ou a coluna movimentacoes.obra_id).")
			fmt.Fprintln(out, "Nada foi alterado. Para migrar uma CÓPIA e conferir o resultado, rode de novo com --migrate.")
			return 1
		}
		return reportStockComparison(out)
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
	fmt.Fprintln(out, "!!  ATENÇÃO: --migrate ALTERA O BANCO ACIMA.")
	fmt.Fprintln(out, "!!  As migrações vão rodar nele agora. Use SOMENTE numa CÓPIA,")
	fmt.Fprintln(out, "!!  nunca no banco de produção.")
	fmt.Fprintln(out, "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
	fmt.Fprintln(out, "")

	if migrated {
		fmt.Fprintln(out, "O banco já estava migrado: não há como tirar o retrato de antes. Rodando só as migrações pendentes e a conferência simples.")
		if err := database.CreateTables(); err != nil {
			fmt.Fprintln(out, "Erro nas migrações (nada foi gravado):", err)
			return 1
		}
		code := reportStockComparison(out)
		violations, err := database.ForeignKeyViolations()
		if err != nil {
			fmt.Fprintln(out, "Erro ao conferir as chaves estrangeiras:", err)
			return 1
		}
		for _, v := range violations {
			fmt.Fprintln(out, "  PROBLEMA:", foreignKeyProblem(v))
		}
		if len(violations) > 0 {
			fmt.Fprintf(out, "%d chave(s) estrangeira(s) quebrada(s) (PRAGMA foreign_key_check).\n", len(violations))
			return 1
		}
		fmt.Fprintln(out, "Chaves estrangeiras: nenhuma violação (PRAGMA foreign_key_check).")
		if err := reportForeignKeyTables(out); err != nil {
			fmt.Fprintln(out, "Erro ao listar as tabelas conferidas:", err)
			return 1
		}
		return code
	}

	snapshot, err := services.TakeStockSnapshot()
	if err != nil {
		fmt.Fprintln(out, "Erro ao ler o estoque de antes da migração:", err)
		return 1
	}
	csvPath := database.Path() + ".antes-da-migracao.csv"
	if err := writeSnapshotCSV(csvPath, snapshot); err != nil {
		fmt.Fprintln(out, "Erro ao salvar o retrato de antes da migração:", err)
		return 1
	}
	fmt.Fprintf(out, "Retrato de antes da migração: %d material(is), salvo em %s\n", len(snapshot.Materials), csvPath)

	if err := database.CreateTables(); err != nil {
		fmt.Fprintln(out, "Erro na migração (a transação foi desfeita, nada foi gravado):", err)
		return 1
	}
	fmt.Fprintln(out, "Migração concluída. Conferindo contra o retrato...")

	problems, err := services.CheckMigrationAgainstSnapshot(snapshot)
	if err != nil {
		fmt.Fprintln(out, "Erro ao conferir:", err)
		return 1
	}
	violations, err := database.ForeignKeyViolations()
	if err != nil {
		fmt.Fprintln(out, "Erro ao conferir as chaves estrangeiras:", err)
		return 1
	}
	for _, v := range violations {
		problems = append(problems, foreignKeyProblem(v))
	}
	for _, problem := range problems {
		fmt.Fprintln(out, "  PROBLEMA:", problem)
	}
	if len(problems) > 0 {
		fmt.Fprintf(out, "%d problema(s) encontrado(s).\n", len(problems))
		return 1
	}
	fmt.Fprintf(out, "Nenhum problema: %d material(is) com o saldo igual ao de antes, tudo no almoxarifado central, as movimentações ligadas certo e nenhuma chave estrangeira quebrada (PRAGMA foreign_key_check).\n", len(snapshot.Materials))
	if err := reportForeignKeyTables(out); err != nil {
		fmt.Fprintln(out, "Erro ao listar as tabelas conferidas:", err)
		return 1
	}
	return 0
}

// reportForeignKeyTables mostra em quais tabelas o foreign_key_check
// conferiu chave estrangeira. O PRAGMA confere o banco inteiro; a lista
// deixa visível que as tabelas novas (as de requisição, por exemplo)
// entraram na conta.
func reportForeignKeyTables(out io.Writer) error {
	tables, err := database.TablesWithForeignKeys()
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Chaves estrangeiras conferidas em:", strings.Join(tables, ", "))
	return nil
}

// foreignKeyProblem descreve uma chave estrangeira quebrada para a saída
// do verify-stock.
func foreignKeyProblem(v database.ForeignKeyViolation) string {
	return fmt.Sprintf("chave estrangeira quebrada: %s, linha %d, aponta para um registro de %s que não existe", v.Table, v.RowID, v.Parent)
}

// reportStockComparison mostra a conferência simples (produtos.quantidade
// x soma dos saldos) e devolve o código de saída.
func reportStockComparison(out io.Writer) int {
	divergences, checked, err := services.VerifyStockMigration()
	if err != nil {
		fmt.Fprintln(out, "Erro ao conferir o estoque:", err)
		return 1
	}

	fmt.Fprintln(out, "Conferência simples: produtos.quantidade x soma dos saldos das obras")
	fmt.Fprintln(out, "(Só pega banco migrado pela metade ou mexido depois: logo após a migração as duas batem por construção.)")
	for _, d := range divergences {
		status := ""
		if !d.Active {
			status = " [removido]"
		}
		fmt.Fprintf(out, "  #%d %s (%s)%s: produtos.quantidade = %s, soma dos saldos = %s, diferença = %s\n",
			d.MaterialID, d.Name, d.Unit, status,
			utils.FormatQuantity(d.OldQuantity),
			utils.FormatQuantity(d.SiteTotal),
			utils.FormatQuantity(d.SiteTotal-d.OldQuantity))
	}
	fmt.Fprintf(out, "%d material(is) conferido(s), %d divergência(s).\n", checked, len(divergences))

	if len(divergences) > 0 {
		fmt.Fprintln(out, "Num banco já em uso, divergência é esperada: entradas e saídas depois da migração mudam só os saldos.")
		return 1
	}
	return 0
}

// writeSnapshotCSV salva o retrato de antes da migração, para ficar como
// registro junto da cópia do banco. Usa ";" como o CSV das movimentações.
func writeSnapshotCSV(path string, snapshot services.StockSnapshot) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	writer := csv.NewWriter(file)
	writer.Comma = ';'
	_ = writer.Write([]string{"id", "nome", "quantidade"})
	for _, m := range snapshot.Materials {
		_ = writer.Write([]string{strconv.Itoa(m.ID), m.Name, strconv.FormatFloat(m.Quantity, 'f', -1, 64)})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
