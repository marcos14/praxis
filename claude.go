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
)

// motorClaude executa o Claude Code headless (`claude -p`) com stream-json.
// Suporta nativamente schema (--json-schema), budget (--max-budget-usd) e
// custo em USD (total_cost_usd no evento result).
type motorClaude struct{}

func (motorClaude) Nome() string { return "claude" }

func (motorClaude) Capacidades() Capacidades {
	return Capacidades{SchemaNativo: true, BudgetNativo: true, CustoUSDNativo: true}
}

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

// Rodar executa `claude -p` com stream-json: mostra o progresso ao vivo no
// console, grava cada evento num .jsonl e devolve o resultado final (inclusive
// quando o run termina com is_error=true — quem decide e o chamador).
func (motorClaude) Rodar(op OpcoesRun) (*ResultadoRun, error) {
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
	if op.Schema != "" {
		args = append(args, "--json-schema", op.Schema)
	}
	if proib := proibidosClaude(op); len(proib) > 0 {
		args = append(args, "--disallowedTools")
		args = append(args, proib...)
	}

	logFile, logPath, err := abrirLog(op.Raiz, op.RotuloLog, "jsonl")
	if err != nil {
		return nil, err
	}
	defer logFile.Close()

	ctx, cancel := contextoTimeout(op.TimeoutMin)
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

	var res *ResultadoRun
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024) // resultados de tool podem ser enormes
	for sc.Scan() {
		linha := sc.Bytes()
		logFile.Write(linha)
		logFile.Write([]byte{'\n'})
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
						fmt.Println(indentar(t, "  │ "))
					}
				case "tool_use":
					fmt.Printf("  → %s\n", c.Name)
				}
			}
		case "result":
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
		return nil, fmt.Errorf("claude excedeu o timeout — log: %s", logPath)
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
