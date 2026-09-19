package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// prepareProjectDirectory garante que o processo esteja rodando com o
// diretório de trabalho na raiz do projeto (onde ficam as pastas
// frontend/ e backend/), independente de onde o binário foi chamado.
func prepareProjectDirectory() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "frontend", "html", "index.html")); err == nil {
			return os.Chdir(dir)
		}
		// Pasta com o nome do repositório (o git clone cria "UchoaStock"),
		// para rodar a partir da pasta de cima dele.
		nestedProject := filepath.Join(dir, "UchoaStock")
		if _, err := os.Stat(filepath.Join(nestedProject, "frontend", "html", "index.html")); err == nil {
			return os.Chdir(nestedProject)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return fmt.Errorf("frontend/html/index.html não encontrado")
		}
		dir = parent
	}
}
