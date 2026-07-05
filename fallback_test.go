package main

import "testing"

func TestProximoMotorFallback(t *testing.T) {
	estado := novoEstadoFallback()
	estado.marcarEsgotado("claude")
	got := proximoMotorFallback([]string{"claude", "codex"}, "claude", estado)
	if got != "codex" {
		t.Fatalf("proximo motor: %q", got)
	}
	estado.marcarEsgotado("codex")
	if got := proximoMotorFallback([]string{"claude", "codex"}, "claude", estado); got != "" {
		t.Fatalf("nao deveria haver fallback disponivel: %q", got)
	}
}
