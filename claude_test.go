package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDecodificarEstruturadoPreferido(t *testing.T) {
	res := &ResultadoClaude{
		Estruturado: json.RawMessage(`{"veredito":"APROVADO","problemas":[]}`),
		Resultado:   "texto qualquer",
	}
	var v Veredito
	if err := decodificarEstruturado(res, &v); err != nil {
		t.Fatal(err)
	}
	if v.Veredito != "APROVADO" || len(v.Problemas) != 0 {
		t.Fatalf("veredito inesperado: %+v", v)
	}
}

func TestDecodificarEstruturadoDoTextoComCercas(t *testing.T) {
	res := &ResultadoClaude{
		Resultado: "Segue o veredito:\n```json\n{\"veredito\":\"REPROVADO\",\"problemas\":[\"faltou teste de migracao\"]}\n```\n",
	}
	var v Veredito
	if err := decodificarEstruturado(res, &v); err != nil {
		t.Fatal(err)
	}
	if v.Veredito != "REPROVADO" || len(v.Problemas) != 1 {
		t.Fatalf("veredito inesperado: %+v", v)
	}
}

func TestDecodificarEstruturadoSemJSON(t *testing.T) {
	res := &ResultadoClaude{Resultado: "sem json nenhum"}
	var v Veredito
	if err := decodificarEstruturado(res, &v); err == nil {
		t.Fatal("esperava erro para resposta sem JSON")
	}
}

func TestParseEventoResult(t *testing.T) {
	linha := `{"type":"result","subtype":"success","is_error":false,"result":"tudo certo","total_cost_usd":3.21,"num_turns":17,"structured_output":{"veredito":"APROVADO","problemas":[]}}`
	var ev eventoStreamClaude
	if err := json.Unmarshal([]byte(linha), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Type != "result" || ev.IsError || ev.Result != "tudo certo" ||
		ev.TotalCostUSD != 3.21 || ev.NumTurns != 17 || len(ev.StructuredOutput) == 0 {
		t.Fatalf("evento result mal parseado: %+v", ev)
	}
}

func TestLimiteSessaoClaude(t *testing.T) {
	texto := "You've hit your session limit · resets 11:40pm"
	if !limiteSessaoAtingido(texto) {
		t.Fatalf("esperava detectar limite em %q", texto)
	}
	if got := linhaLimite("prefixo\n" + texto + "\nrodape"); got != texto {
		t.Fatalf("linha limite: %q", got)
	}
	if limiteSessaoAtingido("rate limit sem informacao de reset") {
		t.Fatal("nao deveria detectar limite sem reset")
	}
}

func TestMotorClaudeDetectaLimiteNoStderrSemResultado(t *testing.T) {
	dir := t.TempDir()
	nome := "claude"
	conteudo := "#!/bin/sh\necho \"You've hit your session limit - resets 11:40pm\" >&2\nexit 1\n"
	if runtime.GOOS == "windows" {
		nome = "claude.bat"
		conteudo = "@echo off\r\necho You've hit your session limit - resets 11:40pm 1>&2\r\nexit /b 1\r\n"
	}
	caminho := filepath.Join(dir, nome)
	if err := os.WriteFile(caminho, []byte(conteudo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	raiz := t.TempDir()
	res, err := motorClaude{}.Rodar(OpcoesRun{Raiz: raiz, Prompt: "teste", RotuloLog: "limite", TimeoutMin: 1})
	if err != nil {
		t.Fatalf("nao esperava erro, esperava ResultadoRun com limite: %v", err)
	}
	if res == nil || !res.LimiteSessao {
		t.Fatalf("limite nao detectado: %+v", res)
	}
	if res.DetalheLimite == "" {
		t.Fatalf("detalhe do limite deveria ser preenchido: %+v", res)
	}
}

func TestRenderPrompt(t *testing.T) {
	tpl := "Fase {FASE} — {TITULO} em {PLANO}. {FASE} de novo."
	saida := renderPrompt(tpl, map[string]string{"FASE": "2d", "TITULO": "Campos", "PLANO": "PLANO.md"})
	esperado := "Fase 2d — Campos em PLANO.md. 2d de novo."
	if saida != esperado {
		t.Fatalf("esperava %q, veio %q", esperado, saida)
	}
}

func TestPrimeirasEUltimasLinhas(t *testing.T) {
	texto := "a\nb\nc\nd"
	if got := primeirasLinhas(texto, 2); got != "a\nb\n(...)" {
		t.Fatalf("primeirasLinhas: %q", got)
	}
	if got := ultimasLinhas(texto, 2); got != "c\nd" {
		t.Fatalf("ultimasLinhas: %q", got)
	}
	if got := primeirasLinhas("a\nb", 5); got != "a\nb" {
		t.Fatalf("primeirasLinhas sem corte: %q", got)
	}
}

func TestMotorClaudeUsaClaudeConfigDirNoAmbiente(t *testing.T) {
	dirBin := t.TempDir()
	registro := filepath.Join(t.TempDir(), "claude_env.txt")
	nome := "claude"
	conteudo := "#!/bin/sh\nprintf '%s' \"$CLAUDE_CONFIG_DIR\" > \"" + registro + "\"\necho '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"ok\",\"total_cost_usd\":0,\"num_turns\":1}'\nexit 0\n"
	if runtime.GOOS == "windows" {
		nome = "claude.bat"
		conteudo = "@echo off\r\n>\"" + registro + "\" echo %CLAUDE_CONFIG_DIR%\r\necho {\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"ok\",\"total_cost_usd\":0,\"num_turns\":1}\r\nexit /b 0\r\n"
	}
	caminho := filepath.Join(dirBin, nome)
	if err := os.WriteFile(caminho, []byte(conteudo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dirBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	raiz := t.TempDir()
	const cfgDir = `C:\Users\dev\.claude-alt`
	res, err := motorClaude{}.Rodar(OpcoesRun{Raiz: raiz, Prompt: "teste", RotuloLog: "env", TimeoutMin: 1, ClaudeConfigDir: cfgDir})
	if err != nil {
		t.Fatalf("erro inesperado ao rodar claude fake: %v", err)
	}
	if res == nil || res.Subtipo != "success" {
		t.Fatalf("resultado inesperado: %+v", res)
	}
	b, err := os.ReadFile(registro)
	if err != nil {
		t.Fatalf("nao consegui ler registro de ambiente: %v", err)
	}
	if got := strings.TrimSpace(string(b)); got != cfgDir {
		t.Fatalf("CLAUDE_CONFIG_DIR inesperado: %q", got)
	}
}
