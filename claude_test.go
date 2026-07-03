package main

import (
	"encoding/json"
	"testing"
)

func TestDecodificarEstruturadoPreferido(t *testing.T) {
	res := &ResultadoRun{
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
	res := &ResultadoRun{
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
	res := &ResultadoRun{Resultado: "sem json nenhum"}
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
