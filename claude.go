package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// motorClaude executa o Claude Code headless (`claude -p`) com stream-json.
// Suporta nativamente schema, budget e custo em USD.
type motorClaude struct{}

func (motorClaude) Nome() string { return "claude" }

func (motorClaude) Capacidades() Capacidades {
	return Capacidades{SchemaNativo: true, BudgetNativo: true, CustoUSDNativo: true}
}

// esperaResetFranquia e quanto o orquestrador dorme quando a franquia de
// tokens acaba, antes de tentar continuar a mesma execucao. E variavel (e nao
// const) para os testes poderem encurtar a espera.
var esperaResetFranquia = 15 * time.Minute

// errPausaSolicitada e o sentinela devolvido quando o usuario pede, no
// terminal, para pausar a execucao (Enter durante a espera do reset da
// franquia). Nao e uma falha: a fase fica retomavel de onde parou.
var errPausaSolicitada = errors.New("pausa solicitada pelo usuario")

// eventoStreamClaude cobre o subconjunto dos eventos stream-json que interessa.
type eventoStreamClaude struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	NumTurns         int             `json:"num_turns"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	Message          struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
		} `json:"content"`
	} `json:"message"`
}

type eventoStream = eventoStreamClaude

// rodarClaude preserva a API antiga enquanto o pipeline ainda nao foi ligado
// por operacao. Ele executa o motor Claude e, se a franquia acabar, espera o
// reset e tenta a mesma execucao novamente.
func rodarClaude(op OpcoesClaude) (*ResultadoClaude, error) {
	primeiraEspera := true
	for {
		res, err := motorClaude{}.Rodar(op)
		if err != nil || res == nil || !res.LimiteSessao {
			return res, err
		}
		if primeiraEspera && op.OnEspera != nil {
			op.OnEspera(strings.TrimSpace(res.DetalheLimite))
		}
		primeiraEspera = false
		if err := esperarResetFranquia(op, res); err != nil {
			return nil, err
		}
	}
}

// esperarResetFranquia dorme esperaResetFranquia respeitando o cancelamento
// (Ctrl+C) do contexto pai. Enquanto espera, aceita Enter no terminal para
// pausar e continuar depois.
func esperarResetFranquia(op OpcoesRun, res *ResultadoRun) error {
	ctx := op.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if op.PausaCh != nil {
		select {
		case <-op.PausaCh:
		default:
		}
	}
	detalhe := strings.TrimSpace(res.DetalheLimite)
	if detalhe == "" {
		detalhe = "franquia de tokens esgotada (limite de sessao)"
	}
	retomar := time.Now().Add(esperaResetFranquia)
	fmt.Printf("\n%s\n", detalhe)
	fmt.Printf("   Franquia de tokens esgotada; durmo %v e retomo a mesma fase as %s.\n",
		esperaResetFranquia, retomar.Format("15:04"))
	if op.PausaCh != nil {
		fmt.Println("   [Enter] para PAUSAR agora e continuar depois; Ctrl+C aborta.")
	}
	t := time.NewTimer(esperaResetFranquia)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("execucao interrompida durante a espera do reset da franquia")
	case <-op.PausaCh:
		fmt.Println("pausa solicitada; encerrando para continuar depois.")
		return errPausaSolicitada
	case <-t.C:
		fmt.Println("retomando apos a espera da franquia...")
		return nil
	}
}

// limiteSessaoAtingido reconhece a mensagem que o claude imprime quando a
// franquia de tokens acaba.
func limiteSessaoAtingido(texto string) bool {
	t := strings.ToLower(texto)
	return (strings.Contains(t, "session limit") || strings.Contains(t, "usage limit")) &&
		strings.Contains(t, "reset")
}

// linhaLimite extrai a primeira linha do texto que menciona o limite/reset.
func linhaLimite(texto string) string {
	for _, l := range strings.Split(texto, "\n") {
		if limiteSessaoAtingido(l) {
			return strings.TrimSpace(l)
		}
	}
	return strings.TrimSpace(texto)
}

// Rodar faz uma unica execucao de `claude -p` com stream-json: mostra o
// progresso ao vivo no console, grava cada evento em um .jsonl e devolve o
// resultado final.
func (motorClaude) Rodar(op OpcoesRun) (*ResultadoRun, error) {
	args := []string{"-p", "--dangerously-skip-permissions", "--output-format", "stream-json", "--verbose"}
	if op.Modelo != "" {
		args = append(args, "--model", op.Modelo)
	}
	if op.Esforco != "" {
		args = append(args, "--effort", op.Esforco)
	}
	if op.BudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", op.BudgetUSD))
	}
	for _, d := range op.AddDirs {
		args = append(args, "--add-dir", resolverDir(op.Raiz, d))
	}
	if op.Schema != "" {
		args = append(args, "--json-schema", op.Schema)
	}
	if proibidos := proibidosClaude(op); len(proibidos) > 0 {
		args = append(args, "--disallowedTools")
		args = append(args, proibidos...)
	}

	logFile, logPath, err := abrirLog(op.Raiz, op.RotuloLog, "jsonl")
	if err != nil {
		return nil, err
	}
	defer logFile.Close()

	ctx, cancel, timeout := contextoTimeout(op.Ctx, op.TimeoutMin)
	defer cancel()

	fmt.Println("  AVISO: Claude em modo BYPASS (--dangerously-skip-permissions): acesso total ao sistema, sem prompts de permissao. Use apenas em ambiente controlado.")
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = op.Raiz
	cmd.Stdin = strings.NewReader(op.Prompt)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("nao consegui executar `claude` (esta no PATH e logado?): %w", err)
	}

	var res *ResultadoRun
	var textoAcc strings.Builder
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		linha := sc.Bytes()
		_, _ = logFile.Write(linha)
		_, _ = logFile.Write([]byte{'\n'})
		var ev eventoStreamClaude
		if json.Unmarshal(linha, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "assistant":
			for _, c := range ev.Message.Content {
				switch c.Type {
				case "text":
					if t := strings.TrimSpace(c.Text); t != "" {
						textoAcc.WriteString(t + "\n")
						fmt.Println(indentar(t, "  | "))
					}
				case "tool_use":
					fmt.Printf("  -> %s\n", c.Name)
				}
			}
		case "result":
			textoAcc.WriteString(ev.Result + "\n")
			res = &ResultadoRun{
				IsError:     ev.IsError,
				Subtipo:     ev.Subtype,
				Resultado:   ev.Result,
				Estruturado: ev.StructuredOutput,
				CustoUSD:    ev.TotalCostUSD,
				NumTurns:    ev.NumTurns,
				LogPath:     logPath,
			}
		}
	}
	errScan := sc.Err()
	errWait := cmd.Wait()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("claude excedeu o timeout de %v; log: %s", timeout, logPath)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, fmt.Errorf("execucao interrompida; log: %s", logPath)
	}
	if texto := strings.TrimSpace(stderr.String()); limiteSessaoAtingido(texto) {
		return &ResultadoRun{
			IsError:       true,
			Subtipo:       "limite de sessao/uso",
			Resultado:     texto,
			LogPath:       logPath,
			LimiteSessao:  true,
			DetalheLimite: linhaLimite(texto),
		}, nil
	}
	if res == nil {
		return nil, fmt.Errorf("claude terminou sem evento de resultado (scan: %v, exit: %v)\nstderr: %s\nlog: %s",
			errScan, errWait, ultimasLinhas(stderr.String(), 15), logPath)
	}
	if res.CustoUSD > 0 {
		fmt.Printf("  (run: US$ %.2f, %d turnos)\n", res.CustoUSD, res.NumTurns)
	}
	if texto := textoAcc.String(); limiteSessaoAtingido(texto) {
		res.LimiteSessao = true
		res.DetalheLimite = linhaLimite(texto)
	}
	if texto := strings.TrimSpace(stderr.String()); limiteSessaoAtingido(texto) {
		res.LimiteSessao = true
		res.DetalheLimite = linhaLimite(texto)
		if res.Resultado == "" {
			res.Resultado = texto
		}
	}
	return res, nil
}

// proibidosClaude traduz as intencoes genericas nas ferramentas que o Claude
// deve recusar: o commit e sempre do orquestrador; o revisor nao edita nada.
func proibidosClaude(op OpcoesRun) []string {
	var p []string
	if op.ProibirCommit || op.SomenteLeitura {
		p = append(p, "Bash(git commit*)", "Bash(git push*)")
	}
	if op.SomenteLeitura {
		p = append(p, "Edit", "Write", "NotebookEdit")
	}
	return p
}
