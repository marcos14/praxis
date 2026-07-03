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
	"strings"
)

// motorCodex executa o OpenAI Codex CLI headless (`codex exec --json`).
// Nativo: schema (--output-schema) e read-only (--sandbox read-only).
// Fallback: budget e custo em USD nao existem nativamente — o custo e estimado
// a partir dos tokens (custoEstimado) e o budget vira um teto por fase (soft),
// verificado pelo orquestrador entre os runs.
type motorCodex struct{}

func (motorCodex) Nome() string { return "codex" }

func (motorCodex) Capacidades() Capacidades {
	return Capacidades{SchemaNativo: true, BudgetNativo: false, CustoUSDNativo: false}
}

// eventoCodex cobre o subconjunto do fluxo JSONL de `codex exec --json`.
type eventoCodex struct {
	Type string `json:"type"`
	Item struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Command string `json:"command"`
	} `json:"item"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error json.RawMessage `json:"error"`
}

func (motorCodex) Rodar(op OpcoesRun) (*ResultadoRun, error) {
	args := []string{"exec", "--json", "--skip-git-repo-check", "-C", op.Raiz, "--color", "never"}
	if op.Modelo != "" {
		args = append(args, "--model", op.Modelo)
	}
	sandbox := "workspace-write"
	if op.SomenteLeitura {
		sandbox = "read-only"
	}
	args = append(args, "--sandbox", sandbox)
	for _, d := range op.AddDirs {
		args = append(args, "--add-dir", resolverDir(op.Raiz, d))
	}

	// saida estruturada nativa: schema num arquivo temporario, resposta final
	// escrita noutro arquivo (-o) que lemos depois.
	var outFile string
	if op.Schema != "" {
		esquema, err := schemaStrictOpenAI(op.Schema)
		if err != nil {
			return nil, fmt.Errorf("schema invalido para o codex: %w", err)
		}
		sf, err := os.CreateTemp("", "praxis-schema-*.json")
		if err != nil {
			return nil, err
		}
		if _, err := sf.WriteString(esquema); err != nil {
			sf.Close()
			return nil, err
		}
		sf.Close()
		defer os.Remove(sf.Name())

		of, err := os.CreateTemp("", "praxis-out-*.json")
		if err != nil {
			return nil, err
		}
		outFile = of.Name()
		of.Close()
		defer os.Remove(outFile)

		args = append(args, "--output-schema", sf.Name(), "-o", outFile)
	}
	args = append(args, "-") // le o prompt do stdin

	logFile, logPath, err := abrirLog(op.Raiz, op.RotuloLog, "jsonl")
	if err != nil {
		return nil, err
	}
	defer logFile.Close()

	ctx, cancel := contextoTimeout(op.TimeoutMin)
	defer cancel()

	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.Dir = op.Raiz
	cmd.Stdin = strings.NewReader(op.Prompt)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("nao consegui executar `codex` (esta no PATH e logado?): %w", err)
	}

	var ultimoTexto, subtipo string
	var tokIn, tokOut, numTurns int
	isError := false
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		linha := sc.Bytes()
		logFile.Write(linha)
		logFile.Write([]byte{'\n'})
		var ev eventoCodex
		if json.Unmarshal(linha, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "item.started":
			if ev.Item.Type == "command_execution" && ev.Item.Command != "" {
				fmt.Printf("  → %s\n", primeirasLinhas(ev.Item.Command, 1))
			}
		case "item.completed":
			if ev.Item.Type == "agent_message" {
				if t := strings.TrimSpace(ev.Item.Text); t != "" {
					fmt.Println(indentar(t, "  │ "))
					ultimoTexto = ev.Item.Text
				}
			}
		case "turn.completed":
			numTurns++
			tokIn += ev.Usage.InputTokens
			tokOut += ev.Usage.OutputTokens
		case "turn.failed", "error":
			isError = true
			if len(ev.Error) > 0 {
				subtipo = primeirasLinhas(string(ev.Error), 1)
			}
		}
	}
	errScan := sc.Err()
	errWait := cmd.Wait()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("codex excedeu o timeout — log: %s", logPath)
	}
	if errWait != nil {
		isError = true
		if subtipo == "" {
			subtipo = ultimasLinhas(stderr.String(), 3)
		}
	}

	res := &ResultadoRun{
		IsError:   isError,
		Subtipo:   subtipo,
		Resultado: ultimoTexto,
		CustoUSD:  custoEstimado(op.Modelo, tokIn, tokOut),
		NumTurns:  numTurns,
		TokensIn:  tokIn,
		TokensOut: tokOut,
		LogPath:   logPath,
	}
	if outFile != "" {
		if b, err := os.ReadFile(outFile); err == nil && len(bytes.TrimSpace(b)) > 0 {
			res.Estruturado = json.RawMessage(bytes.TrimSpace(b))
			if res.Resultado == "" {
				res.Resultado = string(bytes.TrimSpace(b))
			}
		}
	}
	if res.Resultado == "" && errScan != nil {
		return nil, fmt.Errorf("codex terminou sem saida util (scan: %v, exit: %v) — log: %s", errScan, errWait, logPath)
	}
	if res.CustoUSD > 0 {
		fmt.Printf("  (run: ~US$ %.2f estimado, %d/%d tokens)\n", res.CustoUSD, tokIn, tokOut)
	} else if tokIn+tokOut > 0 {
		fmt.Printf("  (run: %d/%d tokens; custo em USD indisponivel p/ o modelo %q)\n", tokIn, tokOut, op.Modelo)
	}
	return res, nil
}

// schemaStrictOpenAI adapta um JSON Schema para o modo "strict" que o Codex
// (Responses API da OpenAI) exige: todo objeto precisa de
// "additionalProperties": false e listar TODAS as suas propriedades em
// "required". Campos antes opcionais passam a ser sempre emitidos pelo modelo
// (com valor vazio quando não se aplicam), o que é inócuo para os schemas do
// Praxis. Recebe e devolve o schema serializado como JSON.
func schemaStrictOpenAI(esquema string) (string, error) {
	var raiz any
	if err := json.Unmarshal([]byte(esquema), &raiz); err != nil {
		return "", err
	}
	strictNo(raiz)
	b, err := json.Marshal(raiz)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// strictNo percorre o schema recursivamente aplicando as regras do modo strict
// a cada nó do tipo objeto.
func strictNo(n any) {
	m, ok := n.(map[string]any)
	if !ok {
		if arr, ok := n.([]any); ok {
			for _, item := range arr {
				strictNo(item)
			}
		}
		return
	}
	if tipo, _ := m["type"].(string); tipo == "object" {
		if props, ok := m["properties"].(map[string]any); ok {
			m["additionalProperties"] = false
			chaves := make([]any, 0, len(props))
			for k := range props {
				chaves = append(chaves, k)
			}
			m["required"] = chaves
		}
	}
	for _, v := range m {
		strictNo(v)
	}
}
