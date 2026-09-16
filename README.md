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

As contas padrão são criadas automaticamente na primeira execução, pelo seed. As senhas **não** ficam no código nem neste arquivo.

| Conta | Email | Role | Variável de ambiente da senha |
|---|---|---|---|
| SuperAdmin | `superadmin@gmail.com` | `superadmin` | `SEED_SUPERADMIN_PASSWORD` |
| Administrador | `admin@gmail.com` | `admin` | `SEED_ADMIN_PASSWORD` |
| Gestor | `gerente@gmail.com` | `gestor` | `SEED_GERENTE_PASSWORD` |
| Solicitante | `usuario@gmail.com` | `solicitante` | `SEED_USUARIO_PASSWORD` |

Os cargos disponíveis são Administrador (`admin`), Gestor (`gestor`), Almoxarife (`almoxarife`), Solicitante (`solicitante`) e Auditor (`auditor`). O que cada um pode fazer está em `backend/cmd/permissions.go` e no `INFORMACOES.MD`. Bancos antigos são migrados sozinhos na inicialização: `gerente` vira `gestor` e `basico` vira `solicitante`.

Se a variável não estiver definida, o seed gera uma senha aleatória e a imprime **uma única vez** no log de inicialização.

Para trocar a senha de uma conta já existente:

```bash
go run ./backend/cmd reset-password <email> <nova-senha>
```

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

* `/movimentacoes` — Histórico de movimentações

* `/usuarios` — Administração de usuários

## Autor

Desenvolvido por [Matheus Henrique Coelho Lopes](https://github.com/coelho7z7).
