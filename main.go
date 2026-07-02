// Praxis — orquestrador de fases para Claude Code.
//
// Executa as fases de um plano (.md) uma a uma, cada fase numa execucao
// independente do `claude -p` (contexto limpo), com gates deterministicos
// (build/lint/test), ciclo de correcao, revisor com veredito estruturado e
// commit local por fase (sem push). O conhecimento entre fases fica no
// proprio plano (Registro de Andamento).
package main

import (
	"fmt"
	"os"
)

const versao = "0.1.0"

const ajuda = `Praxis ` + versao + ` — orquestrador de fases para Claude Code

Executa as fases de um plano (.md) uma a uma, cada fase numa execucao
independente do "claude -p" (contexto limpo):

  executor -> gates deterministicos (build/lint/test) -> corretor (se falhar)
           -> revisor (contexto limpo, veredito JSON)  -> commit local (sem push)

USO:
  praxis inicializar [--plano <arquivo.md>] [--add-dirs <d1,d2>] [--modelo <m>] [--versionar sim|nao] [--raiz <dir>]
      Prepara um projeto: pergunta o caminho do plano do usuario, usa o Claude
      para quebra-lo em micro-fases (cada uma executavel numa unica execucao
      do Opus), edita o plano com a estrutura de fases e gera os arquivos de
      acompanhamento (fases.csv + autopilot.json + prompts).

  praxis executar [fases] [--forcar] [--raiz <dir>]
      Sem argumento: executa em sequencia todas as fases prontas
      (status=pendente, dependencias concluidas, sem requer_humano), parando
      quando a fila acabar, uma fase falhar ou so restarem fases bloqueadas.
      Com argumento ("2d" ou "2d,2e"): executa apenas essas fases, em ordem.
      --forcar ignora a checagem de dependencias (so no modo com argumento).

  praxis status [--raiz <dir>]
      Mostra a fila de fases (fases.csv).

  praxis ajuda | --help
      Esta ajuda.

ARQUIVOS NECESSARIOS (criados pelo "inicializar", na raiz do projeto):
  <plano>.md                 Plano canonico com as fases: checkboxes, "Depende
                             de:" e um Registro de Andamento — que e a memoria
                             compartilhada entre as fases (cada execucao le e
                             atualiza este arquivo).
  automacao/autopilot.json   Configuracao: caminho do plano, modelo, add_dirs
                             (outros repositorios que o Claude pode editar),
                             orcamento (max_budget_usd) e timeout por execucao,
                             limites de correcao/revisao e os GATES: comandos
                             deterministicos de verificacao (build/lint/test)
                             que o orquestrador roda apos cada fase — sem
                             confiar no autorrelato do modelo.
  automacao/fases.csv        Fila de fases (editavel no Excel; delimitador ';').
                             Colunas: fase;titulo;status;depende_de;requer_humano;
                             gate_extra;modelo;tentativas;custo_usd;concluido_em;observacao
                             status: pendente|executando|concluida|falhou|bloqueada|adiada
                             depende_de: fases separadas por "+" (ex.: 2f+3e)
                             requer_humano=sim: o runner nunca executa (hardware
                             fisico, aprovacao externa); marca como bloqueada.
                             gate_extra: nome de um gate extra do autopilot.json
                             (ex.: testes de integracao mais lentos).
  automacao/prompts/*.md     Prompts do executor/corretor/revisor/inicializador.
                             Personalizaveis; se apagados, os padroes embutidos
                             sao usados (e recriados pelo "inicializar").
  automacao/logs/            Um .jsonl por execucao do claude, logs dos gates e
                             um RESUMO-<data>.md ao final de cada rodada.

FLUXO TIPICO:
  1. Escreva (ou gere com o Claude) o plano do projeto num .md.
  2. "praxis inicializar" -> informe o caminho do plano.
  3. Revise automacao/fases.csv e autopilot.json (gates, dependencias, budget).
  4. "praxis executar" e saia do computador. Cada fase vira um commit local
     descrevendo o que foi feito; nada e enviado (git push manual, apos revisao).
  5. "praxis status" para acompanhar; logs em automacao/logs/.

REQUISITOS: "claude" (Claude Code CLI) logado, "git" e os toolchains dos gates
(ex.: go, npm, python) no PATH. Rode a partir da raiz do projeto (ou use --raiz).
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
