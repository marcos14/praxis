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
  "notificar":{"type":"boolean"},
  "gate_extra":{"type":"string"},
  "observacao":{"type":"string"}}}},
"gates":{"type":"array","items":{"type":"object","required":["nome","dir","comandos"],"properties":{
  "nome":{"type":"string"},"dir":{"type":"string"},"somente_se_mudou":{"type":"boolean"},
  "comandos":{"type":"array","items":{"type":"string"}}}}},
"gates_extra":{"type":"array","items":{"type":"object","required":["nome","comandos"],"properties":{
  "nome":{"type":"string"},"dir":{"type":"string"},
  "comandos":{"type":"array","items":{"type":"string"}}}}}}}`

type faseInit struct {
	Fase         string   `json:"fase"`
	Titulo       string   `json:"titulo"`
	Status       string   `json:"status"`
	DependeDe    []string `json:"depende_de"`
	RequerHumano bool     `json:"requer_humano"`
	Notificar    bool     `json:"notificar"`
	GateExtra    string   `json:"gate_extra"`
	Observacao   string   `json:"observacao"`
}

type saidaInit struct {
	Fases      []faseInit  `json:"fases"`
	Gates      []Gate      `json:"gates"`
	GatesExtra []GateExtra `json:"gates_extra"`
}

// cmdInicializar prepara um projeto para o autopilot.
func cmdInicializar(argv []string) error {
	fs := flag.NewFlagSet("inicializar", flag.ExitOnError)
	raizFlag := fs.String("raiz", "", "raiz do projeto (padrao: diretorio atual)")
	planoFlag := fs.String("plano", "", "caminho do plano .md, relativo a raiz (senao, pergunta)")
	motorFlag := fs.String("motor", "", "harness para planejar/executar inicialmente: claude|codex (senao, pergunta)")
	modeloFlag := fs.String("modelo", "", "modelo legado do Claude; use motores.modelos no autopilot.json para configurar por harness")
	addDirsFlag := fs.String("add-dirs", "", "diretorios adicionais editaveis pelo harness, separados por virgula")
	versionarFlag := fs.String("versionar", "", "versionar o estado do Praxis (automacao/) no git: sim|nao (padrao: sim)")
	webhookFlag := fs.String("webhook", "", "ativar notificacoes por webhook no autopilot.json: sim|nao")
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
	if addDirsBruto == "" && *planoFlag == "" {
		addDirsBruto = perguntar("Diretorios adicionais que o harness pode editar (outros repos), separados por virgula (vazio = nenhum)", "")
	}
	var addDirs []string
	for _, d := range strings.Split(addDirsBruto, ",") {
		if d = strings.TrimSpace(d); d != "" {
			addDirs = append(addDirs, d)
		}
	}

	if err := os.MkdirAll(dirLogs(raiz), 0o755); err != nil {
		return err
	}
	if err := materializarPrompts(raiz); err != nil {
		return err
	}
	cfg := configPadrao()
	if existente, err := carregarConfig(raiz); err == nil {
		cfg = existente
	}
	cfg.Plano = plano
	cfg.AddDirs = addDirs
	if modelo := strings.TrimSpace(*modeloFlag); modelo != "" {
		cfg.Modelo = modelo
		cfg.Motores.Modelos["claude"] = modelo
	}

	motorEscolhido := normalizarNomeMotor(*motorFlag)
	if motorEscolhido == "" && *planoFlag == "" {
		motorEscolhido = perguntarMotorInicializacao(perguntar, cfg)
	}
	if motorEscolhido != "" {
		if err := aplicarMotorInicializacao(cfg, motorEscolhido); err != nil {
			return err
		}
	}

	versionar := cfg.VersionarAutomacao
	if v := strings.TrimSpace(*versionarFlag); v != "" {
		versionar = ehSim(v)
	} else if *planoFlag == "" {
		sug := "nao"
		if versionar {
			sug = "sim"
		}
		versionar = ehSim(perguntar("Versionar o estado do Praxis (automacao/fases.csv) no git? Recomendado p/ nao perder o progresso em projetos grandes; 'nao' ignora a pasta automacao/ inteira", sug))
	}
	cfg.VersionarAutomacao = versionar

	ativarWebhook := false
	if v := strings.TrimSpace(*webhookFlag); v != "" {
		ativarWebhook = ehSim(v)
	} else if *planoFlag == "" {
		ativarWebhook = ehSim(perguntar("Ativar notificacoes por webhook no autopilot.json (Telegram/Discord/Slack/Google Chat/generico)?", "nao"))
	}
	if ativarWebhook {
		fmt.Printf("  configure os canais em %s; %s sera gerado sem segredos.\n", caminhoConfig(raiz), caminhoConfigExemplo(raiz))
	}
	if err := salvarConfig(raiz, cfg); err != nil {
		return err
	}
	if err := garantirGitignore(raiz, cfg.VersionarAutomacao); err != nil {
		fmt.Printf("AVISO: nao consegui ajustar o .gitignore: %v\n", err)
	}

	notif := carregarNotificador(raiz)
	notif.enviarEvento("planejamento_iniciado", "Praxis: planejamento iniciado", fmt.Sprintf("Plano: %s", plano))

	motorPlanejar := motorParaOperacao(cfg, "planejar")
	modeloPlanejar := modeloParaMotor(cfg, motorPlanejar)
	fmt.Printf("\n> analisando `%s` e quebrando em micro-fases (%s", plano, motorPlanejar)
	if modeloPlanejar != "" {
		fmt.Printf(", modelo %s", modeloPlanejar)
	}
	fmt.Println(")...")
	tpl, err := carregarPrompt(raiz, "inicializador.md")
	if err != nil {
		return err
	}
	res, motorUsado, err := rodarComFallback(raiz, "planejar", motorPlanejar, OpcoesRun{
		Raiz: raiz, Prompt: renderPrompt(tpl, map[string]string{"PLANO": plano}),
		Modelo: modeloPlanejar, Esforco: esforcoParaMotor(cfg, motorPlanejar), AddDirs: addDirs, BudgetUSD: cfg.MaxBudgetUSD,
		TimeoutMin: cfg.TimeoutMin, Schema: schemaInit,
		ProibirCommit: true, RotuloLog: "inicializar",
	}, novoEstadoFallback())
	if err != nil {
		return err
	}
	if res.IsError {
		return fmt.Errorf("inicializacao falhou (%s); log: %s", res.Subtipo, res.LogPath)
	}
	var saida saidaInit
	if err := decodificarEstruturado(res, &saida); err != nil {
		return fmt.Errorf("nao consegui ler o JSON de fases (%v); log: %s", err, res.LogPath)
	}
	if len(saida.Fases) == 0 {
		return fmt.Errorf("o inicializador nao retornou fases; log: %s", res.LogPath)
	}

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
			RequerHumano: fi.RequerHumano, Notificar: fi.Notificar, GateExtra: fi.GateExtra, Observacao: fi.Observacao,
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
	notif.enviarEvento("inicializacao_concluida", "Praxis: inicializacao concluida",
		fmt.Sprintf("%d fases geradas para %s", len(fases), plano))

	pendentes := 0
	for _, f := range fases {
		if f.Status == StPendente {
			pendentes++
		}
	}
	fmt.Printf(`
Inicializacao concluida (motor %s, custo US$ %.2f):
  plano:    %s (reestruturado em %d fases, %d pendentes)
  fila:     %s
  config:   %s (%d gates)
  prompts:  %s

Proximos passos:
  1. Revise o plano, o fases.csv (dependencias, requer_humano) e o autopilot.json (gates, budget).
  2. No autopilot.json, voce pode editar motores.operacoes para escolher o harness por funcao
     (planejar/executar/corrigir/revisar), motores.modelos para sobrescrever modelos e
     motores.esforcos para ajustar o esforco.
  3. Rode: praxis executar
`, motorUsado, res.CustoUSD, plano, len(fases), pendentes, csvPath, caminhoConfig(raiz), len(cfg.Gates), dirPrompts(raiz))
	return nil
}

func perguntarMotorInicializacao(perguntar func(string, string) string, cfg *Config) string {
	instalados := motoresInstalados()
	atual := motorParaOperacao(cfg, "planejar")
	padrao := atual
	if !contemString(instalados, atual) && len(instalados) > 0 {
		padrao = instalados[0]
	}
	if len(instalados) > 0 {
		fmt.Printf("Harnesses encontrados no PATH: %s\n", strings.Join(instalados, ", "))
	} else {
		fmt.Printf("AVISO: nao encontrei nenhum harness conhecido no PATH (%s).\n", strings.Join(motoresConhecidos(), ", "))
	}
	resp := perguntar("Harness inicial para planejar/executar (modelo default do harness; depois edite por funcao no autopilot.json)", padrao)
	return normalizarNomeMotor(resp)
}

func aplicarMotorInicializacao(cfg *Config, motor string) error {
	motor = normalizarNomeMotor(motor)
	if _, err := selecionarMotor(motor); err != nil {
		return err
	}
	if !motorInstalado(motor) {
		fmt.Printf("AVISO: harness %q nao foi encontrado no PATH; vou salvar a config mesmo assim.\n", motor)
	}
	if cfg.Motores.Operacoes == nil {
		cfg.Motores.Operacoes = map[string]string{}
	}
	for _, op := range operacoesValidas {
		cfg.Motores.Operacoes[op] = motor
	}
	cfg.Motor = motor
	cfg.Motores.Fallback.Ordem = ordemFallbackInicial(motor)
	return nil
}

func ordemFallbackInicial(primeiro string) []string {
	primeiro = normalizarNomeMotor(primeiro)
	ordem := []string{}
	if primeiro != "" {
		ordem = append(ordem, primeiro)
	}
	for _, motor := range motoresInstalados() {
		if motor != primeiro {
			ordem = append(ordem, motor)
		}
	}
	for _, motor := range motoresConhecidos() {
		if motor != primeiro && !contemString(ordem, motor) {
			ordem = append(ordem, motor)
		}
	}
	return ordem
}

func contemString(vs []string, alvo string) bool {
	for _, v := range vs {
		if v == alvo {
			return true
		}
	}
	return false
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
