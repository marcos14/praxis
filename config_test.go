package main

import (
	"encoding/json"
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
	cfg.Projeto = "meu-projeto"
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

func TestCarregarConfigPreencheBlocosNovos(t *testing.T) {
	raiz := t.TempDir()
	if err := os.MkdirAll(filepath.Join(raiz, dirAutomacao), 0o755); err != nil {
		t.Fatal(err)
	}
	bruto := `{"plano":"PLANO.md","modelo":"sonnet","max_budget_usd":25,"timeout_min":120,"max_correcoes":2,"max_ciclos_revisao":1,"versionar_automacao":true,"gates":[]}`
	if err := os.WriteFile(caminhoConfig(raiz), []byte(bruto), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := carregarConfig(raiz)
	if err != nil {
		t.Fatal(err)
	}
	if got := motorParaOperacao(cfg, "executar"); got != "claude" {
		t.Fatalf("motor executar: %q", got)
	}
	if got := modeloParaMotor(cfg, "claude"); got != "sonnet" {
		t.Fatalf("modelo claude deveria respeitar legado: %q", got)
	}
	if got := modeloParaMotor(cfg, "codex"); got != "gpt-5.5" {
		t.Fatalf("modelo codex default inesperado: %q", got)
	}
	if got := esforcoParaMotor(cfg, "claude"); got != "high" {
		t.Fatalf("esforco claude default inesperado: %q", got)
	}
	if got := esforcoParaMotor(cfg, "codex"); got != "high" {
		t.Fatalf("esforco codex default inesperado: %q", got)
	}
	if !cfg.Notificacoes.Eventos["rodada_concluida"] || cfg.Notificacoes.Eventos["fase_iniciada"] {
		t.Fatalf("defaults de eventos inesperados: %+v", cfg.Notificacoes.Eventos)
	}
	if _, err := os.Stat(caminhoConfigExemplo(raiz)); err != nil {
		t.Fatalf("autopilot.exemplo.json deveria ser gerado: %v", err)
	}
}

func TestCarregarConfigMigraNotificacoesINI(t *testing.T) {
	raiz := t.TempDir()
	if err := os.MkdirAll(filepath.Join(raiz, dirAutomacao), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := salvarConfig(raiz, configPadrao()); err != nil {
		t.Fatal(err)
	}
	cfg := configPadrao()
	cfg.Notificacoes = NotificacoesConfig{}
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(caminhoConfig(raiz), b, 0o600); err != nil {
		t.Fatal(err)
	}
	ini := "[telegram]\nativo=sim\ntoken=123\nchat_id=42\n[painel]\nauth=sim\nbase64=abc\nbind=127.0.0.1\n"
	if err := os.WriteFile(caminhoNotificacoes(raiz), []byte(ini), 0o600); err != nil {
		t.Fatal(err)
	}
	lida, err := carregarConfig(raiz)
	if err != nil {
		t.Fatal(err)
	}
	tg := lida.Notificacoes.Canais["telegram"]
	if !tg.Ativo || tg.BotToken != "123" || tg.ChatID != "42" {
		t.Fatalf("telegram nao migrado: %+v", tg)
	}
	if !lida.Painel.AuthAtivo || lida.Painel.CredencialBase64 != "abc" || lida.Painel.Bind != "127.0.0.1" {
		t.Fatalf("painel nao migrado: %+v", lida.Painel)
	}
	if _, err := os.Stat(caminhoNotificacoes(raiz) + ".bak"); err != nil {
		t.Fatalf("ini legado deveria virar .bak: %v", err)
	}
}

func TestCarregarConfigReleituraFresca(t *testing.T) {
	raiz := t.TempDir()
	cfg := configPadrao()
	if err := salvarConfig(raiz, cfg); err != nil {
		t.Fatal(err)
	}
	primeira, err := carregarConfig(raiz)
	if err != nil {
		t.Fatal(err)
	}
	if primeira.Motores.Operacoes["executar"] != "claude" {
		t.Fatalf("default inesperado: %+v", primeira.Motores.Operacoes)
	}
	cfg.Motores.Operacoes["executar"] = "codex"
	if err := salvarConfig(raiz, cfg); err != nil {
		t.Fatal(err)
	}
	segunda, err := carregarConfig(raiz)
	if err != nil {
		t.Fatal(err)
	}
	if got := segunda.Motores.Operacoes["executar"]; got != "codex" {
		t.Fatalf("releitura nao refletiu mudanca no disco: %q", got)
	}
}

func TestNormalizarMaxFasesNovas(t *testing.T) {
	raiz := t.TempDir()
	if err := os.MkdirAll(filepath.Join(raiz, dirAutomacao), 0o755); err != nil {
		t.Fatal(err)
	}
	// config antiga, sem o campo: normaliza para o default 10
	bruto := `{"plano":"PLANO.md","gates":[]}`
	if err := os.WriteFile(caminhoConfig(raiz), []byte(bruto), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := carregarConfig(raiz)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxFasesNovas != 10 {
		t.Fatalf("max_fases_novas ausente deveria normalizar para 10, veio %d", cfg.MaxFasesNovas)
	}
	if !cfg.Notificacoes.Eventos["fases_novas_inseridas"] || !cfg.Notificacoes.Eventos["fases_novas_descartadas"] {
		t.Fatalf("eventos de fases novas deveriam nascer ativos: %+v", cfg.Notificacoes.Eventos)
	}
	// -1 desativa o recurso e e preservado
	bruto = `{"plano":"PLANO.md","max_fases_novas":-1,"gates":[]}`
	if err := os.WriteFile(caminhoConfig(raiz), []byte(bruto), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = carregarConfig(raiz)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxFasesNovas != -1 {
		t.Fatalf("max_fases_novas=-1 deveria ser preservado, veio %d", cfg.MaxFasesNovas)
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
