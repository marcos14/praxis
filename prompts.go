package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed defaults/*.md
var promptsPadrao embed.FS

var nomesPrompts = []string{"executor.md", "corretor.md", "revisor.md", "inicializador.md"}

// carregarPrompt prefere a versao personalizada em automacao/prompts/;
// na falta dela, usa o padrao embutido no binario.
func carregarPrompt(raiz, nome string) (string, error) {
	if b, err := os.ReadFile(filepath.Join(dirPrompts(raiz), nome)); err == nil {
		return string(b), nil
	}
	b, err := promptsPadrao.ReadFile("defaults/" + nome)
	if err != nil {
		return "", fmt.Errorf("prompt %s nao existe: %w", nome, err)
	}
	return string(b), nil
}

// materializarPrompts grava os prompts padrao em automacao/prompts/ (sem
// sobrescrever personalizacoes existentes).
func materializarPrompts(raiz string) error {
	if err := os.MkdirAll(dirPrompts(raiz), 0o755); err != nil {
		return err
	}
	for _, nome := range nomesPrompts {
		destino := filepath.Join(dirPrompts(raiz), nome)
		if _, err := os.Stat(destino); err == nil {
			continue
		}
		b, err := promptsPadrao.ReadFile("defaults/" + nome)
		if err != nil {
			return err
		}
		if err := os.WriteFile(destino, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}
