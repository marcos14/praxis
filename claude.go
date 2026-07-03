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
	JSONSchema string          // se definido, força saida estruturada (--json-schema)
	Disallowed []string        // ferramentas proibidas (--disallowedTools)
	RotuloLog  string          // prefixo do arquivo .jsonl em automacao/logs/
	Ctx        context.Context // contexto pai (cancelamento via Ctrl+C); nil = background
}

type ResultadoClaude struct {
	IsError     bool
	Subtipo     string
	Resultado   string          // texto final do run
	Estruturado json.RawMessage // saida do --json-schema, quando presente
	CustoUSD    float64
	NumTurns    int
	LogPath     string
}

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

// rodarClaude executa `claude -p` com stream-json: mostra o progresso ao
// vivo no console, grava cada evento em um .jsonl e devolve o resultado
// final (inclusive quando o run termina com is_error=true — quem decide o
// que fazer e o chamador).
func rodarClaude(op OpcoesClaude) (*ResultadoClaude, error) {
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
						fmt.Println(indentar(t, "  │ "))
					}
				case "tool_use":
					fmt.Printf("  → %s\n", c.Name)
				}
			}
		case "result":
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
