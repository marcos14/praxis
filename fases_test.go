package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCSVRoundTrip(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "fases.csv")
	original := []*Fase{
		{Fase: "2d", Titulo: "Campos Visiveis + Diretorio", Status: StPendente},
		{
			Fase: "4", Titulo: "Sync delta; incremental (a,b)", Status: StPendente,
			DependeDe: []string{"2f", "3e"}, RequerHumano: true, Notificar: true, GateExtra: "integration",
			Modelo: "opus", Tentativas: 2, CustoUSD: 12.5,
			ConcluidoEm: "2026-07-02 10:00", Observacao: "aprovacao do DBA; obrigatoria",
		},
	}
	if err := salvarFases(caminho, original); err != nil {
		t.Fatalf("salvarFases: %v", err)
	}
	lidas, err := carregarFases(caminho)
	if err != nil {
		t.Fatalf("carregarFases: %v", err)
	}
	if !reflect.DeepEqual(original, lidas) {
		t.Fatalf("round-trip divergente:\noriginal: %+v\nlidas:    %+v", original[1], lidas[1])
	}
}

func TestCarregarFasesStatusVazioViraPendente(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "fases.csv")
	if err := salvarFases(caminho, []*Fase{{Fase: "1", Titulo: "x"}}); err != nil {
		t.Fatal(err)
	}
	lidas, err := carregarFases(caminho)
	if err != nil {
		t.Fatal(err)
	}
	if lidas[0].Status != StPendente {
		t.Fatalf("status vazio deveria virar pendente, veio %q", lidas[0].Status)
	}
}

func TestProximaProntaRespeitaOrdemEDependencias(t *testing.T) {
	fases := []*Fase{
		{Fase: "1", Status: StConcluida},
		{Fase: "3", Status: StPendente, DependeDe: []string{"2"}},
		{Fase: "2", Status: StPendente, DependeDe: []string{"1"}},
	}
	f, motivo := proximaPronta(fases)
	if f == nil {
		t.Fatalf("esperava fase pronta, veio parada: %s", motivo)
	}
	if f.Fase != "2" {
		t.Fatalf("esperava fase 2 (unica com deps satisfeitas), veio %s", f.Fase)
	}
}

func TestProximaProntaRetomaPausadaComPrioridade(t *testing.T) {
	fases := []*Fase{
		{Fase: "1", Status: StConcluida},
		{Fase: "2", Status: StPendente, DependeDe: []string{"1"}},
		{Fase: "3", Status: StPausada, DependeDe: []string{"1"}},
	}
	f, _ := proximaPronta(fases)
	if f == nil || f.Fase != "3" {
		t.Fatalf("esperava retomar a fase pausada 3 antes da pendente 2, veio %v", f)
	}
}

func TestCarregarFasesSemColunaNotificar(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "fases.csv")
	// CSV legado, sem a coluna notificar
	legado := "fase;titulo;status;depende_de;requer_humano;gate_extra;modelo;tentativas;custo_usd;concluido_em;observacao\n" +
		"1;Fase inicial;pendente;;nao;;opus;;;;\n"
	if err := os.WriteFile(caminho, []byte(legado), 0o644); err != nil {
		t.Fatal(err)
	}
	lidas, err := carregarFases(caminho)
	if err != nil {
		t.Fatalf("carregarFases legado: %v", err)
	}
	if len(lidas) != 1 || lidas[0].Notificar {
		t.Fatalf("CSV legado deveria carregar com Notificar=false, veio %+v", lidas[0])
	}
}

func TestProximaProntaMarcaRequerHumanoComoBloqueada(t *testing.T) {
	fases := []*Fase{
		{Fase: "6", Status: StPendente, RequerHumano: true},
		{Fase: "8", Status: StPendente},
	}
	f, _ := proximaPronta(fases)
	if f == nil || f.Fase != "8" {
		t.Fatalf("esperava pular a 6 (requer humano) e devolver a 8, veio %+v", f)
	}
	if fases[0].Status != StBloqueada {
		t.Fatalf("fase 6 deveria ficar bloqueada, ficou %q", fases[0].Status)
	}
}

func TestProximaProntaFilaVazia(t *testing.T) {
	fases := []*Fase{
		{Fase: "1", Status: StConcluida},
		{Fase: "2", Status: StAdiada},
	}
	f, motivo := proximaPronta(fases)
	if f != nil {
		t.Fatalf("nao deveria haver fase pronta, veio %s", f.Fase)
	}
	if !strings.Contains(motivo, "fila vazia") {
		t.Fatalf("motivo inesperado: %q", motivo)
	}
}

func TestProximaProntaTravadaPorDependencia(t *testing.T) {
	fases := []*Fase{
		{Fase: "2", Status: StFalhou},
		{Fase: "3", Status: StPendente, DependeDe: []string{"2"}},
	}
	f, motivo := proximaPronta(fases)
	if f != nil {
		t.Fatalf("nao deveria haver fase pronta, veio %s", f.Fase)
	}
	if strings.Contains(motivo, "fila vazia") {
		t.Fatalf("fila nao esta vazia (ha falhou/pendente): %q", motivo)
	}
}

func TestDepsPendentes(t *testing.T) {
	fases := []*Fase{
		{Fase: "2c", Status: StConcluida},
		{Fase: "3b", Status: StPendente},
		{Fase: "3c", Status: StPendente, DependeDe: []string{"2c", "3b", "9x"}},
	}
	pend := depsPendentes(fases, fases[2])
	if !reflect.DeepEqual(pend, []string{"3b", "9x"}) {
		t.Fatalf("esperava [3b 9x], veio %v", pend)
	}
}
