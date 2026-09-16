UchôaStock

Sistema de controle de estoque de materiais de obra.
Sem venda, sem preço, sem PDV: só entrada, saída e alerta do que está acabando.

BACKEND

cmd/
- main.go — servidor
- project.go — projeto
- auth_handler.go — login
- authorization.go — permissões
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

A tabela "obras" guarda o almoxarifado central (tipo CENTRAL) e as obras
(tipo OBRA). No código Go o tipo é Site.

O catálogo (produtos) é um só; o saldo é por obra, na tabela "saldos"
(produto_id + obra_id). produtos.quantidade é do tempo de um estoque só:
foi copiada para o central na migração e não é mais lida nem gravada.
Toda entrada e saída grava movimentacoes.obra_id; atualização de cadastro
fica sem obra (NULL).


OBRAS

/obras?busca=texto&situacao=ANDAMENTO|PARALISADA|CONCLUIDA
Só admin/superadmin cadastram e editam. Obra não é apagada: vira CONCLUIDA.
O almoxarifado central é criado pela migração, é único e fica sempre em
andamento. Nome não pode repetir (sem diferenciar maiúsculas).
Obra concluída é só consulta: não aceita entrada nem saída.


OBRA SELECIONADA

O seletor do topo (POST /obra-atual) guarda a obra em sessoes.obra_id.
Ele tem busca (sem acento e sem diferenciar maiúsculas), montada pelo
app.js em cima do <select data-searchable>.
Dashboard, materiais, estoque e movimentações mostram só essa obra.
"Todas as obras" (NULL) é só para admin e superadmin; os outros caem no
almoxarifado central. Entrada e saída exigem uma obra específica, e o
formulário leva obra_id para conferir que a obra não mudou em outra aba.


OBRA DO USUÁRIO

usuario_obras liga cada usuário (que não é admin) a uma obra. É definida
por admin no cadastro e na edição do usuário. Quem não é admin entra no
sistema pela obra dele. Gerente movimenta e edita só essa obra; básico só
consulta. A aba Obras mostra a equipe de cada obra.


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


RODAR

./dev.sh

TESTES

go build ./...
go vet ./...
go test ./...
