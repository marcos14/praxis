package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSelecionarMotorRegistro(t *testing.T) {
	m, err := selecionarMotor("")
	if err != nil {
		t.Fatal(err)
	}
	if m.Nome() != "claude" {
		t.Fatalf("motor default inesperado: %s", m.Nome())
	}
	m, err = selecionarMotor("codex")
	if err != nil {
		t.Fatal(err)
	}
	if m.Nome() != "codex" {
		t.Fatalf("motor codex inesperado: %s", m.Nome())
	}
	if _, err := selecionarMotor("desconhecido"); err == nil {
		t.Fatal("esperava erro para motor desconhecido")
	}
}

func TestCustoEstimado(t *testing.T) {
	got := custoEstimado("gpt-5.5", 1_000_000, 1_000_000)
	if got != 35 {
		t.Fatalf("custo gpt-5.5 inesperado: %.2f", got)
	}
	got = custoEstimado("gpt-5-codex", 1_000_000, 1_000_000)
	if got != 11.25 {
		t.Fatalf("custo inesperado: %.2f", got)
	}
	if got := custoEstimado("modelo-inexistente", 1_000_000, 1_000_000); got != 0 {
		t.Fatalf("modelo desconhecido deveria custar 0, veio %.2f", got)
	}
}

func TestCoAuthorTrailer(t *testing.T) {
	if got := coAuthorTrailer("claude"); got == "" || got == coAuthorTrailer("codex") {
		t.Fatalf("trailers deveriam existir e ser diferentes, claude=%q codex=%q", got, coAuthorTrailer("codex"))
	}
	if got := coAuthorTrailer("desconhecido"); got != "" {
		t.Fatalf("motor desconhecido nao deveria ter trailer: %q", got)
	}
}

func TestSchemaStrictOpenAI(t *testing.T) {
	schema := `{"type":"object","properties":{"b":{"type":"string"},"a":{"type":"object","properties":{"x":{"type":"string"}}}}}`
	strict, err := schemaStrictOpenAI(schema)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strict), &m); err != nil {
		t.Fatal(err)
	}
	if m["additionalProperties"] != false {
		t.Fatalf("additionalProperties raiz nao foi desativado: %v", m["additionalProperties"])
	}
	req, _ := m["required"].([]any)
	if strings.Join([]string{req[0].(string), req[1].(string)}, ",") != "a,b" {
		t.Fatalf("required raiz inesperado: %#v", req)
	}
	props := m["properties"].(map[string]any)
	a := props["a"].(map[string]any)
	if a["additionalProperties"] != false {
		t.Fatalf("additionalProperties aninhado nao foi desativado: %v", a["additionalProperties"])
	}
}

func TestLimiteCodexAtingido(t *testing.T) {
	for _, texto := range []string{
		"429 too many requests",
		"rate limit exceeded",
		"usage limit reached",
		"insufficient_quota",
	} {
		if !limiteCodexAtingido(texto) {
			t.Fatalf("esperava limite para %q", texto)
		}
	}
	if limiteCodexAtingido("erro de sintaxe no comando") {
		t.Fatal("nao esperava limite")
	}
}
