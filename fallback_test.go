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

func TestProximoMotorFallbackComAliasClaude(t *testing.T) {
	estado := novoEstadoFallback()
	estado.marcarEsgotado("claude")
	ordem := []string{"claude", "claude_alt", "codex"}
	if got := proximoMotorFallback(ordem, "claude", estado); got != "claude_alt" {
		t.Fatalf("fallback deveria ir para alias claude_alt, veio %q", got)
	}
	estado.marcarEsgotado("claude_alt")
	if got := proximoMotorFallback(ordem, "claude", estado); got != "codex" {
		t.Fatalf("fallback deveria ir para codex apos esgotar alias, veio %q", got)
	}
}
