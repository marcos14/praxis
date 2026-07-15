// Praxis: orquestrador de fases com motores de codigo intercambiaveis.
package main

import (
	"fmt"
	"os"
	"strings"
)

const versao = "0.1.0"

const ajuda = `Praxis ` + versao + ` - orquestrador de fases

Executa fases de um plano Markdown em contexto limpo:

  executor -> gates -> corretor -> revisor -> atualizacao do plano -> commit local

Motores suportados: claude e codex. A escolha e feita por operacao em
automacao/autopilot.json, que e relido antes de cada run.

USO:
  praxis --claude-alias-create <alias> [--dir <caminho>] [--usar-em <ops>] [--sem-fallback] [--sem-shell-alias] [--raiz <dir>]
      Cria/atualiza um alias de conta Claude em motores.claude_config_dirs e
	  cria atalhos no PowerShell (ex.: claude-alt) para facilitar o login da
	  segunda conta (CLAUDE_CONFIG_DIR).
      Exemplo: praxis --claude-alias-create claude_alt --usar-em executar,corrigir

  praxis inicializar [--plano <arquivo.md>] [--add-dirs <d1,d2>] [--motor claude|codex] [--modelo <m>] [--versionar sim|nao] [--webhook sim|nao] [--raiz <dir>]
      Prepara um projeto, quebra o plano em micro-fases e gera fases.csv,
      autopilot.json, autopilot.exemplo.json, prompts e logs. No modo
      interativo, lista os harnesses encontrados no PATH e pergunta qual usar.

  praxis executar [fases] [--forcar] [--painel] [--porta <n>] [--raiz <dir>]
      Executa todas as fases prontas ou apenas as fases informadas
      ("2d" ou "2d,2e"). Usa motores.operacoes.executar/corrigir/revisar.

  praxis status [--raiz <dir>]
      Mostra a fila de fases.

  praxis painel [--porta <n>] [--abrir sim|nao] [--raiz <dir>]
      Sobe o painel web com status, logs ao vivo e edicao de motores,
      notificacoes e do status das fases em /api/config e /api/fase-status.
      A edicao exige colar o token do painel (botao "Entrar").

  praxis auth [--regenerar] [--raiz <dir>]
      Mostra o token do painel (bloco "painel" de autopilot.json). Com
      --regenerar, cria um novo token e invalida o anterior.

CONFIG:
  automacao/autopilot.json e a unica fonte de verdade:
    - motores.operacoes: planejar, executar, corrigir, revisar
    - motores.modelos: modelo default por motor
    - motores.esforcos: esforco default por motor (ex.: high)
    - motores.fallback: ativo + ordem de fallback
    - notificacoes.canais e notificacoes.eventos
    - painel.token (gerado automaticamente) e painel.bind

  Exemplo: Claude planeja/revisa e Codex executa/corrige:
    "operacoes": {"planejar":"claude","executar":"codex","corrigir":"codex","revisar":"claude"}
  Se quiser usar só Codex desde a inicialização:
    praxis inicializar --motor codex

FALLBACK:
  Se fallback estiver ativo e um motor atingir limite de uso, o Praxis tenta o
  proximo motor da ordem configurada. Sem fallback disponivel, espera o reset da
  franquia, aceita Enter para pausar e Ctrl+C para interromper.

NOTIFICACOES:
  Eventos importantes vem ligados por padrao: inicializacao_concluida,
  troca_de_harness, franquia_esgotada, fase_concluida, marco_concluido,
  rodada_concluida, rodada_parou e erro_interno. Os demais podem ser ligados no
  autopilot.json ou pelo painel.

SEGREDOS:
  autopilot.json contem tokens e credenciais, portanto e ignorado pelo git.
  autopilot.exemplo.json nao contem segredos e pode ser versionado. O progresso
  da fila continua em fases.csv quando versionar_automacao=true.

REQUISITOS:
  git, os toolchains dos gates e pelo menos um CLI de motor logado:
  claude (Claude Code) e/ou codex (OpenAI Codex CLI).
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(ajuda)
		os.Exit(2)
	}
	if alias, resto, ok, err := parseClaudeAliasCreateArg(os.Args[1:]); ok {
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nERRO: %v\n", err)
			os.Exit(1)
		}
		if err := cmdClaudeAliasCreate(alias, resto); err != nil {
			fmt.Fprintf(os.Stderr, "\nERRO: %v\n", err)
			os.Exit(1)
		}
		return
	}
	var err error
	switch os.Args[1] {
	case "inicializar", "init":
		err = cmdInicializar(os.Args[2:])
	case "executar", "run":
		err = cmdExecutar(os.Args[2:])
	case "status":
		err = cmdStatus(os.Args[2:])
	case "painel", "web":
		err = cmdPainel(os.Args[2:])
	case "auth":
		err = cmdAuth(os.Args[2:])
	case "claude-alias-create":
		if len(os.Args) < 3 {
			err = fmt.Errorf("uso: praxis claude-alias-create <alias> [--dir <caminho>] [--usar-em <ops>] [--sem-fallback] [--sem-shell-alias] [--raiz <dir>]")
			break
		}
		err = cmdClaudeAliasCreate(strings.TrimSpace(os.Args[2]), os.Args[3:])
	case "ajuda", "help", "--help", "-h":
		fmt.Print(ajuda)
	case "versao", "--version", "-v":
		fmt.Println("praxis " + versao)
	default:
		fmt.Fprintf(os.Stderr, "comando desconhecido: %s\n\n", os.Args[1])
		fmt.Print(ajuda)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nERRO: %v\n", err)
		os.Exit(1)
	}
}

func parseClaudeAliasCreateArg(args []string) (alias string, resto []string, ok bool, err error) {
	if len(args) == 0 {
		return "", nil, false, nil
	}
	arg := strings.TrimSpace(args[0])
	if !strings.HasPrefix(arg, "--claude-alias-create") {
		return "", nil, false, nil
	}
	if arg == "--claude-alias-create" {
		if len(args) < 2 || strings.HasPrefix(strings.TrimSpace(args[1]), "-") {
			return "", nil, true, fmt.Errorf("faltou o alias: use `praxis --claude-alias-create <alias>`")
		}
		return strings.TrimSpace(args[1]), args[2:], true, nil
	}
	if strings.HasPrefix(arg, "--claude-alias-create=") {
		alias = strings.TrimSpace(strings.TrimPrefix(arg, "--claude-alias-create="))
		if alias == "" {
			return "", nil, true, fmt.Errorf("faltou o alias apos --claude-alias-create=")
		}
		return alias, args[1:], true, nil
	}
	return "", nil, true, fmt.Errorf("parametro invalido: %s", arg)
}
