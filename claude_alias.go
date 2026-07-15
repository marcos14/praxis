package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var reAliasClaude = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func cmdClaudeAliasCreate(alias string, argv []string) error {
	fs := flag.NewFlagSet("claude-alias-create", flag.ExitOnError)
	raizFlag := fs.String("raiz", "", "raiz do projeto (padrao: deteccao automatica)")
	dirFlag := fs.String("dir", "", "diretorio de configuracao da conta Claude para este alias")
	usarEm := fs.String("usar-em", "", "operacoes para apontar para o alias (ex.: executar,corrigir)")
	semFallback := fs.Bool("sem-fallback", false, "nao mexe em motores.fallback.ordem")
	semShellAlias := fs.Bool("sem-shell-alias", false, "nao cria atalho de shell (claude-alt/claude_alt)")
	if err := fs.Parse(argv); err != nil {
		return err
	}

	alias = normalizarNomeMotor(alias)
	if !reAliasClaude.MatchString(alias) {
		return fmt.Errorf("alias invalido %q: use letras, numeros, _ ou -, comecando por letra", alias)
	}
	if alias == "claude" {
		return fmt.Errorf("alias %q e reservado para a conta Claude padrao", alias)
	}
	if _, ok := motoresRegistrados[alias]; ok {
		return fmt.Errorf("alias %q conflita com um motor existente", alias)
	}

	raiz := resolverRaiz(*raizFlag)
	cfg, err := carregarConfig(raiz)
	if err != nil {
		return err
	}

	dir := strings.TrimSpace(*dirFlag)
	if dir == "" && cfg.Motores.ClaudeConfigDirs != nil {
		dir = strings.TrimSpace(cfg.Motores.ClaudeConfigDirs[alias])
	}
	if dir == "" {
		dir = sugerirDirClaudeAlias(alias)
	}
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("nao consegui sugerir diretorio para o alias %q; informe --dir", alias)
	}

	if cfg.Motores.ClaudeConfigDirs == nil {
		cfg.Motores.ClaudeConfigDirs = map[string]string{}
	}
	cfg.Motores.ClaudeConfigDirs[alias] = dir

	opsAtualizadas, err := aplicarAliasNasOperacoes(cfg, alias, *usarEm)
	if err != nil {
		return err
	}

	if !*semFallback {
		cfg.Motores.Fallback.Ordem = inserirAliasNoFallback(cfg.Motores.Fallback.Ordem, alias)
	}

	if err := salvarConfig(raiz, cfg); err != nil {
		return err
	}

	cmdsShell := nomesComandoShell(alias)
	shellInfo := "(nao configurado)"
	if !*semShellAlias {
		if runtime.GOOS == "windows" {
			res, err := configurarAliasPowerShell(cmdsShell, dir)
			if err != nil {
				return err
			}
			shellInfo = res.Resumo
		} else {
			shellInfo = "(auto-config disponivel apenas no Windows PowerShell)"
		}
	}

	fmt.Printf(`
Alias Claude criado/atualizado com sucesso.

  Alias           : %s
  Config dir      : %s
  Projeto         : %s
  Fallback ordem  : %s
  Shell command   : %s
`, alias, dir, raiz, strings.Join(cfg.Motores.Fallback.Ordem, ", "), shellInfo)

	if len(opsAtualizadas) > 0 {
		fmt.Printf("  Operacoes alteradas: %s\n", strings.Join(opsAtualizadas, ", "))
	}

	fmt.Printf(`
Proximos passos:
	1. Recarregar perfil (use exatamente este, que funciona mesmo quando $PROFILE nao existe):
		 if (Test-Path $PROFILE) { . $PROFILE } else { . "$HOME\OneDrive\Documentos\WindowsPowerShell\Microsoft.PowerShell_profile.ps1" }

	2. Testar:
		 %s

Opcional:
	- Se ainda nao estiver em uso por nenhuma operacao, ajuste no painel
		(Configuracao -> Motores) ou rode novamente com:
			--usar-em executar,corrigir

	- Para validar fallback rapido, execute uma fase e observe no log do Praxis
		quando o alias entrar na ordem configurada.
`, primeiroComandoShell(cmdsShell))

	return nil
}

type resultadoShellAlias struct {
	Resumo     string
	ReloadHint string
}

func nomesComandoShell(alias string) []string {
	alias = normalizarNomeMotor(alias)
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || contemString(out, v) {
			return
		}
		out = append(out, v)
	}
	add(strings.ReplaceAll(alias, "_", "-"))
	add(alias)
	return out
}

func comandoLoginShell(cmds []string, dir string) string {
	if len(cmds) == 0 {
		return fmt.Sprintf("$env:CLAUDE_CONFIG_DIR = \"%s\"\n       claude", dir)
	}
	return fmt.Sprintf("%s", cmds[0])
}

func primeiroComandoShell(cmds []string) string {
	if len(cmds) == 0 {
		return "claude"
	}
	return cmds[0]
}

func configurarAliasPowerShell(cmds []string, configDir string) (resultadoShellAlias, error) {
	if len(cmds) == 0 {
		return resultadoShellAlias{}, fmt.Errorf("nenhum nome de comando para alias de shell")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return resultadoShellAlias{}, fmt.Errorf("nao consegui descobrir o HOME para configurar perfil do PowerShell: %w", err)
	}
	perfis := caminhosPerfilPowerShell(home)
	if len(perfis) == 0 {
		return resultadoShellAlias{}, fmt.Errorf("nao consegui montar lista de perfis do PowerShell")
	}
	for _, p := range perfis {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return resultadoShellAlias{}, fmt.Errorf("nao consegui preparar a pasta do perfil do PowerShell (%s): %w", p, err)
		}
		if _, err := os.Stat(p); err != nil {
			if os.IsNotExist(err) {
				if err := os.WriteFile(p, []byte(""), 0o600); err != nil {
					return resultadoShellAlias{}, fmt.Errorf("nao consegui criar perfil do PowerShell (%s): %w", p, err)
				}
			} else {
				return resultadoShellAlias{}, fmt.Errorf("falha ao acessar perfil do PowerShell (%s): %w", p, err)
			}
		}
		for _, nome := range cmds {
			if err := upsertBlocoAliasPowerShell(p, nome, configDir); err != nil {
				return resultadoShellAlias{}, err
			}
		}
	}
	reload := fmt.Sprintf("if (Test-Path $PROFILE) { . $PROFILE } else { . '%s' }", perfis[0])
	return resultadoShellAlias{
		Resumo:     strings.Join(cmds, ", "),
		ReloadHint: reload,
	}, nil
}

func caminhosPerfilPowerShell(home string) []string {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil
	}
	candidatos := []string{
		filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "OneDrive", "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "OneDrive", "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "OneDrive", "Documentos", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "OneDrive", "Documentos", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "Documentos", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "Documentos", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
	}
	vistos := map[string]bool{}
	var out []string
	for _, c := range candidatos {
		if c == "" || vistos[c] {
			continue
		}
		vistos[c] = true
		out = append(out, c)
	}
	return out
}

func upsertBlocoAliasPowerShell(profilePath, nomeCmd, configDir string) error {
	start := "# >>> praxis-claude-alias:" + nomeCmd + " >>>"
	end := "# <<< praxis-claude-alias:" + nomeCmd + " <<<"
	qDir := strings.ReplaceAll(configDir, "'", "''")
	bloco := strings.TrimSpace(fmt.Sprintf(`%s
function %s {
    $env:CLAUDE_CONFIG_DIR = '%s'
    claude @args
}
%s`, start, nomeCmd, qDir, end))
	conteudoBytes, err := os.ReadFile(profilePath)
	if err != nil {
		return fmt.Errorf("nao consegui ler o perfil do PowerShell (%s): %w", profilePath, err)
	}
	conteudoNovo := upsertBlocoMarcado(string(conteudoBytes), start, end, bloco)
	if err := os.WriteFile(profilePath, []byte(conteudoNovo), 0o600); err != nil {
		return fmt.Errorf("nao consegui escrever o perfil do PowerShell (%s): %w", profilePath, err)
	}
	return nil
}

func upsertBlocoMarcado(conteudo, start, end, bloco string) string {
	if strings.TrimSpace(conteudo) == "" {
		return bloco + "\n"
	}
	ini := strings.Index(conteudo, start)
	fim := strings.Index(conteudo, end)
	if ini >= 0 && fim >= ini {
		fim += len(end)
		antes := strings.TrimRight(conteudo[:ini], "\n")
		depois := strings.TrimLeft(conteudo[fim:], "\n")
		if antes == "" && depois == "" {
			return bloco + "\n"
		}
		if antes == "" {
			return bloco + "\n" + depois
		}
		if depois == "" {
			return antes + "\n" + bloco + "\n"
		}
		return antes + "\n" + bloco + "\n" + depois
	}
	if !strings.HasSuffix(conteudo, "\n") {
		conteudo += "\n"
	}
	return conteudo + "\n" + bloco + "\n"
}

func aplicarAliasNasOperacoes(cfg *Config, alias, usarEm string) ([]string, error) {
	usarEm = strings.TrimSpace(usarEm)
	if usarEm == "" {
		return nil, nil
	}
	set := map[string]bool{}
	for _, op := range strings.Split(usarEm, ",") {
		op = strings.TrimSpace(op)
		if op == "" {
			continue
		}
		if !operacaoValida(op) {
			return nil, fmt.Errorf("operacao invalida em --usar-em: %s", op)
		}
		set[op] = true
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("--usar-em foi informado, mas sem operacoes validas")
	}
	if cfg.Motores.Operacoes == nil {
		cfg.Motores.Operacoes = map[string]string{}
	}
	var atualizadas []string
	for _, op := range operacoesValidas {
		if !set[op] {
			continue
		}
		cfg.Motores.Operacoes[op] = alias
		atualizadas = append(atualizadas, op)
	}
	return atualizadas, nil
}

func inserirAliasNoFallback(ordem []string, alias string) []string {
	if len(ordem) == 0 {
		return []string{"claude", alias, "codex"}
	}
	alias = normalizarNomeMotor(alias)
	var norm []string
	for _, m := range ordem {
		m = normalizarNomeMotor(m)
		if m == "" {
			continue
		}
		norm = append(norm, m)
	}
	if contemString(norm, alias) {
		return norm
	}
	idxClaude := -1
	for i, m := range norm {
		if m == "claude" {
			idxClaude = i
			break
		}
	}
	if idxClaude < 0 {
		return append(norm, alias)
	}
	out := append([]string{}, norm[:idxClaude+1]...)
	out = append(out, alias)
	out = append(out, norm[idxClaude+1:]...)
	return out
}

func sugerirDirClaudeAlias(alias string) string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	sufixo := strings.TrimPrefix(alias, "claude-")
	sufixo = strings.TrimPrefix(sufixo, "claude_")
	if sufixo == "" || sufixo == alias {
		sufixo = alias
	}
	return filepath.Join(home, ".claude-"+sufixo)
}
