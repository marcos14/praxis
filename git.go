package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func gitLimpo(dir string) (bool, error) {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return false, fmt.Errorf("git status em %s: %w", dir, err)
	}
	return len(bytes.TrimSpace(out)) == 0, nil
}

// arvoreLimpaFora reporta se o repositorio esta limpo desconsiderando os
// arquivos de bookkeeping do proprio Praxis (tudo sob raiz/automacao/). Esses
// arquivos (fases.csv, logs, RESUMO...) mudam como efeito colateral da
// execucao e nunca devem bloquear a proxima fase — a pre-checagem so existe
// para proteger o trabalho do usuario de ser varrido para o commit da fase.
func arvoreLimpaFora(repo, raiz string) (bool, error) {
	nomes, err := gitArquivosMudados(repo)
	if err != nil {
		return false, err
	}
	auto, err := filepath.Abs(filepath.Join(raiz, dirAutomacao))
	if err != nil {
		return false, err
	}
	auto = filepath.Clean(auto)
	for _, n := range nomes {
		abs, err := filepath.Abs(filepath.Join(repo, n))
		if err != nil {
			return false, nil
		}
		abs = filepath.Clean(abs)
		if abs == auto || strings.HasPrefix(abs, auto+string(filepath.Separator)) {
			continue // arquivo do proprio Praxis
		}
		return false, nil
	}
	return true, nil
}

// commitAutomacao registra o estado do Praxis (automacao/) num commit proprio
// no repositorio que contem a raiz — assim o progresso do fases.csv fica no
// historico e a arvore volta a ficar limpa para a proxima fase. Best-effort:
// so commita se houver algo em automacao/ para commitar (barra commit vazio).
func commitAutomacao(raiz, msg string) error {
	repo := gitToplevel(raiz)
	if repo == "" {
		return nil // raiz nao e repo git — nada a fazer
	}
	auto, err := filepath.Abs(filepath.Join(raiz, dirAutomacao))
	if err != nil {
		return err
	}
	out, err := exec.Command("git", "-C", repo, "status", "--porcelain", "--", auto).Output()
	if err != nil {
		return fmt.Errorf("git status automacao em %s: %w", repo, err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil // nada a registrar
	}
	if out, err := exec.Command("git", "-C", repo, "add", "--", auto).CombinedOutput(); err != nil {
		return fmt.Errorf("git add automacao em %s: %v — %s", repo, err, out)
	}
	if out, err := exec.Command("git", "-C", repo, "commit", "-m", msg, "--", auto).CombinedOutput(); err != nil {
		return fmt.Errorf("git commit automacao em %s: %v — %s", repo, err, out)
	}
	return nil
}

const (
	marcadorGitignoreIni = "# >>> praxis (orquestrador de fases) — nao edite entre os marcadores >>>"
	marcadorGitignoreFim = "# <<< praxis <<<"
)

// garantirGitignore mantem, na raiz do projeto, um bloco de .gitignore
// gerenciado pelo Praxis. Versionando: ignora so o transitorio (logs, exe,
// backups), deixando fases.csv/autopilot.json/prompts rastreados. Sem
// versionar: ignora a pasta automacao/ inteira e para de rastrear o que ja
// estava versionado nela.
func garantirGitignore(raiz string, versionar bool) error {
	var regras []string
	if versionar {
		regras = []string{"/automacao/logs/", "/automacao/*.exe", "/automacao/fases-*.bak.csv", "/automacao/notificacoes.ini"}
	} else {
		regras = []string{"/automacao/"}
	}
	bloco := marcadorGitignoreIni + "\n" + strings.Join(regras, "\n") + "\n" + marcadorGitignoreFim

	caminho := filepath.Join(raiz, ".gitignore")
	conteudo := ""
	if b, err := os.ReadFile(caminho); err == nil {
		conteudo = string(b)
	}
	if err := os.WriteFile(caminho, []byte(substituirBloco(conteudo, bloco)), 0o644); err != nil {
		return err
	}
	if !versionar {
		// para de rastrear o que agora esta ignorado (best-effort)
		if repo := gitToplevel(raiz); repo != "" {
			if auto, err := filepath.Abs(filepath.Join(raiz, dirAutomacao)); err == nil {
				_ = exec.Command("git", "-C", repo, "rm", "-r", "--cached", "--ignore-unmatch", "-q", "--", auto).Run()
			}
		}
	}
	return nil
}

// substituirBloco insere ou atualiza o bloco gerenciado (entre os marcadores)
// no conteudo de um .gitignore, preservando o resto do arquivo.
func substituirBloco(conteudo, bloco string) string {
	i := strings.Index(conteudo, marcadorGitignoreIni)
	if i < 0 {
		sep := ""
		if conteudo != "" {
			if !strings.HasSuffix(conteudo, "\n") {
				sep = "\n"
			}
			sep += "\n"
		}
		return conteudo + sep + bloco + "\n"
	}
	resto := ""
	if j := strings.Index(conteudo[i:], marcadorGitignoreFim); j >= 0 {
		resto = strings.TrimPrefix(conteudo[i+j+len(marcadorGitignoreFim):], "\n")
	}
	return conteudo[:i] + bloco + "\n" + resto
}

// gitToplevel devolve a raiz do repositorio que contem dir ("" se nao for repo).
func gitToplevel(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitArquivosMudados lista os caminhos com mudancas (staged, unstaged e
// untracked), relativos a raiz do repo, com barras normais.
func gitArquivosMudados(dir string) ([]string, error) {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("git status em %s: %w", dir, err)
	}
	var nomes []string
	for _, l := range strings.Split(string(out), "\n") {
		if len(l) < 4 {
			continue
		}
		nome := strings.TrimSpace(l[3:])
		// renomeios vem como "antigo -> novo"
		if i := strings.Index(nome, " -> "); i >= 0 {
			nome = nome[i+4:]
		}
		nomes = append(nomes, strings.Trim(nome, `"`))
	}
	return nomes, nil
}

func gitCommit(dir, msg string) error {
	if out, err := exec.Command("git", "-C", dir, "add", "-A").CombinedOutput(); err != nil {
		return fmt.Errorf("git add em %s: %v — %s", dir, err, out)
	}
	cmd := exec.Command("git", "-C", dir, "commit", "-F", "-")
	cmd.Stdin = strings.NewReader(msg)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit em %s: %v — %s", dir, err, out)
	}
	return nil
}

// reposEnvolvidos lista as raizes unicas dos repositorios git tocados por uma
// fase: o repo do projeto + os add_dirs que forem repos.
func reposEnvolvidos(raiz string, cfg *Config) []string {
	vistos := map[string]bool{}
	var repos []string
	adicionar := func(dir string) {
		top := gitToplevel(dir)
		if top == "" || vistos[top] {
			return
		}
		vistos[top] = true
		repos = append(repos, top)
	}
	adicionar(raiz)
	for _, d := range cfg.AddDirs {
		adicionar(resolverDir(raiz, d))
	}
	return repos
}
