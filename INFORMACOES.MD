UchôaStock

Sistema de controle de estoque de materiais de obra.
Sem venda, sem preço, sem PDV: só entrada, saída e alerta do que está acabando.

BACKEND

cmd/
- main.go — servidor
- project.go — projeto
- auth_handler.go — login
- authorization.go — middleware withUser, acesso negado e regras por obra
- permissions.go — permissões por ação: o que cada cargo pode fazer
- session.go — sessões
- dashboard_handler.go — dashboard
- site_handler.go — obras
- site_scope.go — obra selecionada no topo (seletor e troca de obra)
- material_handler.go — materiais
- stock_handler.go — estoque
- movement_handler.go — movimentações
- user_handler.go — usuários

services/
- material_create.go — cadastro de materiais
- material_query.go — consultas de materiais e alerta de estoque baixo
- stock_service.go — entradas e saídas
- movement_service.go — movimentações
- user_auth.go — autenticação
- user_query.go — consultas de usuários
- user_management.go — gerenciamento de usuários
- user_seed.go — usuários iniciais
- password_reset.go — troca de senha, nome e email pelo CLI
- site_create.go — cadastro e edição de obras
- site_query.go — consultas de obras
- session_site.go — obra guardada na sessão

models/
- material.go
- movement.go
- site.go
- user.go

database/
- database.go

utils/
- validation.go


FRONTEND

html/
- index.html — login
- dashboard.html — dashboard
- sites.html — obras
- site_switcher.html — seletor de obra da topbar (incluído pelas telas internas)
- materials.html — materiais
- edit_material.html — editar material
- stock.html — estoque
- movements.html — movimentações
- users.html — usuários
- access_denied.html — acesso negado

css/  (design system — a ordem dos <link> importa, é cascata)
- tokens.css — :root: cores, espaçamento, tipografia, raios, sombras
- base.css — reset, tipografia, foco/acessibilidade
- components.css — botões, inputs, tabelas, cards, modais, badges, toasts
- layout.css — sidebar, topbar, conteúdo, navegação mobile
- pages.css — o específico de cada tela
- login.css — tela de login (só ela carrega)
- fonts/ — Inter auto-hospedada (.woff2)

Telas internas: tokens, base, components, layout, pages
Login:          tokens, base, components, login

Regras do CSS:
- Nada de valor solto. Sempre var(--token).
- Mobile-first: a base é o celular, @media (min-width) acrescenta.
- Breakpoints: 768 / 1024 / 1280 (o de 768 casa com o app.js).
- !important só no bloco prefers-reduced-motion.

js/
- app.js


BANCO

backend/data/uchoastock.db

SQLite.

A tabela continua se chamando "produtos" (renomear exige migração).
No código Go o tipo é Material.

A coluna usuarios.role tem DEFAULT 'basico', um cargo que não existe mais
(virou 'solicitante'). O SQLite não troca o DEFAULT de uma coluna sem
recriar a tabela, então a regra é: todo INSERT em usuarios informa o role
explicitamente. Nunca conte com o DEFAULT.

A tabela "obras" guarda o almoxarifado central (tipo CENTRAL) e as obras
(tipo OBRA). No código Go o tipo é Site.

O catálogo (produtos) é um só; o saldo é por obra, na tabela "saldos"
(produto_id + obra_id). produtos.quantidade é do tempo de um estoque só:
foi copiada para o central na migração e não é mais lida nem gravada.
Toda entrada e saída grava movimentacoes.obra_id; atualização de cadastro
fica sem obra (NULL).


PERMISSÕES

O acesso é por ação, não por nome de cargo. backend/cmd/permissions.go tem
a lista de permissões e o map rolePermissions (cargo -> permissões), que é
a fonte única da verdade. Toda checagem passa por can(usuario, permissao).
Criar um cargo = uma linha nesse map + o cargo em services.validRoles.

Cargos (valor gravado no banco entre parênteses):

| Permissão               | Admin | Gestor | Almoxarife | Solicitante | Auditor |
|-------------------------|:-----:|:------:|:----------:|:-----------:|:-------:|
| material.editar         |   x   |        |            |             |         |
| material.remover        |   x   |        |            |             |         |
| estoque.movimentar      |   x   |   x    |     x      |             |         |
| requisicao.criar        |   x   |   x    |     x      |      x      |         |
| requisicao.aprovar      |   x   |   x    |            |             |         |
| movimentacoes.ver_todas |   x   |   x    |     x      |             |    x    |
| movimentacoes.exportar  |   x   |   x    |     x      |             |    x    |
| usuarios.gerenciar      |   x   |   x    |            |             |         |
| obras.gerenciar         |   x   |   x    |            |             |         |
| obras.reabrir           |   x   |        |            |             |         |
| obras.todas             |   x   |        |            |             |         |

Administrador (admin), Gestor (gestor), Almoxarife (almoxarife),
Solicitante (solicitante), Auditor (auditor).

- superadmin pode tudo (can() devolve true) e continua reservado a
  superadmin@gmail.com: não aparece no dropdown e não pode ser alterado,
  removido nem ter a senha trocada por outra pessoa.
- Todo cargo vê dashboard, materiais e estoque.
- requisicao.* só existem no map: a requisição de material ainda não foi
  feita.
- O catálogo de materiais é da empresa toda e é exclusivo do admin (e do
  superadmin): material.editar cadastra e edita nome, unidade e limite
  mínimo; material.remover remove. Gestor e almoxarife só movimentam o
  estoque (entrada e saída) na própria obra.
- Material com saldo > 0 em qualquer obra não pode ser removido, nem pelo
  admin: a mensagem diz em quantas obras ainda há saldo.
- O limite mínimo (produtos.limite_minimo) é um campo único do material,
  igual para todas as obras. Por isso também fica só com o admin. Se um
  dia precisar ser por obra, é mudança de schema (coluna em saldos).
- Sem movimentacoes.ver_todas (o solicitante), o histórico, a exportação e
  a atividade recente do dashboard mostram só o que a pessoa registrou.
- Sem movimentacoes.exportar, /movimentacoes/exportar responde 403.

Obra: a permissão diz O QUE a pessoa faz; a obra diz ONDE.
- obras.todas (só admin) age em qualquer obra e usa "Todas as obras".
- Sem ela, estoque.movimentar e obras.gerenciar valem só na obra vinculada
  ao usuário (canMoveStockAt, canEditSite em authorization.go).
- Cadastrar obra exige obras.gerenciar + obras.todas (admin).

Gestão de usuários (usuarios.gerenciar), regras em permissions.go:
- O gestor só cria e edita Almoxarife e Solicitante (manageableRoles).
- O gestor não altera, não remove e não troca a senha de admin, de outro
  gestor, de auditor nem de ninguém de outra obra; e só vincula à própria
  obra (canManageUser, canAssignRole, canAssignSite).
- Qualquer um troca a própria senha.

A tela esconde botões e abas pelas flags de permissão (CanManageUsers,
CanEditMaterial, CanRemoveMaterial, CanExport, CanManageSites...). Isso é
só UX: o backend confere a permissão em todo POST.

Migração: na inicialização, gerente vira gestor e basico vira solicitante
(idempotente, em database.CreateTables).


OBRAS

/obras?busca=texto&situacao=ANDAMENTO|PARALISADA|CONCLUIDA
Cadastrar: admin. Editar, paralisar, retomar e encerrar: quem tem
obras.gerenciar, na obra ao seu alcance (ver PERMISSÕES). Obra não é
apagada: vira CONCLUIDA.

Situação (regras em services/site_create.go, checkSiteStatusChange):
- ANDAMENTO  -> PARALISADA | CONCLUIDA
- PARALISADA -> ANDAMENTO | CONCLUIDA
- CONCLUIDA  -> ANDAMENTO (reabrir: só com obras.reabrir, ou seja, admin)
- Qualquer outra é recusada. Pelos botões, pedir a situação atual também
  ("a obra já está paralisada"); na edição, manter a situação é normal.
- Encerrar exige saldo zero em todos os materiais ativos da obra. A
  mensagem diz quantos ainda têm saldo. Material removido não conta.
- Paralisar, Retomar, Reabrir e Encerrar pedem confirmação na tela.
O almoxarifado central é criado pela migração, é único e fica sempre em
andamento. Nome não pode repetir (sem diferenciar maiúsculas).
Obra concluída é só consulta: não aceita entrada nem saída.


OBRA SELECIONADA

O seletor do topo (POST /obra-atual) guarda a obra em sessoes.obra_id.
Ele tem busca (sem acento e sem diferenciar maiúsculas), montada pelo
app.js em cima do <select data-searchable>.
Dashboard, materiais, estoque e movimentações mostram só essa obra.
"Todas as obras" (NULL) é só para quem tem obras.todas (admin e
superadmin); os outros caem na obra deles ou, sem vínculo, no almoxarifado
central. Entrada e saída exigem uma obra específica, e o
formulário leva obra_id para conferir que a obra não mudou em outra aba.


OBRA DO USUÁRIO

usuario_obras liga cada usuário (que não é admin) a uma obra. É definida
no cadastro e na edição do usuário (o gestor só vincula à obra dele).
Quem não é admin entra no sistema pela obra dele. Gestor e almoxarife
movimentam só essa obra; solicitante e auditor consultam. A aba Obras
mostra a equipe de cada obra.


BUSCA EM TEMPO REAL

Todo filtro com data-live-search busca enquanto se digita e troca só os
elementos data-live-region (tabela, paginação, contadores).


ESTOQUE BAIXO

services.LowStockThreshold = 10
Material com 10 unidades ou menos aparece no painel "Materiais acabando".
O painel mostra os 5 mais críticos, com quem foi o último a movimentar
cada um (LEFT JOIN com movimentacoes + usuarios), e uma linha "e mais N"
quando sobra material fora da lista.


RESUMO DO DIA

services.CountTodayMovements() alimenta o cartão "Hoje" (entradas/saídas).
As datas usam date(...,'localtime') porque o SQLite grava em UTC — sem
isso o dia viraria às 21h no horário do Brasil.


FILTRO DE MOVIMENTAÇÕES

/movimentacoes?tipo=ENTRADA|SAIDA|ATUALIZACAO
Tipo vazio ou desconhecido devolve o histórico completo.
MovementFilter.UserID > 0 limita ao que esse usuário registrou (usado para
quem não tem movimentacoes.ver_todas).


VERIFICAÇÃO DA MIGRAÇÃO DE SALDOS

go run ./backend/cmd verify-stock            (não altera o banco)
go run ./backend/cmd verify-stock --migrate  (ALTERA: só numa CÓPIA)

Sem flag: não roda migração nenhuma. Banco não migrado -> erro, código 1.
Banco migrado -> conferência simples: produtos.quantidade x soma dos
saldos. Ela vale pouco: a migração só LÊ produtos.quantidade para montar
os saldos, então logo depois de migrar as duas batem por construção. Só
pega banco migrado pela metade ou mexido depois (e num banco em uso a
divergência é esperada).

--migrate num banco não migrado (a verificação que vale):
1. mostra um aviso grande de que o banco vai ser alterado;
2. tira um retrato ANTES (quantidade de cada material e quantas
   movimentações de cada tipo) e salva em <banco>.antes-da-migracao.csv;
3. roda as migrações;
4. confere contra o retrato: saldo de cada material igual ao de antes e
   todo no central, produtos.quantidade intacta, nenhum material a mais ou
   a menos, mesmo número de movimentações, entradas e saídas ligadas ao
   central e atualizações sem obra. Código 1 se houver problema.

Uso numa cópia da produção (PowerShell):
$env:DB_PATH="C:\caminho\copia.db"; go run ./backend/cmd verify-stock --migrate

MIGRAÇÕES

database.CreateTables roda tudo (tabelas, colunas, cargos, obras, cópia do
estoque) numa transação só. Se qualquer passo falhar, nada é gravado e a
próxima inicialização tenta de novo do zero. Dentro dela, use sempre tx,
nunca DB: a conexão é uma só (SetMaxOpenConns(1)).


RODAR

./dev.sh

TESTES

go build ./...
go vet ./...
go test ./...
