package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const schemaInit = `{"type":"object","required":["fases","gates"],"properties":{
"fases":{"type":"array","items":{"type":"object","required":["fase","titulo","status"],"properties":{
  "fase":{"type":"string"},"titulo":{"type":"string"},
  "status":{"type":"string","enum":["pendente","concluida","adiada"]},
  "depende_de":{"type":"array","items":{"type":"string"}},
  "requer_humano":{"type":"boolean"},
  "gate_extra":{"type":"string"},
  "observacao":{"type":"string"}}}},
"gates":{"type":"array","items":{"type":"object","required":["nome","dir","comandos"],"properties":{
  "nome":{"type":"string"},"dir":{"type":"string"},"somente_se_mudou":{"type":"boolean"},
  "comandos":{"type":"array","items":{"type":"string"}}}}},
"gates_extra":{"type":"array","items":{"type":"object","required":["nome","comandos"],"properties":{
  "nome":{"type":"string"},
  "comandos":{"type":"array","items":{"type":"string"}}}}}}}`

type faseInit struct {
	Fase         string   `json:"fase"`
	Titulo       string   `json:"titulo"`
	Status       string   `json:"status"`
	DependeDe    []string `json:"depende_de"`
	RequerHumano bool     `json:"requer_humano"`
	GateExtra    string   `json:"gate_extra"`
	Observacao   string   `json:"observacao"`
}

type saidaInit struct {
	Fases      []faseInit  `json:"fases"`
	Gates      []Gate      `json:"gates"`
	GatesExtra []GateExtra `json:"gates_extra"`
}

// cmdInicializar prepara um projeto para o autopilot: pergunta o caminho do
// plano do usuario, usa o Claude para quebra-lo em micro-fases (editando o
// proprio plano) e gera automacao/fases.csv + autopilot.json + prompts.
func cmdInicializar(argv []string) error {
	fs := flag.NewFlagSet("inicializar", flag.ExitOnError)
	raizFlag := fs.String("raiz", "", "raiz do projeto (padrao: diretorio atual)")
	planoFlag := fs.String("plano", "", "caminho do plano .md, relativo a raiz (senao, pergunta)")
	modeloFlag := fs.String("modelo", "", "modelo para executar as fases (padrao: opus)")
	addDirsFlag := fs.String("add-dirs", "", "diretorios adicionais editaveis pelo Claude, separados por virgula")
	versionarFlag := fs.String("versionar", "", "versionar o estado do Praxis (automacao/) no git: sim|nao (padrao: sim)")
	fs.Parse(argv)
	raiz := *raizFlag
	if raiz == "" {
		raiz = resolverRaiz("")
	}

	entrada := bufio.NewReader(os.Stdin)
	perguntar := func(msg, padrao string) string {
		if padrao != "" {
			fmt.Printf("%s [%s]: ", msg, padrao)
		} else {
			fmt.Printf("%s: ", msg)
		}
		linha, _ := entrada.ReadString('\n')
		linha = strings.TrimSpace(linha)
		if linha == "" {
			return padrao
		}
		return linha
	}

	// 1) coleta das respostas (flags tem precedencia; senao pergunta)
	plano := *planoFlag
	if plano == "" {
		sugestao := ""
		if _, err := os.Stat(filepath.Join(raiz, "PLANO.md")); err == nil {
			sugestao = "PLANO.md"
		}
		plano = perguntar("Caminho do plano (.md), relativo a raiz do projeto", sugestao)
	}
	if plano == "" {
		return fmt.Errorf("informe o caminho do plano (.md)")
	}
	plano = filepath.ToSlash(plano)
	if _, err := os.Stat(filepath.Join(raiz, plano)); err != nil {
		return fmt.Errorf("plano nao encontrado: %s", filepath.Join(raiz, plano))
	}

	addDirsBruto := *addDirsFlag
	if addDirsBruto == "" && *planoFlag == "" { // modo interativo
		addDirsBruto = perguntar("Diretorios adicionais que o Claude pode editar (outros repos), separados por virgula (vazio = nenhum)", "")
	}
	var addDirs []string
	for _, d := range strings.Split(addDirsBruto, ",") {
		if d = strings.TrimSpace(d); d != "" {
			addDirs = append(addDirs, d)
		}
	}

	modelo := *modeloFlag
	if modelo == "" && *planoFlag == "" {
		modelo = perguntar("Modelo para executar as fases", "opus")
	}
	if modelo == "" {
		modelo = "opus"
	}

	// 2) estrutura de arquivos
	if err := os.MkdirAll(dirLogs(raiz), 0o755); err != nil {
		return err
	}
	if err := materializarPrompts(raiz); err != nil {
		return err
	}
	cfg := configPadrao()
	if existente, err := carregarConfig(raiz); err == nil {
		cfg = existente // preserva ajustes (budget, timeout, gates...) em re-inicializacao
	}
	cfg.Plano = plano
	cfg.Modelo = modelo
	cfg.AddDirs = addDirs

	// versionar o estado do Praxis (automacao/) no git? Versionando, o Praxis
	// faz um commit de bookkeeping por fase (progresso no historico, arvore
	// limpa); senao, joga a pasta automacao/ inteira no .gitignore.
	versionar := cfg.VersionarAutomacao // padrao: config existente ou true
	if v := strings.TrimSpace(*versionarFlag); v != "" {
		versionar = ehSim(v)
	} else if *planoFlag == "" { // modo interativo
		sug := "nao"
		if versionar {
			sug = "sim"
		}
		versionar = ehSim(perguntar("Versionar o estado do Praxis (automacao/fases.csv) no git? Recomendado p/ nao perder o progresso em projetos grandes; 'nao' ignora a pasta automacao/ inteira", sug))
	}
	cfg.VersionarAutomacao = versionar

	// 3) o Claude analisa o plano, quebra em micro-fases (editando o .md) e
	// devolve fases + gates em JSON estruturado
	fmt.Printf("\n▶ analisando `%s` e quebrando em micro-fases (claude, modelo %s)...\n", plano, modelo)
	tpl, err := carregarPrompt(raiz, "inicializador.md")
	if err != nil {
		return err
	}
	res, err := rodarClaude(OpcoesClaude{
		Raiz: raiz, Prompt: renderPrompt(tpl, map[string]string{"PLANO": plano}),
		Modelo: modelo, AddDirs: addDirs, BudgetUSD: cfg.MaxBudgetUSD,
		TimeoutMin: cfg.TimeoutMin, JSONSchema: schemaInit,
		Disallowed: proibidosSempre, RotuloLog: "inicializar",
	})
	if err != nil {
		return err
	}
	if res.IsError {
		return fmt.Errorf("inicializacao falhou (%s) — log: %s", res.Subtipo, res.LogPath)
	}
	var saida saidaInit
	if err := decodificarEstruturado(res, &saida); err != nil {
		return fmt.Errorf("nao consegui ler o JSON de fases (%v) — log: %s", err, res.LogPath)
	}
	if len(saida.Fases) == 0 {
		return fmt.Errorf("o inicializador nao retornou fases — log: %s", res.LogPath)
	}

	// 4) grava fases.csv (com backup, se ja existir) e autopilot.json
	csvPath := caminhoCSV(raiz)
	if _, err := os.Stat(csvPath); err == nil {
		backup := strings.TrimSuffix(csvPath, ".csv") + "-" + agoraTS() + ".bak.csv"
		if err := os.Rename(csvPath, backup); err == nil {
			fmt.Printf("  fases.csv existente preservado em %s\n", backup)
		}
	}
	var fases []*Fase
	for _, fi := range saida.Fases {
		st := fi.Status
		if st == "" {
			st = StPendente
		}
		fases = append(fases, &Fase{
			Fase: fi.Fase, Titulo: fi.Titulo, Status: st, DependeDe: fi.DependeDe,
			RequerHumano: fi.RequerHumano, GateExtra: fi.GateExtra, Observacao: fi.Observacao,
		})
	}
	if err := salvarFases(csvPath, fases); err != nil {
		return err
	}
	if len(saida.Gates) > 0 {
		cfg.Gates = saida.Gates
	}
	if len(saida.GatesExtra) > 0 {
		cfg.GatesExtra = saida.GatesExtra
	}
	if err := salvarConfig(raiz, cfg); err != nil {
		return err
	}
	if err := garantirGitignore(raiz, cfg.VersionarAutomacao); err != nil {
		fmt.Printf("AVISO: nao consegui ajustar o .gitignore: %v\n", err)
	}

	// 5) resumo
	pendentes := 0
	for _, f := range fases {
		if f.Status == StPendente {
			pendentes++
		}
	}
	fmt.Printf(`
Inicializacao concluida (custo US$ %.2f):
  plano:    %s (reestruturado em %d fases, %d pendentes)
  fila:     %s
  config:   %s (%d gates)
  prompts:  %s

Proximos passos:
  1. Revise o plano, o fases.csv (dependencias, requer_humano) e o autopilot.json (gates, budget).
  2. Rode: praxis executar
`, res.CustoUSD, plano, len(fases), pendentes, csvPath, caminhoConfig(raiz), len(cfg.Gates), dirPrompts(raiz))
	return nil
}

// ehSim interpreta respostas afirmativas (sim/s/y/yes/true/1); qualquer outra
// coisa e tratada como negativa.
func ehSim(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "sim", "s", "y", "yes", "true", "1":
		return true
	}
	return false
}
