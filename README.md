# UchôaStock: Sistema de Controle de Estoque em Go

UchôaStock é um sistema de controle de estoque de materiais de obra, desenvolvido para acompanhar o que entra, o que sai e o que está prestes a acabar, unindo um backend em Go a uma interface web leve e direta.

## Overview

Obras costumam perder tempo e material por falta de controle: ninguém sabe quanto cimento sobrou nem quando o vergalhão vai acabar. O UchôaStock resolve isso oferecendo um sistema centralizado onde é possível cadastrar materiais, acompanhar quantidades, registrar entradas e saídas, e ser avisado do que está prestes a acabar.

### Key Features

* **Dashboard:** visão geral do estoque, com alerta dos materiais prestes a acabar

* **Materiais:** cadastro, edição e remoção de materiais de obra

* **Controle de Estoque:** registro de entradas e saídas por material

* **Movimentações:** histórico completo de entradas, saídas e alterações

* **Login e Permissões:** autenticação de usuários e controle de acesso

## Architecture

O UchôaStock é organizado em módulos:

1. **Backend (Go):** lógica de negócio, rotas, autenticação e regras do sistema

2. **Frontend (HTML/CSS/JS):** interface web e interações do sistema

3. **Banco de Dados (SQLite):** persistência dos dados do sistema

## Requirements

* Go 1.26+

* SQLite

* Navegador web atualizado para acessar a interface

## Usuários de teste

As contas padrão são criadas automaticamente na inicialização, pelo seed, quando ainda não existem. As senhas **não** ficam no código nem neste arquivo: cada uma vem de uma variável de ambiente.

| Conta | Email | Role | Variável de ambiente da senha |
|---|---|---|---|
| SuperAdmin | `superadmin@gmail.com` | `superadmin` | `SEED_SUPERADMIN_PASSWORD` |
| Administrador | `admin@gmail.com` | `admin` | `SEED_ADMIN_PASSWORD` |
| Gestor | `gerente@gmail.com` | `gestor` | `SEED_GESTOR_PASSWORD` |
| Almoxarife | `almoxarife@gmail.com` | `almoxarife` | `SEED_ALMOXARIFE_PASSWORD` |
| Solicitante | `solicitante@gmail.com` | `solicitante` | `SEED_SOLICITANTE_PASSWORD` |
| Auditor | `auditor@gmail.com` | `auditor` | `SEED_AUDITOR_PASSWORD` |
| Usuário (somente leitura) | `usuario@gmail.com` | `basico` | `SEED_USUARIO_PASSWORD` |

Os cargos disponíveis são Administrador (`admin`), Gestor (`gestor`), Almoxarife (`almoxarife`), Solicitante (`solicitante`) e Auditor (`auditor`). O que cada um pode fazer está em `backend/cmd/permissions.go` e no `INFORMACOES.MD`. A conta `usuario@gmail.com` é de demonstração: vê as telas, mas não altera nada. Bancos antigos são migrados sozinhos na inicialização: `gerente` vira `gestor` e `basico` vira `solicitante` (menos a conta de demonstração).

Se a variável de uma conta não estiver definida, **a conta não é criada**, e o log de inicialização diz qual variável falta. Nenhuma senha é escrita no log.

Cada pessoa troca a própria senha em **Minha senha**, no rodapé da barra lateral (é pedida a senha atual). Para trocar a senha de uma conta pela linha de comando:

```bash
go run ./backend/cmd reset-password <email> <nova-senha>
```

Trocar a senha encerra as sessões abertas daquela conta em outros aparelhos.

## Como executar

```bash
go run ./backend/cmd
```

Depois, acesse `http://localhost:8080` no navegador.

Para usar outra porta local, defina a variável `PORT` antes de iniciar o servidor.

No Railway, a aplicação utiliza automaticamente a porta fornecida pela variável `PORT` do ambiente.

### Rotas principais

* `/` — Login

* `/login` — Autenticação

* `/logout` — Encerramento da sessão

* `/dashboard` — Dashboard

* `/materiais` — Cadastro e listagem de materiais

* `/alterar-material` — Alteração e remoção de materiais

* `/estoque` — Entradas e saídas de estoque

* `/fornecedores` — Cadastro de fornecedores

* `/inventarios` — Inventário: contagem física, diferenças e ajuste aprovado

* `/movimentacoes` — Histórico de movimentações

* `/solicitacoes` — Solicitações de material: lista com filtro e busca

* `/solicitacoes/nova` — Nova solicitação (materiais e quantidades)

* `/solicitacoes/{id}` — Detalhe da solicitação: aprovar, rejeitar, atender e cancelar

* `/usuarios` — Administração de usuários

* `/minha-senha` — Troca da própria senha (qualquer pessoa logada)

## Autor

Desenvolvido por [Matheus Henrique Coelho Lopes](https://github.com/coelho7z7).
