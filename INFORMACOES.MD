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
- material_handler.go — materiais
- stock_handler.go — estoque
- movement_handler.go — movimentações
- user_handler.go — usuários
- cli.go — terminal

services/
- material_create.go — cadastro de materiais
- material_query.go — consultas de materiais e alerta de estoque baixo
- stock_service.go — entradas e saídas
- movement_service.go — movimentações
- user_create.go — cadastro de usuários
- user_auth.go — autenticação
- user_query.go — consultas de usuários
- user_management.go — gerenciamento de usuários
- user_seed.go — usuários iniciais
- password_reset.go — troca de senha, nome e email pelo CLI

models/
- material.go
- movement.go
- user.go

database/
- database.go

utils/
- input.go
- validation.go


FRONTEND

html/
- index.html — login
- dashboard.html — dashboard
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
