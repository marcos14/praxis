package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestVereditoCompatSemFasesNovas(t *testing.T) {
	var ver Veredito
	if err := json.Unmarshal([]byte(`{"veredito":"APROVADO","problemas":[]}`), &ver); err != nil {
		t.Fatal(err)
	}
	if ver.FasesNovas != nil {
		t.Fatalf("veredito sem fases_novas deveria decodificar como nil, veio %+v", ver.FasesNovas)
	}
	bruto := `{"veredito":"APROVADO","problemas":[],"fases_novas":[{"titulo":"Paginacao no endpoint X","descricao":"Adicionar paginacao.","checklist":["item 1"],"depende_de":["Fase 2b"],"gate_extra":"integration","observacao":"obs"}]}`
	if err := json.Unmarshal([]byte(bruto), &ver); err != nil {
		t.Fatal(err)
	}
	esperado := FaseNova{
		Titulo: "Paginacao no endpoint X", Descricao: "Adicionar paginacao.",
		Checklist: []string{"item 1"}, DependeDe: []string{"Fase 2b"},
		GateExtra: "integration", Observacao: "obs",
	}
	if len(ver.FasesNovas) != 1 || !reflect.DeepEqual(ver.FasesNovas[0], esperado) {
		t.Fatalf("fases_novas mal decodificado: %+v", ver.FasesNovas)
	}
}

func TestGerarIDFaseNovaNaoColide(t *testing.T) {
	todas := []*Fase{
		{Fase: "Fase 1c"},
		{Fase: "fase 1c.N1"}, // colisao case-insensitive
	}
	if id := gerarIDFaseNova(todas, "Fase 1c"); id != "Fase 1c.n2" {
		t.Fatalf("esperava Fase 1c.n2, veio %q", id)
	}
	if id := gerarIDFaseNova(todas, "Fase 9"); id != "Fase 9.n1" {
		t.Fatalf("esperava Fase 9.n1, veio %q", id)
	}
}

func TestPrepararFasesNovasDedupe(t *testing.T) {
	todas := []*Fase{{Fase: "Fase 1", Titulo: "Autenticacao por token"}}
	atual := &Fase{Fase: "Fase 1", Titulo: "Autenticacao por token"}
	sugeridas := []FaseNova{
		{Titulo: "  autenticacao POR   token ", Descricao: "duplicada de fase existente"},
		{Titulo: "Rate limit no endpoint", Descricao: "nova de verdade", Valor: "alto"},
		{Titulo: "rate LIMIT no endpoint", Descricao: "duplicada dentro do lote"},
		{Titulo: "", Descricao: "sem titulo"},
		{Titulo: "Sem descricao", Descricao: "   "},
	}
	aceitas, specs, descartadas := prepararFasesNovas(todas, atual, sugeridas, 10)
	if len(aceitas) != 1 || aceitas[0].Titulo != "Rate limit no endpoint" {
		t.Fatalf("esperava so 'Rate limit no endpoint', veio %+v", aceitas)
	}
	if len(specs) != 1 || specs[0].Titulo != sugeridas[1].Titulo {
		t.Fatalf("specs dessincronizadas das aceitas: %+v", specs)
	}
	if len(descartadas) != 4 {
		t.Fatalf("esperava 4 descartes, veio %+v", descartadas)
	}
	if aceitas[0].Status != StPendente {
		t.Fatalf("fase nova de alto valor deveria nascer pendente, veio %q", aceitas[0].Status)
	}
	if aceitas[0].RequerHumano {
		t.Fatal("fase nova de alto valor nao deveria exigir humano")
	}
	if aceitas[0].Observacao == "" {
		t.Fatal("fase nova deveria ganhar observacao default")
	}
}

func TestPrepararFasesNovasBaixoValor(t *testing.T) {
	atual := &Fase{Fase: "Fase 1"}
	sugeridas := []FaseNova{
		{Titulo: "Refino opcional", Descricao: "melhoria nice-to-have", Valor: "baixo"},
		{Titulo: "Sem classificacao", Descricao: "valor ausente cai no caminho conservador"},
		{Titulo: "Valor invalido", Descricao: "qualquer coisa fora de alto vira avaliar", Valor: "medio"},
	}
	aceitas, _, descartadas := prepararFasesNovas(nil, atual, sugeridas, 10)
	if len(descartadas) != 0 {
		t.Fatalf("nao esperava descartes, veio %+v", descartadas)
	}
	if len(aceitas) != 3 {
		t.Fatalf("esperava 3 aceitas, veio %d", len(aceitas))
	}
	for _, f := range aceitas {
		if f.Status != StAvaliar {
			t.Fatalf("%q deveria nascer como %q, veio %q", f.Titulo, StAvaliar, f.Status)
		}
		if !f.RequerHumano {
			t.Fatalf("%q de baixo valor deveria exigir humano", f.Titulo)
		}
	}
}

func TestPrepararFasesNovasTeto(t *testing.T) {
	atual := &Fase{Fase: "Fase 2"}
	sugeridas := []FaseNova{
		{Titulo: "A", Descricao: "a"},
		{Titulo: "B", Descricao: "b"},
		{Titulo: "C", Descricao: "c"},
	}
	aceitas, _, descartadas := prepararFasesNovas(nil, atual, sugeridas, 1)
	if len(aceitas) != 1 || len(descartadas) != 2 {
		t.Fatalf("teto 1: esperava 1 aceita e 2 descartadas, veio %d/%d", len(aceitas), len(descartadas))
	}
	for _, d := range descartadas {
		if !strings.Contains(d.Motivo, "teto") {
			t.Fatalf("motivo de descarte inesperado: %+v", d)
		}
	}
	aceitas, _, descartadas = prepararFasesNovas(nil, atual, sugeridas, 0)
	if len(aceitas) != 0 || len(descartadas) != 3 {
		t.Fatalf("teto 0: esperava 0 aceitas e 3 descartadas, veio %d/%d", len(aceitas), len(descartadas))
	}
}

func TestPrepararFasesNovasSaneiaDependeDe(t *testing.T) {
	todas := []*Fase{
		{Fase: "Fase 1", Titulo: "Base"},
		{Fase: "Fase 3a", Titulo: "Outra"},
	}
	atual := buscarFase(todas, "Fase 1")
	sugeridas := []FaseNova{
		{Titulo: "Primeira nova", Descricao: "x", DependeDe: []string{"Fase 3a", "Fase 9z", "fase 1"}},
		{Titulo: "Segunda nova", Descricao: "y", DependeDe: []string{"Primeira nova"}},
	}
	aceitas, _, _ := prepararFasesNovas(todas, atual, sugeridas, 10)
	if len(aceitas) != 2 {
		t.Fatalf("esperava 2 aceitas, veio %+v", aceitas)
	}
	// dep inexistente removida, dep na fase atual nao duplica, origem sempre presente
	if !reflect.DeepEqual(aceitas[0].DependeDe, []string{"Fase 1", "Fase 3a"}) {
		t.Fatalf("deps da primeira: %v", aceitas[0].DependeDe)
	}
	// dep por titulo de outra fase nova do lote resolve para o ID gerado
	if !reflect.DeepEqual(aceitas[1].DependeDe, []string{"Fase 1", aceitas[0].Fase}) {
		t.Fatalf("deps da segunda: %v", aceitas[1].DependeDe)
	}
}

func TestFaseNovaRoundTripCSV(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "fases.csv")
	todas := []*Fase{{Fase: "Fase 1c", Titulo: "Base", Status: StConcluida}}
	atual := todas[0]
	aceitas, _, _ := prepararFasesNovas(todas, atual, []FaseNova{
		{Titulo: "Nova", Descricao: "meta", DependeDe: []string{"Fase 1c"}, GateExtra: "integration"},
	}, 10)
	todas = append(todas, aceitas...)
	if err := salvarFases(caminho, todas); err != nil {
		t.Fatalf("salvarFases: %v", err)
	}
	lidas, err := carregarFases(caminho)
	if err != nil {
		t.Fatalf("carregarFases: %v", err)
	}
	if !reflect.DeepEqual(todas, lidas) {
		t.Fatalf("round-trip divergente:\noriginal: %+v\nlidas:    %+v", todas[1], lidas[1])
	}
}

func TestAnexarSpecFaseNova(t *testing.T) {
	plano := filepath.Join(t.TempDir(), "PLANO.md")
	if err := os.WriteFile(plano, []byte("# Plano\n\nconteudo original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f1 := &Fase{Fase: "Fase 2.n1", Titulo: "Nova A", DependeDe: []string{"Fase 2"}, GateExtra: "integration"}
	if err := anexarSpecFaseNova(plano, f1, FaseNova{Descricao: "Meta A.", Checklist: []string{"item 1", "item 2"}}); err != nil {
		t.Fatal(err)
	}
	f2 := &Fase{Fase: "Fase 2.n2", Titulo: "Nova B", DependeDe: []string{"Fase 2"}}
	if err := anexarSpecFaseNova(plano, f2, FaseNova{Descricao: "Meta B."}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(plano)
	if err != nil {
		t.Fatal(err)
	}
	txt := string(b)
	if !strings.HasPrefix(txt, "# Plano") {
		t.Fatal("conteudo original deveria ser preservado")
	}
	if n := strings.Count(txt, marcadorFasesDescobertas); n != 1 {
		t.Fatalf("marcador deveria aparecer 1 vez, apareceu %d", n)
	}
	for _, quer := range []string{"### Fase 2.n1 — Nova A", "### Fase 2.n2 — Nova B",
		"Gate extra: `integration`", "- [ ] item 2", "- [ ] Implementar a meta acima com testes."} {
		if !strings.Contains(txt, quer) {
			t.Fatalf("plano sem %q:\n%s", quer, txt)
		}
	}
}

func TestProximaProntaPegaFaseNovaAposOrigem(t *testing.T) {
	fases := []*Fase{
		{Fase: "Fase 1", Status: StConcluida},
		{Fase: "Fase 1.n1", Status: StPendente, DependeDe: []string{"Fase 1"}},
	}
	f, motivo := proximaPronta(fases)
	if f == nil || f.Fase != "Fase 1.n1" {
		t.Fatalf("fase nova deveria estar pronta apos a origem concluir, veio %v (%s)", f, motivo)
	}
}
