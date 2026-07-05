// Praxis: orquestrador de fases com motores de codigo intercambiaveis.
package main

import (
	"fmt"
	"os"
)

const versao = "0.1.0"

const ajuda = `Praxis ` + versao + ` - orquestrador de fases

Executa fases de um plano Markdown em contexto limpo:

  executor -> gates -> corretor -> revisor -> atualizacao do plano -> commit local

Motores suportados: claude e codex. A escolha e feita por operacao em
automacao/autopilot.json, que e relido antes de cada run.

USO:
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
      notificacoes e auth em /api/config. Edicao exige Basic Auth ativo.

  praxis auth [--user <u>] [--pass <s>]
      Gera a credencial base64 para colar em autopilot.json, bloco "painel".

CONFIG:
  automacao/autopilot.json e a unica fonte de verdade:
    - motores.operacoes: planejar, executar, corrigir, revisar
    - motores.modelos: modelo default por motor
    - motores.esforcos: esforco default por motor (ex.: high)
    - motores.fallback: ativo + ordem de fallback
    - notificacoes.canais e notificacoes.eventos
    - painel.auth_ativo, credencial_base64 e bind

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
