package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLimiteOpencode(t *testing.T) {
	for _, texto := range []string{
		"429 Too Many Requests",
		"You have hit your usage limit",
		"rate limit exceeded",
		"provider overloaded",
	} {
		if !limiteOpencodeAtingido(texto) {
			t.Fatalf("esperava detectar limite em %q", texto)
		}
	}
	if limiteOpencodeAtingido("tudo certo, sem limite") {
		t.Fatal("nao deveria detectar limite em texto normal")
	}
	if got := linhaLimiteOpencode("prefixo\nrate limit exceeded\nrodape"); got != "rate limit exceeded" {
		t.Fatalf("linha limite: %q", got)
	}
}

func TestPermissoesOpencode(t *testing.T) {
	// Revisor: sem edicao e sem commit.
	somenteLeitura := permissoesOpencode(OpcoesRun{SomenteLeitura: true})
	var pl map[string]any
	if err := json.Unmarshal([]byte(somenteLeitura), &pl); err != nil {
		t.Fatalf("json invalido: %v (%s)", err, somenteLeitura)
	}
	if pl["edit"] != "deny" {
		t.Fatalf("revisor deveria negar edit: %s", somenteLeitura)
	}
	if _, ok := pl["bash"]; !ok {
		t.Fatalf("revisor deveria negar git commit/push: %s", somenteLeitura)
	}

	// Executor: pode editar, mas nao commita.
	proibirCommit := permissoesOpencode(OpcoesRun{ProibirCommit: true})
	var pc map[string]any
	if err := json.Unmarshal([]byte(proibirCommit), &pc); err != nil {
		t.Fatalf("json invalido: %v (%s)", err, proibirCommit)
	}
	if _, ok := pc["edit"]; ok {
		t.Fatalf("executor nao deveria negar edit: %s", proibirCommit)
	}
	if _, ok := pc["bash"]; !ok {
		t.Fatalf("executor deveria negar git commit/push: %s", proibirCommit)
	}

	// Livre: nenhuma restricao.
	if got := permissoesOpencode(OpcoesRun{}); got != "{}" {
		t.Fatalf("esperava {} sem restricoes, veio %q", got)
	}
}

func TestParseEventoOpencodeTexto(t *testing.T) {
	linha := `{"type":"text","timestamp":1,"sessionID":"s","part":{"type":"text","text":"resposta final","time":{"end":2}}}`
	var ev eventoOpencode
	if err := json.Unmarshal([]byte(linha), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Type != "text" || ev.Part.Text != "resposta final" {
		t.Fatalf("evento text mal parseado: %+v", ev)
	}
}

func TestParseEventoOpencodeStepFinish(t *testing.T) {
	linha := `{"type":"step_finish","part":{"tokens":{"input":120,"output":45}}}`
	var ev eventoOpencode
	if err := json.Unmarshal([]byte(linha), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Part.Tokens.Input != 120 || ev.Part.Tokens.Output != 45 {
		t.Fatalf("tokens mal parseados: %+v", ev.Part.Tokens)
	}
}

func TestMotorOpencodeCapacidades(t *testing.T) {
	c := motorOpencode{}.Capacidades()
	if c.SchemaNativo || c.BudgetNativo || c.CustoUSDNativo {
		t.Fatalf("opencode nao tem capacidades nativas: %+v", c)
	}
	if (motorOpencode{}).Nome() != "opencode" {
		t.Fatalf("nome inesperado: %q", (motorOpencode{}).Nome())
	}
}

func TestMotorOpencodeDetectaLimiteNoStderr(t *testing.T) {
	dir := t.TempDir()
	nome := "opencode"
	conteudo := "#!/bin/sh\necho \"429 Too Many Requests: rate limit exceeded\" >&2\nexit 1\n"
	if runtime.GOOS == "windows" {
		nome = "opencode.bat"
		conteudo = "@echo off\r\necho 429 Too Many Requests: rate limit exceeded 1>&2\r\nexit /b 1\r\n"
	}
	caminho := filepath.Join(dir, nome)
	if err := os.WriteFile(caminho, []byte(conteudo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	raiz := t.TempDir()
	res, err := motorOpencode{}.Rodar(OpcoesRun{Raiz: raiz, Prompt: "teste", RotuloLog: "limite", TimeoutMin: 1})
	if err != nil {
		t.Fatalf("nao esperava erro, esperava ResultadoRun com limite: %v", err)
	}
	if res == nil || !res.LimiteSessao {
		t.Fatalf("limite nao detectado: %+v", res)
	}
	if res.DetalheLimite == "" || !strings.Contains(strings.ToLower(res.DetalheLimite), "limit") {
		t.Fatalf("detalhe do limite deveria mencionar o limite: %+v", res)
	}
}
