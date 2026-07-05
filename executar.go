package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
)

const schemaVeredito = `{"type":"object","required":["veredito","problemas"],"properties":{"veredito":{"type":"string","enum":["APROVADO","REPROVADO"]},"problemas":{"type":"array","items":{"type":"string"}}}}`

type Veredito struct {
	Veredito  string   `json:"veredito"`
	Problemas []string `json:"problemas"`
}

// O commit e responsabilidade do orquestrador — nenhum run pode commitar/pushar.
var proibidosSempre = []string{"Bash(git commit*)", "Bash(git push*)"}

// O revisor tambem nao pode alterar nada.
var proibidosRevisor = append([]string{"Edit", "Write", "NotebookEdit"}, proibidosSempre...)

func cmdExecutar(argv []string) error {
	fs := flag.NewFlagSet("executar", flag.ExitOnError)
	raizFlag := fs.String("raiz", "", "raiz do projeto (padrao: deteccao automatica)")
	forcar := fs.Bool("forcar", false, "ignora a checagem de dependencias (so no modo com fases explicitas)")
	painel := fs.Bool("painel", false, "sobe o painel web de acompanhamento e abre o navegador")
	portaPainel := fs.Int("porta", portaPainelPadrao, "porta HTTP do painel (com --painel)")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	raiz := resolverRaiz(*raizFlag)
	cfg, err := carregarConfig(raiz)
	if err != nil {
		return err
	}
	csvPath := caminhoCSV(raiz)
	fases, err := carregarFases(csvPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// canal de pausa: um leitor de stdin transforma cada Enter num pedido de
	// pausa, consumido durante a espera do reset da franquia. Em servidor sem
	// terminal (stdin fechado), o leitor termina e o canal simplesmente nunca
	// dispara — a execucao segue o fluxo automatico.
	pausaCh := make(chan struct{}, 1)
	go lerStdinParaPausa(pausaCh)
	notif := carregarNotificador(raiz)
	estadoFallback := novoEstadoFallback()

	var estado *estadoExecucao
	if *painel {
		estado = novoEstadoExecucao()
		iniciarPainel(raiz, *portaPainel, true, estado)
	}
	// Sinais (Ctrl+C): a primeira interrupcao encerra a rodada com seguranca
	// — a fase em andamento vira `pausada` (retomavel, sem perder o trabalho).
	// Com o painel no ar, ele continua de pe para mostrar o motivo; uma
	// segunda interrupcao fecha tudo.
	var faseCorrente string
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		for s := range sig {
			if ctx.Err() == nil {
				fmt.Printf("\n⚠ %v recebido — encerrando a fase atual com segurança (pausada). Ctrl+C de novo força a saída.\n", s)
				if estado != nil {
					estado.definir("interrompido", "interrompido pelo usuário ("+s.String()+")")
				}
				cancel()
			} else {
				fmt.Println("\nsaindo.")
				os.Exit(130)
			}
		}
	}()

	var rodadas []*Fase
	var errParada error
	motivoParada := "fila processada"

	executarUma := func(f *Fase) error {
		faseCorrente = f.Fase
		estado.definirFase(f.Fase)
		errFase := pipelineFase(ctx, raiz, cfg, fases, f, pausaCh, notif, estadoFallback)
		if err := salvarFases(csvPath, fases); err != nil {
			fmt.Printf("AVISO: nao consegui salvar %s: %v\n", csvPath, err)
		}
		// registra o estado do Praxis (fases.csv) num commit proprio: o
		// status/conclusao e gravado DEPOIS do commit da fase, entao sem isto
		// o fases.csv ficaria sujo e a pre-checagem da proxima fase abortaria.
		if cfg.VersionarAutomacao {
			if err := commitAutomacao(raiz, fmt.Sprintf("chore(praxis): estado apos Fase %s [%s]", f.Fase, f.Status)); err != nil {
				fmt.Printf("AVISO: nao consegui registrar o estado do Praxis: %v\n", err)
			}
		}
		rodadas = append(rodadas, f)
		return errFase
	}

	// A rodada roda dentro de um recover: um panico numa fase nao pode derrubar
	// o processo (e o painel junto) — vira uma parada com motivo apresentavel.
	func() {
		defer func() {
			if r := recover(); r != nil {
				errParada = fmt.Errorf("erro interno (panic): %v", r)
				motivoParada = fmt.Sprintf("erro interno (panic): %v\n%s", r, primeirasLinhas(string(debug.Stack()), 8))
				fmt.Fprintf(os.Stderr, "\nPANICO capturado: %v\n", r)
				notificarEvento(raiz, "erro_interno", "Praxis: erro interno", motivoParada)
			}
		}()

		if fs.NArg() > 0 {
			// modo explicito: "executar 2d" ou "executar 2d,2e ..."
			for _, nome := range separarLista(fs.Args()) {
				if ctx.Err() != nil {
					break
				}
				if strings.HasPrefix(nome, "-") {
					errParada = fmt.Errorf("flags devem vir antes das fases: `executar %s 2d` (nao `executar 2d %s`)", nome, nome)
					return
				}
				f := buscarFase(fases, nome)
				if f == nil {
					errParada = fmt.Errorf("fase %q nao existe em %s", nome, csvPath)
					return
				}
				if f.Status == StConcluida {
					fmt.Printf("fase %s ja esta concluida — pulando\n", f.Fase)
					continue
				}
				if !*forcar {
					if pend := depsPendentes(fases, f); len(pend) > 0 {
						errParada = fmt.Errorf("fase %s depende de fases nao concluidas: %s (use --forcar para ignorar)",
							f.Fase, strings.Join(pend, ", "))
						return
					}
				}
				if err := executarUma(f); err != nil {
					errParada = err
					motivoParada = err.Error()
					break
				}
			}
		} else {
			// modo sequencia: roda tudo que estiver pronto, ate acabar ou falhar
			for {
				if ctx.Err() != nil {
					break
				}
				f, motivo := proximaPronta(fases)
				if err := salvarFases(csvPath, fases); err != nil { // persiste bloqueadas
					fmt.Printf("AVISO: nao consegui salvar %s: %v\n", csvPath, err)
				}
				if f == nil {
					motivoParada = motivo
					break
				}
				if err := executarUma(f); err != nil {
					errParada = err
					motivoParada = err.Error()
					break
				}
			}
		}
	}()

	// Pausa pelo menu (Enter durante a espera da franquia): a fase ja foi
	// marcada `pausada` no pipeline; nao e falha e a rodada para para o usuario
	// trocar de conta / relogar e retomar depois.
	pausadoPeloMenu := errors.Is(errParada, errPausaSolicitada)
	if pausadoPeloMenu {
		motivoParada = "pausado — retome com 'praxis executar' (a fase continua de onde parou)"
		errParada = nil
	}

	// Interrupcao (Ctrl+C) tambem nao e falha: a fase que estava rodando vira
	// `pausada` (mantendo o trabalho nao commitado) para poder ser retomada sem
	// esbarrar na pre-checagem de arvore limpa.
	if ctx.Err() != nil {
		if f := buscarFase(fases, faseCorrente); f != nil &&
			(f.Status == StExecutando || f.Status == StFalhou || f.Status == StPausada) {
			f.Status = StPausada
			f.Observacao = "pausada (Ctrl+C) — retome com 'praxis executar'"
			if err := salvarFases(csvPath, fases); err != nil {
				fmt.Printf("AVISO: nao consegui salvar %s: %v\n", csvPath, err)
			}
		}
		if motivoParada == "" || errParada != nil {
			motivoParada = "pausado pelo usuário (Ctrl+C) — retome com 'praxis executar'"
		}
		errParada = nil
	}

	pausado := pausadoPeloMenu || ctx.Err() != nil

	caminhoResumo := escreverResumo(raiz, rodadas, motivoParada)
	titulo := tituloNotificacao(rodadas, errParada)
	if pausado {
		titulo = "Praxis pausado — retome com 'praxis executar'"
	}
	notificar(titulo, motivoParada+"\nResumo: "+caminhoResumo)
	eventoRodada := "rodada_concluida"
	if errParada != nil || pausado {
		eventoRodada = "rodada_parou"
	}
	notif.enviarEvento(eventoRodada, titulo, motivoParada+"\n"+resumoAndamento(fases))

	// Com o painel no ar, nunca deixamos o processo morrer sozinho: finaliza o
	// estado (que o painel mostra num banner) e segue servindo o painel ate o
	// usuario fechar com Ctrl+C.
	if estado != nil {
		situacao := "concluido"
		switch {
		case pausado:
			situacao = "pausado"
		case ctx.Err() != nil:
			situacao = "interrompido"
		case errParada != nil && strings.HasPrefix(motivoParada, "erro interno (panic)"):
			situacao = "erro"
		case errParada != nil:
			situacao = "falhou"
		}
		estado.encerrar(situacao, motivoParada)
		fmt.Println("\n────────────────────────────────────────────────────")
		fmt.Printf("Execução encerrada (%s). Motivo:\n  %s\n", situacao, motivoParada)
		fmt.Println("O painel continua no ar para você conferir o estado.")
		fmt.Println("Pressione Ctrl+C para fechar o painel.")
		select {} // bloqueia; o handler de sinal encerra o processo
	}

	return errParada
}

func tituloNotificacao(rodadas []*Fase, errParada error) string {
	ok := 0
	for _, f := range rodadas {
		if f.Status == StConcluida {
			ok++
		}
	}
	if errParada != nil {
		return fmt.Sprintf("Praxis PAROU — %d fase(s) concluida(s), 1 falhou", ok)
	}
	return fmt.Sprintf("Praxis terminou — %d fase(s) concluida(s)", ok)
}

// lerStdinParaPausa le linhas de stdin e, a cada Enter, sinaliza um pedido de
// pausa (nao-bloqueante). Termina quando stdin fecha (EOF) — o que acontece em
// servidor sem terminal interativo, caso em que a pausa manual simplesmente
// nao fica disponivel e a execucao segue o fluxo automatico.
func lerStdinParaPausa(ch chan<- struct{}) {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func separarLista(args []string) []string {
	var nomes []string
	for _, a := range args {
		for _, n := range strings.Split(a, ",") {
			if n = strings.TrimSpace(n); n != "" {
				nomes = append(nomes, n)
			}
		}
	}
	return nomes
}

func depsPendentes(fases []*Fase, f *Fase) []string {
	var pend []string
	for _, d := range f.DependeDe {
		dep := buscarFase(fases, d)
		if dep == nil || dep.Status != StConcluida {
			pend = append(pend, d)
		}
	}
	return pend
}

// pipelineFase executa o ciclo completo de uma fase:
// executor -> gates (com correcoes) -> revisor (com correcao) -> guarda do
// plano -> commit local. Devolve erro quando a fase falha (ja marcada no CSV).
// ctx permite interromper (Ctrl+C) o run do claude em andamento; pausaCh
// habilita a pausa manual durante a espera da franquia; notif dispara os
// avisos remotos (franquia esgotada, marcos concluidos).
func pipelineFase(ctx context.Context, raiz string, cfg *Config, todas []*Fase, f *Fase, pausaCh <-chan struct{}, notif *Notificador, estadoFallback *EstadoFallback) error {
	fmt.Printf("\n════════ Fase %s — %s ════════\n", f.Fase, f.Titulo)

	// retomada: uma fase `pausada` foi interrompida no meio e tem trabalho nao
	// commitado que pertence a ela mesma — nesse caso NAO exigimos arvore limpa
	// (seria bloqueada pelo proprio progresso da fase); nos demais casos a
	// pre-checagem protege o trabalho do usuario de entrar no commit da fase.
	retomando := f.Status == StPausada
	repos := reposEnvolvidos(raiz, cfg)
	if retomando {
		fmt.Println("↩ retomando fase pausada — mantendo o trabalho não commitado da fase")
	} else {
		for _, r := range repos {
			limpo, err := arvoreLimpaFora(r, raiz)
			if err != nil {
				return err
			}
			if !limpo {
				return fmt.Errorf("arvore com mudancas nao commitadas em %s — commite ou guarde (stash) antes de rodar o Praxis", r)
			}
		}
	}

	f.Status = StExecutando
	f.Tentativas++
	_ = salvarFases(caminhoCSV(raiz), todas)
	notif.enviarEvento("fase_iniciada", fmt.Sprintf("Praxis: Fase %s iniciada", f.Fase), f.Titulo)

	custo := 0.0
	motorExecutor := "claude"
	falha := func(formato string, a ...any) error {
		msg := fmt.Sprintf(formato, a...)
		f.Status = StFalhou
		f.CustoUSD += custo
		f.Observacao = primeirasLinhas(msg, 1)
		return fmt.Errorf("fase %s falhou: %s", f.Fase, msg)
	}
	// pausou reconhece o pedido de pausa; pausar marca a fase como retomavel
	// (sem falha) preservando o custo ja gasto e devolve o sentinela.
	pausou := func(err error) bool { return errors.Is(err, errPausaSolicitada) }
	pausar := func() error {
		f.Status = StPausada
		f.CustoUSD += custo
		f.Observacao = "pausada — retome com 'praxis executar'"
		fmt.Printf("⏸️  Fase %s pausada — retome depois com 'praxis executar' (continua de onde parou).\n", f.Fase)
		notif.enviarEvento("pausa", fmt.Sprintf("Praxis: Fase %s pausada", f.Fase), f.Observacao)
		return errPausaSolicitada
	}
	// onEspera dispara o aviso remoto quando a franquia de tokens esgota, para
	// voce saber (mesmo longe do servidor) que a execucao esta esperando o reset.
	onEspera := func(detalhe string) {
		notif.enviarEvento("franquia_esgotada", "Praxis: franquia de tokens esgotada",
			fmt.Sprintf("Fase %s — %s\n%s\nRetomo automaticamente após o reset (ou pause no terminal para trocar de conta).", f.Fase, f.Titulo, detalhe))
	}
	vars := map[string]string{"FASE": f.Fase, "TITULO": f.Titulo, "PLANO": cfg.Plano}

	rodar := func(operacao, nomePrompt, rotulo string, extras map[string]string, schema string, somenteLeitura bool) (*ResultadoRun, string, error) {
		cfgRun, err := carregarConfig(raiz)
		if err != nil {
			return nil, "", err
		}
		cfg = cfgRun
		vars["PLANO"] = cfgRun.Plano
		tpl, err := carregarPrompt(raiz, nomePrompt)
		if err != nil {
			return nil, "", err
		}
		valores := map[string]string{}
		for k, v := range vars {
			valores[k] = v
		}
		for k, v := range extras {
			valores[k] = v
		}
		motorPrimario := motorParaOperacao(cfgRun, operacao)
		modelo := strings.TrimSpace(f.Modelo)
		if modelo == "" {
			modelo = modeloParaMotor(cfgRun, motorPrimario)
		}
		res, motorUsado, err := rodarComFallback(raiz, operacao, motorPrimario, OpcoesRun{
			Raiz: raiz, Prompt: renderPrompt(tpl, valores), Modelo: modelo,
			Esforco: esforcoParaMotor(cfgRun, motorPrimario),
			AddDirs: cfgRun.AddDirs, BudgetUSD: cfgRun.MaxBudgetUSD, TimeoutMin: cfgRun.TimeoutMin,
			Schema: schema, ProibirCommit: true, SomenteLeitura: somenteLeitura,
			RotuloLog: fmt.Sprintf("fase-%s-%s", f.Fase, rotulo),
			Ctx:       ctx, PausaCh: pausaCh, OnEspera: onEspera,
		}, estadoFallback)
		if res != nil {
			custo += res.CustoUSD
			if cfgRun.MaxBudgetUSD > 0 {
				if m, selErr := selecionarMotor(motorUsado); selErr == nil && !m.Capacidades().BudgetNativo && custo > cfgRun.MaxBudgetUSD {
					return res, motorUsado, fmt.Errorf("budget soft excedido para %s: custo acumulado ~US$ %.2f > limite US$ %.2f", motorUsado, custo, cfgRun.MaxBudgetUSD)
				}
			}
		}
		return res, motorUsado, err
	}

	// corrige a entrega (contexto limpo) a partir de um motivo (gates/revisor)
	corrigir := func(rotulo, motivo string) error {
		notif.enviarEvento("correcao_iniciada", fmt.Sprintf("Praxis: correcao iniciada na Fase %s", f.Fase), motivo)
		res, _, err := rodar("corrigir", "corretor.md", rotulo, map[string]string{"MOTIVO": motivo}, "", false)
		if err != nil {
			return err
		}
		if res.IsError {
			return fmt.Errorf("corretor terminou com erro (%s) — log: %s", res.Subtipo, res.LogPath)
		}
		return nil
	}

	// roda os gates; a cada vermelho, um ciclo de corretor, ate o limite
	gatesVerdes := func() error {
		for tent := 0; ; tent++ {
			// recarrega a config do disco antes de cada rodada: o corretor pode
			// ter ajustado um gate mal configurado (comando incompativel com o
			// shell, dir errado) e essa correcao precisa valer ja agora — a cfg
			// em memoria foi lida uma unica vez no inicio da execucao.
			if fresh, err := carregarConfig(raiz); err == nil {
				cfg.Gates = fresh.Gates
				cfg.GatesExtra = fresh.GatesExtra
			} else {
				fmt.Printf("  AVISO: nao consegui recarregar %s (%v) — mantendo a config anterior\n", nomeConfig, err)
			}
			fmt.Println("▶ gates deterministicos")
			rg, err := rodarGates(raiz, cfg, f)
			if err != nil {
				return err
			}
			if rg.Ok {
				return nil
			}
			// falha de ambiente/config (comando nao encontrado, shell incompativel):
			// o corretor nao consegue consertar isso a tempo — para e pede
			// intervencao humana, sem gastar (nem faturar) tentativas de correcao.
			if rg.Ambiente {
				return fmt.Errorf("gate [%s] nao pode ser executado — parece problema de ambiente/config (binario ausente, PATH ou sintaxe de comando incompativel com o shell), nao do codigo. Ajuste o comando em %s (ou o PATH/dependencias) e rode de novo.\nFinal da saida:\n%s\nlog: %s",
					rg.Gate, nomeConfig, rg.Erro, rg.LogPath)
			}
			if tent >= cfg.MaxCorrecoes {
				return fmt.Errorf("gates continuam vermelhos apos %d correcao(oes) — gate [%s], log: %s", tent, rg.Gate, rg.LogPath)
			}
			notif.enviarEvento("gates_falharam", fmt.Sprintf("Praxis: gates falharam na Fase %s", f.Fase),
				fmt.Sprintf("Gate: %s\nLog: %s", rg.Gate, rg.LogPath))
			fmt.Printf("▶ corretor %d/%d — gate [%s] falhou\n", tent+1, cfg.MaxCorrecoes, rg.Gate)
			motivo := fmt.Sprintf("Os comandos de verificacao (gates) falharam.\nGate: %s\nFinal da saida:\n```\n%s\n```", rg.Gate, rg.Erro)
			if err := corrigir(fmt.Sprintf("corretor%d", tent+1), motivo); err != nil {
				return err
			}
		}
	}

	// 1) executor — a fase em si, contexto limpo
	fmt.Println("▶ executor (contexto limpo)")
	resExec, motorExec, err := rodar("executar", "executor.md", "executor", nil, "", false)
	if err != nil {
		if pausou(err) {
			return pausar()
		}
		return falha("executor: %v", err)
	}
	motorExecutor = motorExec
	if resExec.IsError {
		return falha("executor terminou com erro (%s) — log: %s", resExec.Subtipo, resExec.LogPath)
	}

	// 2) gates + correcoes
	if err := gatesVerdes(); err != nil {
		if pausou(err) {
			return pausar()
		}
		return falha("%v", err)
	}

	// 3) revisor (contexto limpo, so leitura) + no maximo MaxCiclosRevisao correcoes
	for ciclo := 0; ; ciclo++ {
		fmt.Println("▶ revisor (contexto limpo, so leitura)")
		resRev, _, err := rodar("revisar", "revisor.md", fmt.Sprintf("revisor%d", ciclo+1), nil, schemaVeredito, true)
		if err != nil {
			if pausou(err) {
				return pausar()
			}
			return falha("revisor: %v", err)
		}
		var ver Veredito
		if resRev.IsError || decodificarEstruturado(resRev, &ver) != nil {
			return falha("revisor nao devolveu veredito valido — log: %s", resRev.LogPath)
		}
		if ver.Veredito == "APROVADO" {
			fmt.Println("  revisor: APROVADO")
			break
		}
		fmt.Printf("  revisor: REPROVADO\n%s\n", indentar("- "+strings.Join(ver.Problemas, "\n- "), "    "))
		notif.enviarEvento("revisor_reprovou", fmt.Sprintf("Praxis: revisor reprovou a Fase %s", f.Fase), strings.Join(ver.Problemas, "\n"))
		if ciclo >= cfg.MaxCiclosRevisao {
			return falha("revisor reprovou apos %d ciclo(s) de correcao: %s", ciclo, strings.Join(ver.Problemas, " | "))
		}
		motivo := "O revisor de codigo REPROVOU a entrega com os problemas:\n- " + strings.Join(ver.Problemas, "\n- ")
		if err := corrigir(fmt.Sprintf("corretor-rev%d", ciclo+1), motivo); err != nil {
			if pausou(err) {
				return pausar()
			}
			return falha("%v", err)
		}
		if err := gatesVerdes(); err != nil {
			if pausou(err) {
				return pausar()
			}
			return falha("%v", err)
		}
	}

	// 4) guarda do plano: a fase precisa ter atualizado o arquivo do plano
	if nomes, err := gitArquivosMudados(raiz); err == nil && !contemArquivo(nomes, cfg.Plano) {
		fmt.Println("▶ plano nao foi atualizado — pedindo atualizacao")
		prompt := fmt.Sprintf("A Fase %s — %s acabou de ser implementada nesta arvore de trabalho, mas o arquivo do plano (`%s`) nao foi atualizado. Atualize-o AGORA: marque os checkboxes da fase, ajuste o dashboard (se existir) e adicione uma entrada no Registro de Andamento com data, o que foi feito, decisoes/desvios e achados uteis para as proximas fases. Nao altere codigo e nao faca commit.",
			f.Fase, f.Titulo, cfg.Plano)
		cfgRun, cfgErr := carregarConfig(raiz)
		if cfgErr == nil {
			cfg = cfgRun
		}
		motorPrimario := motorParaOperacao(cfg, "executar")
		modeloPlano := strings.TrimSpace(f.Modelo)
		if modeloPlano == "" {
			modeloPlano = modeloParaMotor(cfg, motorPrimario)
		}
		res, motorPlano, err := rodarComFallback(raiz, "executar", motorPrimario, OpcoesRun{
			Raiz: raiz, Prompt: prompt, Modelo: modeloPlano, Esforco: esforcoParaMotor(cfg, motorPrimario), AddDirs: cfg.AddDirs,
			BudgetUSD: cfg.MaxBudgetUSD, TimeoutMin: cfg.TimeoutMin,
			ProibirCommit: true, RotuloLog: fmt.Sprintf("fase-%s-plano", f.Fase),
			Ctx: ctx, PausaCh: pausaCh, OnEspera: onEspera,
		}, estadoFallback)
		if pausou(err) {
			return pausar()
		}
		if err == nil && res != nil {
			custo += res.CustoUSD
			if cfg.MaxBudgetUSD > 0 {
				if m, selErr := selecionarMotor(motorPlano); selErr == nil && !m.Capacidades().BudgetNativo && custo > cfg.MaxBudgetUSD {
					return falha("budget soft excedido para %s: custo acumulado ~US$ %.2f > limite US$ %.2f", motorPlano, custo, cfg.MaxBudgetUSD)
				}
			}
		}
	}

	// 5) commit local por repositorio tocado (sem push)
	trailer := coAuthorTrailer(motorExecutor)
	if trailer != "" {
		trailer = "\n\n" + trailer
	}
	msg := fmt.Sprintf("Fase %s: %s [praxis]\n\n%s%s\n",
		f.Fase, f.Titulo, primeirasLinhas(strings.TrimSpace(resExec.Resultado), 15), trailer)
	for _, r := range repos {
		limpo, err := gitLimpo(r)
		if err != nil {
			return falha("%v", err)
		}
		if limpo {
			continue
		}
		fmt.Printf("▶ commit em %s\n", r)
		if err := gitCommit(r, msg); err != nil {
			return falha("%v", err)
		}
	}

	// 6) fecha a fase
	f.Status = StConcluida
	f.CustoUSD += custo
	f.ConcluidoEm = agoraLegivel()
	f.Observacao = fmt.Sprintf("ok — custo US$ %.2f", custo)
	fmt.Printf("✔ Fase %s concluida (custo US$ %.2f)\n", f.Fase, custo)
	notif.enviarEvento("fase_concluida", fmt.Sprintf("Praxis: Fase %s concluida", f.Fase), resumoAndamento(todas))

	// 7) marco notificavel: avisa remotamente que a fase concluiu, com o
	// panorama do andamento (quantas concluidas, pendentes, falhas...).
	if f.Notificar {
		notif.enviarEvento("marco_concluido", fmt.Sprintf("Praxis: Fase %s — %s concluida", f.Fase, f.Titulo), resumoAndamento(todas))
	}
	return nil
}

// contemArquivo compara caminhos com barras normalizadas.
func contemArquivo(nomes []string, alvo string) bool {
	alvo = filepath.ToSlash(alvo)
	for _, n := range nomes {
		if filepath.ToSlash(n) == alvo {
			return true
		}
	}
	return false
}
