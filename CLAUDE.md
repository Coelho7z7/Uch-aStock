# CLAUDE.md — UchôaStock

Instruções para o Claude Code trabalhar neste repositório. **Leia antes de qualquer alteração.**

Projeto de estudo de Go (backend em Go puro + frontend HTML/CSS/JS + SQLite). Autor: Coelho.
Repositório: <https://github.com/Coelho7z7/GoStock>

**Domínio:** sistema de controle de estoque **de materiais de obra** — cadastrar materiais, acompanhar quantidade, registrar entradas e saídas, e avisar o que está prestes a acabar. **Não é um sistema de vendas:** não existe preço, venda, faturamento nem PDV. Se uma tarefa parecer pedir isso de volta, confirme antes.

---

## 1. Regras inegociáveis

Estas cinco regras valem sempre. Na dúvida entre cumprir uma regra e entregar mais rápido, **cumpra a regra**.

### 1.1 Código em inglês

Identificadores Go — funções, tipos, campos de struct, variáveis, nomes de arquivo — sempre em **inglês**.

```go
// Certo
func RegisterStockExitWeb(materialID int, quantity int, userID int) error

// Errado
func RegistrarSaidaEstoqueWeb(materialID int, quantidade int, usuarioID int) error
```

### 1.2 Banco de dados só com permissão explícita

**Nunca** executar, sem o Coelho autorizar na mensagem:

- `ALTER TABLE`, `DROP`, `TRUNCATE`, `CREATE TABLE`
- `UPDATE` ou `DELETE` rodado à mão no banco
- qualquer alteração em `backend/data/uchoastock.db`
- qualquer migração nova em `database.CreateTables()`
- apagar, mover ou recriar o arquivo do banco

Se uma tarefa exigir mudança de schema: **pare, explique o que precisa mudar e por quê, e espere o OK.**

Ler (`SELECT`) é livre. Escrever não.

### 1.3 UI em português

Tudo que o usuário final lê é em **português**:

- textos de HTML, labels, botões, títulos
- mensagens de erro retornadas ao usuário: `fmt.Errorf("estoque insuficiente para %s", name)`
- mensagens de log e do CLI

### 1.4 Railway só com permissão explícita

**Sempre perguntar antes** de qualquer operação no Railway: `create-deployment`, `redeploy`, `accept-deploy`, `restart-service`, `set-variables`, mexer em volume ou domínio.

Ler status, logs e métricas é livre. Mudar qualquer coisa exige OK.

Atenção especial ao volume persistente: em produção o banco vive em `DB_PATH` (ex.: `/data/uchoastock.db`). Mexer nisso apaga dados reais.

### 1.5 Teste de mesa obrigatório

Antes de dizer que algo está pronto:

1. **Rastrear o fluxo linha a linha** — entrada do usuário → handler → service → SQL → resposta. Conferir os casos de borda: valor zero, valor negativo, campo vazio, material inexistente, estoque insuficiente, usuário sem permissão.
2. Rodar `go build ./...`
3. Rodar `go test ./...`
4. Rodar `go vet ./...`

Se algum passo falhar, **dizer que falhou e colar a saída**. Nunca reportar "funcionando" sem ter verificado.

---

## 2. Comandos

```bash
# Subir o servidor (http://localhost:8080)
go run ./backend/cmd

# Porta alternativa
PORT=3000 go run ./backend/cmd

# Testes
go test ./...

# Qualidade
go build ./...
go vet ./...
gofmt -l .        # lista arquivos mal formatados
gofmt -w .        # formata
```

### CLI administrativo

Cada comando executa e encerra o processo, sem subir o servidor web:

```bash
go run ./backend/cmd reset-password <email> <nova-senha>
go run ./backend/cmd create-user "<nome>" <email> "<senha>" <admin|gestor|almoxarife|solicitante|auditor>
go run ./backend/cmd rename-user <email> "<novo-nome>"
go run ./backend/cmd change-email <email-atual> <novo-email>
```

`create-user` não aceita o cargo `superadmin` — ver seção 5.

---

## 3. Arquitetura — a regra mais importante

O projeto é em camadas. **Respeite a direção das dependências:**

```
frontend/  (HTML + CSS + JS)
    ↓ HTTP
backend/cmd/        handlers HTTP, rotas, sessão, autorização
    ↓ chama
backend/services/   regra de negócio e TODO o SQL
    ↓ usa
backend/database/   conexão e schema
    ↓
SQLite (backend/data/uchoastock.db)
```

**Handler nunca escreve SQL.** Se um handler em `backend/cmd/` precisa de dados, ele chama uma função de `backend/services/`. Sem exceção.

Responsabilidade de cada camada:

| Camada | Faz | Não faz |
|---|---|---|
| `cmd/` | ler form/JSON, validar sessão e permissão, renderizar template, montar resposta | SQL, cálculo de negócio |
| `services/` | regra de negócio, transações, todo o SQL | renderizar HTML, ler `*http.Request` |
| `models/` | structs de dados | lógica |
| `database/` | conectar, criar tabelas, migrar | consultas de negócio |
| `utils/` | validação e leitura de entrada, sem estado | acessar banco |

Ao criar um arquivo novo, siga o padrão de nomes já existente: `material_create.go`, `material_query.go`, `stock_service.go`, `user_auth.go`.

---

## 4. Fronteira de idioma — leia com atenção

O código é em inglês, **mas o banco, as rotas e o JSON são em português**. Isso é intencional. **Não "corrija" para inglês** — quebra o banco e o frontend.

| Onde | Idioma | Exemplo |
|---|---|---|
| Identificadores Go | **inglês** | `RegisterStockExitWeb`, `LowStockThreshold`, `userID` |
| Comentários Go | **português** | `// Valida se o estoque é suficiente antes de debitar.` |
| Tabelas e colunas SQL | **português — congelado** | `produtos`, `nome`, `quantidade`, `ativo` |
| Rotas HTTP | **português — congelado** | `/materiais`, `/alterar-material`, `/estoque`, `/movimentacoes` |
| Tags JSON | **português — congelado** | `` Name string `json:"nome"` `` |
| Valores em texto no banco | **português — congelado** | `"ENTRADA"`, `"SAIDA"`, `"ATUALIZACAO"`, `"basico"`, `"superadmin"` |
| Textos de UI e erros ao usuário | **português** | `"Acesso negado"`, `"material não encontrado"` |

⚠️ A tabela continua se chamando **`produtos`** no banco, mesmo o sistema falando de "materiais" — renomear tabela é mudança de schema e cai na regra 1.2. No código Go o tipo é `Material`; só o SQL mantém o nome antigo.

Renomear qualquer item marcado como **congelado** é uma mudança de banco → cai na regra 1.2 e precisa de permissão.

Comentários: os novos ficam em português. Os arquivos antigos em `services/` têm comentários em inglês — **não reescreva em massa**, só siga o idioma do arquivo que estiver editando quando a mudança for pequena.

---

## 5. Permissões e cargos

O acesso é **por ação**, não por nome de cargo. A fonte única da verdade é o map `rolePermissions` em `backend/cmd/permissions.go`, e toda checagem passa por `can(usuario, permissao)`. **Nunca** escreva `if user.Role == "admin"` num handler: use a permissão da ação. A matriz completa está em `INFORMACOES.MD`, seção PERMISSÕES.

| Cargo | Pode |
|---|---|
| `superadmin` | tudo (`can` sempre true); identidade **reservada** a `superadmin@gmail.com` |
| `admin` | todas as permissões, inclusive `obras.todas`: age em qualquer obra e usa "Todas as obras" |
| `gestor` | todas, menos `obras.todas`: movimenta e gerencia **só a obra vinculada a ele**; em usuários, só cria e edita almoxarife e solicitante da própria obra |
| `almoxarife` | edita o catálogo de materiais, movimenta estoque da própria obra, vê e exporta todas as movimentações |
| `solicitante` | consulta; no histórico vê **só as próprias** movimentações |
| `auditor` | só leitura: vê e exporta todas as movimentações |

A permissão diz **o que** a pessoa faz; a obra diz **onde**. Cada usuário que não é admin tem **uma** obra (tabela `usuario_obras`; a regra de uma só é garantida em `services.setUserSiteTx`) e entra no sistema por ela. As regras que juntam as duas coisas moram em `backend/cmd/authorization.go` (`canMoveStockAt`, `canEditSite`) e as da gestão de usuários em `permissions.go` (`canManageUser`, `canAssignRole`, `canAssignSite`).

Os cargos antigos `gerente` e `basico` são migrados na inicialização para `gestor` e `solicitante`. Todo `INSERT` em `usuarios` informa o `role`: o DEFAULT da coluna ainda é `'basico'`.

Regras que o código garante e que **não devem ser afrouxadas**:

- Só `superadmin@gmail.com` pode ter o cargo `superadmin`. `database.CreateTables()` corrige isso a cada inicialização, mesmo que alguém mexa direto no banco.
- `create-user` não aceita `superadmin` como cargo.
- Autorização é sempre checada **no servidor** (`requirePermission`, `can`, `canMoveStockAt`, `canEditSite`, `canManageUser`). Os templates escondem botões e abas por flags de permissão (`CanManageUsers`, `CanEditMaterial`...), mas isso é só UX: o POST confere de novo.

---

## 6. Frontend — design system

O CSS é um design system com tokens. **Nunca escreva valor solto**: cor,
espaço, tamanho de fonte e raio saem sempre de `var(--token)`, definidos em
`frontend/css/tokens.css`.

### ⚠️ O UchôaStock é um sistema de TEMA ESCURO

Fundo quase preto azulado (`#080D18`) com gradiente radial, cartões em
azul-ardósia (`#111A2B`), sidebar `#0B1220` e texto claro (`#E5EDF8`).
**Não converta para tema claro.** As cores semânticas seguem a lógica de
tema escuro: o texto é o tom claro (`#9FE3B0`, `#FCA5A5`, `#FBBF62`) e o
fundo é a mesma cor translúcida sobre a superfície.

Links e destaques usam `--color-brand-light` (`#8AC7FF`), não
`--color-brand` — o azul cheio da marca só serve como preenchimento de
botão, porque some contra o fundo escuro.

| Arquivo | Responsabilidade |
|---|---|
| `tokens.css` | `:root` — cores, espaçamento, tipografia, raios, sombras, camadas |
| `base.css` | reset, tipografia base, foco e acessibilidade |
| `components.css` | botões, inputs, tabelas, cards, modais, badges, toasts |
| `layout.css` | sidebar, topbar, conteúdo, navegação mobile |
| `pages.css` | o que é específico de uma tela |
| `login.css` | tela de login (carregada só por ela) |

Regras:

- **Mobile-first.** A base do CSS é o celular; `@media (min-width: …)` acrescenta. Não escreva desktop primeiro e corrija no mobile — foi assim que o projeto acumulou 305 `!important`.
- **Breakpoints: 768 / 1024 / 1280.** O de 768 é acoplado ao `app.js` (`window.innerWidth >= 768`); mudar um exige mudar o outro.
- **`!important` é proibido**, com uma exceção: o bloco `prefers-reduced-motion` em `base.css`.
- **Alvo de toque mínimo 44px** (`var(--touch-target)`) em botões e links — o sistema é usado de luva, em obra.
- **Contraste WCAG AA**: 4.5:1 para texto, 3:1 para bordas de input e controles.

### Classes que não podem ser renomeadas

O `app.js` e os templates dependem delas pelo nome. Renomear quebra em runtime, **sem erro de compilação**:

- Montadas pelo template: `.activity-ENTRADA` / `.activity-SAIDA` / `.activity-ATUALIZACAO` (de `activity-{{ .Type }}`) `.stock-quantity.empty` / `.low` / `.normal` (de `{{ .StockStatus }}`) e `.site-status-ANDAMENTO` / `.site-status-PARALISADA` / `.site-status-CONCLUIDA` (de `site-status-{{ .Status }}`).
- Consultadas pelo JS: `.sidebar` · `.content table` · `.cards .card` · `.mobile-menu-button` · `.mobile-sidebar-overlay` · `.login-card` · `.form-success` · `.form-error` · `[data-confirm]` · `[data-confirm-optional]` · `[data-autosubmit]` · `[data-live-search]` · `[data-live-region]` · `[data-searchable]` · `[data-site-field]` · `[data-open]`.
- **Busca em tempo real:** todo formulário de filtro com `data-live-search` busca enquanto se digita, trocando só os elementos `data-live-region` (tabela, paginação, contadores) pelos da resposta do servidor. Por isso, **botão dentro de uma região nunca recebe ouvinte direto** (`querySelectorAll(...).forEach(addEventListener)`): use delegação no `document`, senão o botão para de funcionar depois da primeira busca.
- Aplicadas pelo JS: todas as `gs-*` · `.modal-closing` · `.mobile-menu-open`.
- Os modais abrem pelo atributo **`hidden`**, não por classe. Por isso existe a regra `.modal-create[hidden] { display: none }` — sem ela os modais nascem abertos.

Antes de renomear qualquer classe, confira se ela aparece em `frontend/js/app.js`.

---

## 7. Estoque e movimentações

Entradas e saídas mexem em dados que não podem ficar inconsistentes.

- **Saldo é por obra.** A quantidade mora em `saldos` (material + obra), não em `produtos.quantidade` — essa coluna é do tempo de um estoque só e não deve ser lida nem gravada. Toda entrada e saída acontece numa obra específica e grava `movimentacoes.obra_id`. Obra concluída não aceita movimentação.
- **Sempre em transação.** Alterar o saldo e registrar a movimentação acontecem dentro de um único `tx`, com `defer tx.Rollback()`. Ver `AddStockWeb` e `RegisterStockExitWeb` em `backend/services/stock_service.go` como referência.
- **Estoque nunca fica negativo.** Validar a quantidade disponível antes de debitar.
- **Quantidade sempre maior que zero** em entrada e saída.
- **Toda mudança de quantidade gera movimentação.** Saída sem registro em `movimentacoes` é bug.
- **Material "acabando"** é definido por `services.LowStockThreshold` (hoje 10). Use a constante, nunca o número solto.
- SQL sempre com placeholder `?`. **Nunca** concatenar string em query.

---

## 8. Segurança

**O repositório é público.** Tudo que entra num commit fica visível para qualquer pessoa, para sempre — apagar depois não remove dos commits antigos.

- **Nunca commitar:** `.db`, `.env`, `dev.sh`, senha, token, chave de API, credencial de produção.
- **Nunca commitar binário compilado (`.exe`) nem arquivo `.patch`.** Já aconteceu no histórico deste projeto: um `cmd.exe` e um `credenciais-fix.patch` carregavam a senha do admin embutida. Binário não parece arquivo com segredo, mas é.
- Senha sempre com hash (`golang.org/x/crypto/bcrypt`). Nunca gravar nem logar senha em texto puro.
- Senha de seed vem sempre de variável de ambiente (`SEED_*_PASSWORD`), nunca literal no código — ver `seedPassword` em `user_seed.go`.
- Sessão validada por `token_hash` no banco, com expiração.
- Não colocar credencial neste arquivo nem em código, nem de teste.
- Ao mostrar erro ao usuário, não vazar detalhe interno do banco.

### Rodar localmente

Use `./dev.sh` (ignorado pelo git) — sobe o servidor com contas de desenvolvimento.

⚠️ **Go não recarrega sozinho.** Depois de mexer em qualquer `.go`, é obrigatório parar o servidor (Ctrl+C) e subir de novo — o processo em execução continua com o binário antigo. Isso é especialmente perigoso depois de uma migração: o código velho consulta colunas que não existem mais e o sistema quebra com erro genérico. Mudança só em HTML/CSS/JS não precisa de restart, só refresh.

Se a porta 8080 ficar presa, descubra o processo e encerre:

```bash
netstat -ano | grep ":8080" | grep LISTENING
taskkill //F //PID <pid>
```

O seed **só cria conta que ainda não existe**, então definir `SEED_*_PASSWORD` não tem efeito sobre conta já criada. Para trocar a senha de uma conta existente:

```bash
go run ./backend/cmd reset-password <email> '<nova-senha>'
```

---

## 9. Como trabalhar neste repositório

Este é um projeto de **estudo**. O Coelho começou em Go há pouco tempo. O objetivo é ele entender o código, não só o código funcionar.

- **Explique o porquê**, não só o quê. Ao introduzir algo novo (transação, interface, goroutine), explique em uma ou duas frases o que é e por que serve ali.
- **Mudanças pequenas e focadas.** Uma tarefa por vez.
- **Não refatore o que não foi pedido.** Viu algo melhorável fora do escopo? Comente ao final, não altere.
- **Não adicione dependência nova sem perguntar.** O projeto é propositalmente enxuto: biblioteca padrão + `x/crypto` + driver SQLite.
- **Não crie abstração antecipada.** Código simples e legível vale mais que código "esperto".
- **Prefira editar arquivo existente** a criar arquivo novo.
- **Não crie README/doc novo** a menos que peçam.
- Ao terminar, diga o que mudou, em quais arquivos e o que foi verificado.

---

## 10. Git

Repositório: <https://github.com/Coelho7z7/GoStock> (remoto `origin`), branch principal `main`.

**Antes de começar qualquer tarefa**, consultar o estado do repositório:

```bash
git status
git log --oneline -10
git diff
```

- **Não commitar nem dar push sem o Coelho pedir.**
- Mensagem de commit em **português**, curta e no imperativo, com prefixo:
  `feat:` funcionalidade · `fix:` correção · `chore:` manutenção · `docs:` documentação · `refactor:` reestruturação · `test:` testes
  Exemplo: `fix: impede venda com estoque insuficiente no PDV`
- Conferir `git status` antes de commitar para não subir `.db`, binário ou arquivo temporário.
- Nunca usar `git push --force` em `main`.

---

## 11. Checklist antes de entregar

- [ ] Identificadores em inglês, comentários e UI em português
- [ ] Handler não tem SQL; regra de negócio está em `services/`
- [ ] Nada de schema/banco alterado sem permissão
- [ ] Operação de venda/estoque dentro de transação
- [ ] `go build ./...` passou
- [ ] `go test ./...` passou
- [ ] `go vet ./...` sem apontamentos
- [ ] `gofmt -l .` sem saída
- [ ] Teste de mesa feito, incluindo casos de borda
- [ ] Nenhuma credencial ou `.db` no diff


## 12. Sempre após terminar uma demanda de uma task por completo, me pergunte se pode commitar e fazer deploy.

## 13. Quando fizer commits, não se insira como contribuinte.