package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	raiz := t.TempDir()
	if err := os.MkdirAll(filepath.Join(raiz, dirAutomacao), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := configPadrao()
	cfg.Plano = "docs/PLANO.md"
	cfg.AddDirs = []string{`C:\projetos\outro_repo`}
	cfg.Gates = []Gate{{Nome: "go", Dir: ".", Comandos: []string{"go build ./...", "go test ./..."}}}
	cfg.GatesExtra = []GateExtra{{Nome: "integration", Comandos: []string{"go test -tags=integration ./..."}}}
	if err := salvarConfig(raiz, cfg); err != nil {
		t.Fatalf("salvarConfig: %v", err)
	}
	lida, err := carregarConfig(raiz)
	if err != nil {
		t.Fatalf("carregarConfig: %v", err)
	}
	if !reflect.DeepEqual(cfg, lida) {
		t.Fatalf("round-trip divergente:\noriginal: %+v\nlida:     %+v", cfg, lida)
	}
}

func TestCarregarConfigInexistente(t *testing.T) {
	if _, err := carregarConfig(t.TempDir()); err == nil {
		t.Fatal("esperava erro para config inexistente")
	}
}

func TestResolverDir(t *testing.T) {
	if got := resolverDir(`C:\proj`, "."); got != `C:\proj` {
		t.Fatalf("dir '.': %q", got)
	}
	if got := resolverDir(`C:\proj`, `C:\outro`); got != `C:\outro` {
		t.Fatalf("dir absoluto: %q", got)
	}
	if got := resolverDir(`C:\proj`, "sub"); got != filepath.Join(`C:\proj`, "sub") {
		t.Fatalf("dir relativo: %q", got)
	}
}

func TestContemArquivo(t *testing.T) {
	nomes := []string{"internal/app.go", `PLANO.md`, "automacao/fases.csv"}
	if !contemArquivo(nomes, "PLANO.md") {
		t.Fatal("deveria achar PLANO.md")
	}
	if contemArquivo(nomes, "OUTRO.md") {
		t.Fatal("nao deveria achar OUTRO.md")
	}
}
