package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// OpcoesClaude descreve uma execucao headless do Claude Code (`claude -p`).
// Cada execucao e um contexto limpo: todo o conhecimento necessario deve
// estar no prompt e nos arquivos do projeto (plano, CLAUDE.md...).
type OpcoesClaude struct {
	Raiz       string
	Prompt     string
	Modelo     string
	AddDirs    []string
	BudgetUSD  float64
	TimeoutMin int
	JSONSchema string               // se definido, força saida estruturada (--json-schema)
	Disallowed []string             // ferramentas proibidas (--disallowedTools)
	RotuloLog  string               // prefixo do arquivo .jsonl em automacao/logs/
	Ctx        context.Context      // contexto pai (cancelamento via Ctrl+C); nil = background
	PausaCh    <-chan struct{}      // sinaliza pausa (Enter) durante a espera do reset da franquia
	OnEspera   func(detalhe string) // chamado uma vez ao comecar a esperar o reset da franquia
}

type ResultadoClaude struct {
	IsError     bool
	Subtipo     string
	Resultado   string          // texto final do run
	Estruturado json.RawMessage // saida do --json-schema, quando presente
	CustoUSD    float64
	NumTurns    int
	LogPath     string

	LimiteSessao  bool   // a franquia de tokens/limite de sessao foi atingida
	DetalheLimite string // linha do claude com o horario de reset, quando houver
}

// esperaResetFranquia e quanto o orquestrador dorme quando a franquia de
// tokens acaba, antes de tentar continuar a mesma execucao. E variavel (e nao
// const) para os testes poderem encurtar a espera.
var esperaResetFranquia = 15 * time.Minute

// errPausaSolicitada e o sentinela devolvido quando o usuario pede, no
// terminal, para pausar a execucao (Enter durante a espera do reset da
// franquia). Nao e uma falha: a fase fica retomavel de onde parou.
var errPausaSolicitada = errors.New("pausa solicitada pelo usuario")

// eventoStream cobre o subconjunto dos eventos stream-json que interessa.
type eventoStream struct {
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

// rodarClaude executa `claude -p` com stream-json e trata a franquia de
// tokens: quando o run termina porque o limite de sessao foi atingido, dorme
// esperaResetFranquia (15 min) e tenta a MESMA execucao de novo, ate passar o
// horario de reset (ou o usuario abortar com Ctrl+C). Nos demais casos devolve
// o resultado final — inclusive com is_error=true — e quem decide o que fazer
// e o chamador.
func rodarClaude(op OpcoesClaude) (*ResultadoClaude, error) {
	primeiraEspera := true
	for {
		res, err := rodarClaudeUmaVez(op)
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
// (Ctrl+C) do contexto pai. Enquanto espera, aceita um Enter no terminal
// (PausaCh) para pausar e continuar depois — util para trocar de conta e
// relogar. Devolve errPausaSolicitada se o usuario pediu pausa, ou outro erro
// se a espera foi interrompida (Ctrl+C).
func esperarResetFranquia(op OpcoesClaude, res *ResultadoClaude) error {
	ctx := op.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	// descarta um eventual sinal de pausa digitado fora de uma espera
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
	fmt.Printf("\n⏳ %s\n", detalhe)
	fmt.Printf("   Franquia de tokens esgotada — durmo %v e retomo a mesma fase às %s.\n",
		esperaResetFranquia, retomar.Format("15:04"))
	if op.PausaCh != nil {
		fmt.Println("   [Enter] para PAUSAR agora e continuar depois (ex.: trocar de conta e relogar); Ctrl+C aborta.")
	}
	t := time.NewTimer(esperaResetFranquia)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("execucao interrompida durante a espera do reset da franquia")
	case <-op.PausaCh:
		fmt.Println("⏸️  pausa solicitada — encerrando para você continuar depois.")
		return errPausaSolicitada
	case <-t.C:
		fmt.Println("⏳ retomando apos a espera da franquia...")
		return nil
	}
}

// limiteSessaoAtingido reconhece a mensagem que o claude imprime quando a
// franquia de tokens acaba (ex.: "You've hit your session limit · resets
// 11:40pm" ou "Claude usage limit reached ... will reset at ...").
func limiteSessaoAtingido(texto string) bool {
	t := strings.ToLower(texto)
	return (strings.Contains(t, "session limit") || strings.Contains(t, "usage limit")) &&
		strings.Contains(t, "reset")
}

// linhaLimite extrai a primeira linha do texto que menciona o limite/reset,
// para exibir ao usuario o horario de retomada informado pelo claude.
func linhaLimite(texto string) string {
	for _, l := range strings.Split(texto, "\n") {
		if limiteSessaoAtingido(l) {
			return strings.TrimSpace(l)
		}
	}
	return strings.TrimSpace(texto)
}

// rodarClaudeUmaVez faz uma unica execucao de `claude -p` com stream-json:
// mostra o progresso ao vivo no console, grava cada evento em um .jsonl e
// devolve o resultado final.
func rodarClaudeUmaVez(op OpcoesClaude) (*ResultadoClaude, error) {
	args := []string{"-p", "--dangerously-skip-permissions", "--output-format", "stream-json", "--verbose"}
	if op.Modelo != "" {
		args = append(args, "--model", op.Modelo)
	}
	if op.BudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", op.BudgetUSD))
	}
	for _, d := range op.AddDirs {
		args = append(args, "--add-dir", resolverDir(op.Raiz, d))
	}
	if op.JSONSchema != "" {
		args = append(args, "--json-schema", op.JSONSchema)
	}
	if len(op.Disallowed) > 0 {
		args = append(args, "--disallowedTools")
		args = append(args, op.Disallowed...)
	}

	if err := os.MkdirAll(dirLogs(op.Raiz), 0o755); err != nil {
		return nil, err
	}
	logPath := filepath.Join(dirLogs(op.Raiz), fmt.Sprintf("%s-%s.jsonl", op.RotuloLog, agoraTS()))
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()

	timeout := time.Duration(op.TimeoutMin) * time.Minute
	if timeout <= 0 {
		timeout = 2 * time.Hour
	}
	pai := op.Ctx
	if pai == nil {
		pai = context.Background()
	}
	ctx, cancel := context.WithTimeout(pai, timeout)
	defer cancel()

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

	var res *ResultadoClaude
	var textoAcc strings.Builder // todo o texto do assistente + resultado, p/ detectar a franquia
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024) // resultados de tool podem ser enormes
	for sc.Scan() {
		linha := sc.Bytes()
		logFile.Write(linha)
		logFile.Write([]byte{'\n'})
		var ev eventoStream
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
						fmt.Println(indentar(t, "  │ "))
					}
				case "tool_use":
					fmt.Printf("  → %s\n", c.Name)
				}
			}
		case "result":
			textoAcc.WriteString(ev.Result + "\n")
			res = &ResultadoClaude{
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
		return nil, fmt.Errorf("claude excedeu o timeout de %v — log: %s", timeout, logPath)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, fmt.Errorf("execucao interrompida — log: %s", logPath)
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
	return res, nil
}

// decodificarEstruturado le a saida de um run com --json-schema: prefere o
// campo structured_output; na falta, extrai o JSON do texto final.
func decodificarEstruturado(res *ResultadoClaude, v any) error {
	if len(res.Estruturado) > 0 && string(res.Estruturado) != "null" {
		if err := json.Unmarshal(res.Estruturado, v); err == nil {
			return nil
		}
	}
	bruto := extrairJSON(res.Resultado)
	if bruto == "" {
		return fmt.Errorf("resposta sem JSON reconhecivel")
	}
	return json.Unmarshal([]byte(bruto), v)
}

// extrairJSON pega o primeiro objeto JSON de um texto (tolerante a cercas de
// markdown e a prosa em volta).
func extrairJSON(s string) string {
	ini := strings.Index(s, "{")
	fim := strings.LastIndex(s, "}")
	if ini < 0 || fim <= ini {
		return ""
	}
	return s[ini : fim+1]
}
