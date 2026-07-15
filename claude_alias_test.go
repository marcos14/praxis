package main

import (
	"path/filepath"
	"testing"
)

func TestParseClaudeAliasCreateArg(t *testing.T) {
	alias, rest, ok, err := parseClaudeAliasCreateArg([]string{"--claude-alias-create", "claude_alt", "--raiz", "x"})
	if err != nil || !ok {
		t.Fatalf("esperava parse valido, ok=%v err=%v", ok, err)
	}
	if alias != "claude_alt" {
		t.Fatalf("alias inesperado: %q", alias)
	}
	if len(rest) != 2 || rest[0] != "--raiz" || rest[1] != "x" {
		t.Fatalf("resto inesperado: %+v", rest)
	}

	alias, rest, ok, err = parseClaudeAliasCreateArg([]string{"--claude-alias-create=claude_alt", "--sem-fallback"})
	if err != nil || !ok || alias != "claude_alt" || len(rest) != 1 {
		t.Fatalf("parse com '=' falhou: alias=%q rest=%+v ok=%v err=%v", alias, rest, ok, err)
	}

	_, _, ok, err = parseClaudeAliasCreateArg([]string{"status"})
	if err != nil || ok {
		t.Fatalf("nao deveria tratar comando normal como flag global: ok=%v err=%v", ok, err)
	}
}

func TestCmdClaudeAliasCreateAtualizaConfigEFallback(t *testing.T) {
	raiz := t.TempDir()
	cfg := configPadrao()
	cfg.Motores.Fallback.Ativo = true
	cfg.Motores.Fallback.Ordem = []string{"claude", "codex"}
	if err := salvarConfig(raiz, cfg); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(raiz, "conta-secundaria")
	if err := cmdClaudeAliasCreate("claude_alt", []string{"--raiz", raiz, "--dir", dir, "--usar-em", "executar,corrigir", "--sem-shell-alias"}); err != nil {
		t.Fatalf("cmdClaudeAliasCreate: %v", err)
	}

	lido, err := carregarConfig(raiz)
	if err != nil {
		t.Fatal(err)
	}
	if got := lido.Motores.ClaudeConfigDirs["claude_alt"]; got != dir {
		t.Fatalf("claude_config_dirs nao atualizado: %q", got)
	}
	if got := lido.Motores.Operacoes["executar"]; got != "claude_alt" {
		t.Fatalf("operacao executar nao atualizada: %q", got)
	}
	if got := lido.Motores.Operacoes["corrigir"]; got != "claude_alt" {
		t.Fatalf("operacao corrigir nao atualizada: %q", got)
	}
	if got := lido.Motores.Fallback.Ordem; len(got) < 3 || got[1] != "claude_alt" {
		t.Fatalf("fallback.ordem deveria inserir alias apos claude: %+v", got)
	}
}

func TestCmdClaudeAliasCreateAliasInvalido(t *testing.T) {
	raiz := t.TempDir()
	if err := salvarConfig(raiz, configPadrao()); err != nil {
		t.Fatal(err)
	}
	if err := cmdClaudeAliasCreate("123errado", []string{"--raiz", raiz, "--sem-shell-alias"}); err == nil {
		t.Fatal("esperava erro para alias invalido")
	}
	if err := cmdClaudeAliasCreate("codex", []string{"--raiz", raiz, "--sem-shell-alias"}); err == nil {
		t.Fatal("esperava erro para alias conflitando com motor")
	}
}

func TestInserirAliasNoFallbackSemDuplicar(t *testing.T) {
	ordem := inserirAliasNoFallback([]string{"claude", "claude_alt", "codex"}, "claude_alt")
	if len(ordem) != 3 {
		t.Fatalf("nao deveria duplicar alias: %+v", ordem)
	}
}

func TestNomesComandoShell(t *testing.T) {
	got := nomesComandoShell("claude_alt")
	if len(got) != 2 || got[0] != "claude-alt" || got[1] != "claude_alt" {
		t.Fatalf("nomes de comando inesperados: %+v", got)
	}
}

func TestUpsertBlocoMarcadoIdempotente(t *testing.T) {
	start := "# >>> teste >>>"
	end := "# <<< teste <<<"
	bloco := start + "\nlinha\n" + end
	primeiro := upsertBlocoMarcado("", start, end, bloco)
	segundo := upsertBlocoMarcado(primeiro, start, end, bloco)
	if primeiro != segundo {
		t.Fatalf("upsert deveria ser idempotente\nprimeiro:\n%s\nsegundo:\n%s", primeiro, segundo)
	}
}

func TestCaminhosPerfilPowerShellIncluiOneDriveDocumentos(t *testing.T) {
	home := `C:\Users\marco`
	got := caminhosPerfilPowerShell(home)
	if len(got) < 4 {
		t.Fatalf("lista de perfis deveria ter varios candidatos, veio: %+v", got)
	}
	temDocPt := false
	temOneDrivePt := false
	for _, p := range got {
		if p == `C:\Users\marco\Documentos\WindowsPowerShell\Microsoft.PowerShell_profile.ps1` {
			temDocPt = true
		}
		if p == `C:\Users\marco\OneDrive\Documentos\WindowsPowerShell\Microsoft.PowerShell_profile.ps1` {
			temOneDrivePt = true
		}
	}
	if !temDocPt || !temOneDrivePt {
		t.Fatalf("faltou caminho em portugues para perfil do PowerShell: %+v", got)
	}
}
