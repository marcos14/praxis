package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoTemp cria um repo git isolado com um commit inicial contendo um arquivo
// de codigo e o automacao/fases.csv (como num projeto real do Praxis).
func repoTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v — %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@t.dev")
	git("config", "user.name", "t")
	git("commit", "--allow-empty", "-q", "-m", "raiz")

	if err := os.MkdirAll(filepath.Join(dir, dirAutomacao), 0o755); err != nil {
		t.Fatal(err)
	}
	escrever(t, dir, "codigo.txt", "v1")
	escrever(t, dir, filepath.Join(dirAutomacao, nomeCSV), "fase;status\n2e;executando\n")
	git("add", "-A")
	git("commit", "-q", "-m", "inicial")
	return dir
}

func escrever(t *testing.T, dir, rel, conteudo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(conteudo), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestArvoreLimpaFora_IgnoraBookkeeping reproduz o bug que travava a fase
// seguinte: com so o automacao/fases.csv sujo, gitLimpo acusa sujeira, mas a
// pre-checagem tolerante (arvoreLimpaFora) deve considerar a arvore limpa.
func TestArvoreLimpaFora_IgnoraBookkeeping(t *testing.T) {
	dir := repoTemp(t)

	// so o fases.csv mudou (como o Praxis faz ao marcar a fase concluida)
	escrever(t, dir, filepath.Join(dirAutomacao, nomeCSV), "fase;status\n2e;concluida\n")

	if limpo, err := gitLimpo(dir); err != nil || limpo {
		t.Fatalf("gitLimpo deveria acusar sujeira (fases.csv), got limpo=%v err=%v", limpo, err)
	}
	limpo, err := arvoreLimpaFora(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !limpo {
		t.Fatal("arvoreLimpaFora deveria ignorar automacao/ e reportar limpo")
	}
}

// TestArvoreLimpaFora_TrabalhoDoUsuarioBloqueia garante que mudancas de codigo
// (fora de automacao/) continuam bloqueando, como antes.
func TestArvoreLimpaFora_TrabalhoDoUsuarioBloqueia(t *testing.T) {
	dir := repoTemp(t)
	escrever(t, dir, "codigo.txt", "v2") // trabalho do usuario, nao commitado

	limpo, err := arvoreLimpaFora(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	if limpo {
		t.Fatal("arvoreLimpaFora deveria bloquear com codigo.txt sujo")
	}
}

// TestCommitAutomacao registra o estado do Praxis e deixa a arvore limpa.
func TestCommitAutomacao(t *testing.T) {
	dir := repoTemp(t)
	escrever(t, dir, filepath.Join(dirAutomacao, nomeCSV), "fase;status\n2e;concluida\n")

	if err := commitAutomacao(dir, "chore(praxis): teste"); err != nil {
		t.Fatal(err)
	}
	limpo, err := gitLimpo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !limpo {
		t.Fatal("apos commitAutomacao a arvore deveria estar limpa")
	}
	// idempotente: sem mudancas, nao tenta commit vazio (nao deve dar erro)
	if err := commitAutomacao(dir, "chore(praxis): vazio"); err != nil {
		t.Fatalf("commitAutomacao com arvore limpa deveria ser no-op, got %v", err)
	}
}

// TestGarantirGitignore_NaoVersionar ignora a pasta inteira e desrastreia.
func TestGarantirGitignore_NaoVersionar(t *testing.T) {
	dir := repoTemp(t)
	if err := garantirGitignore(dir, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); !contemLinha(got, "/automacao/") {
		t.Fatalf(".gitignore deveria conter /automacao/, got:\n%s", got)
	}
	// fases.csv (antes rastreado) deve ter saido do indice
	out, _ := exec.Command("git", "-C", dir, "ls-files", filepath.Join(dirAutomacao, nomeCSV)).Output()
	if len(out) != 0 {
		t.Fatalf("fases.csv deveria ter sido desrastreado, ls-files: %q", out)
	}
}

// TestGarantirGitignore_Idempotente: rodar duas vezes nao duplica o bloco.
func TestGarantirGitignore_Idempotente(t *testing.T) {
	dir := repoTemp(t)
	if err := garantirGitignore(dir, true); err != nil {
		t.Fatal(err)
	}
	if err := garantirGitignore(dir, true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if n := strings.Count(string(b), marcadorGitignoreIni); n != 1 {
		t.Fatalf("o bloco gerenciado deveria aparecer 1x, apareceu %d", n)
	}
}

func contemLinha(s, alvo string) bool {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimRight(l, "\r") == alvo {
			return true
		}
	}
	return false
}
