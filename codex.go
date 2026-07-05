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
	"sort"
	"strings"
)

// motorCodex executa o OpenAI Codex CLI headless (`codex exec --json`).
// Budget e custo em USD nao sao nativos; o custo e estimado a partir dos
// tokens e o teto fica para o orquestrador validar entre runs.
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
	if op.Esforco != "" {
		args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", op.Esforco))
	}
	if op.SomenteLeitura {
		args = append(args, "--sandbox", "read-only")
	} else {
		fmt.Println("  AVISO: Codex em modo BYPASS (--dangerously-bypass-approvals-and-sandbox): acesso total ao sistema, sem sandbox nem aprovacoes. Use apenas em ambiente controlado.")
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	for _, d := range op.AddDirs {
		args = append(args, "--add-dir", resolverDir(op.Raiz, d))
	}

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
			_ = sf.Close()
			return nil, err
		}
		_ = sf.Close()
		defer os.Remove(sf.Name())

		of, err := os.CreateTemp("", "praxis-out-*.json")
		if err != nil {
			return nil, err
		}
		outFile = of.Name()
		_ = of.Close()
		defer os.Remove(outFile)

		args = append(args, "--output-schema", sf.Name(), "-o", outFile)
	}
	args = append(args, "-")

	logFile, logPath, err := abrirLog(op.Raiz, op.RotuloLog, "jsonl")
	if err != nil {
		return nil, err
	}
	defer logFile.Close()

	ctx, cancel, timeout := contextoTimeout(op.Ctx, op.TimeoutMin)
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

	var ultimoTexto, subtipo, detalheLimite string
	var tokIn, tokOut, numTurns int
	isError := false
	limite := false
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		linha := sc.Bytes()
		_, _ = logFile.Write(linha)
		_, _ = logFile.Write([]byte{'\n'})
		var ev eventoCodex
		if json.Unmarshal(linha, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "item.started":
			if ev.Item.Type == "command_execution" && ev.Item.Command != "" {
				fmt.Printf("  -> %s\n", primeirasLinhas(ev.Item.Command, 1))
			}
		case "item.completed":
			if ev.Item.Type == "agent_message" {
				if t := strings.TrimSpace(ev.Item.Text); t != "" {
					fmt.Println(indentar(t, "  | "))
					ultimoTexto = ev.Item.Text
				}
			}
		case "turn.completed":
			numTurns++
			tokIn += ev.Usage.InputTokens
			tokOut += ev.Usage.OutputTokens
		case "turn.failed", "error":
			isError = true
			msg := textoErroCodex(ev.Error)
			if msg != "" {
				subtipo = primeirasLinhas(msg, 1)
			}
			if limiteCodexAtingido(msg) {
				limite = true
				detalheLimite = linhaLimiteCodex(msg)
			}
		}
	}
	errScan := sc.Err()
	errWait := cmd.Wait()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("codex excedeu o timeout de %v; log: %s", timeout, logPath)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, fmt.Errorf("execucao interrompida; log: %s", logPath)
	}
	if errWait != nil {
		isError = true
		stderrTexto := stderr.String()
		if subtipo == "" {
			subtipo = ultimasLinhas(stderrTexto, 3)
		}
		if limiteCodexAtingido(stderrTexto) {
			limite = true
			detalheLimite = linhaLimiteCodex(stderrTexto)
		}
	}

	res := &ResultadoRun{
		IsError:       isError,
		Subtipo:       subtipo,
		Resultado:     ultimoTexto,
		CustoUSD:      custoEstimado(op.Modelo, tokIn, tokOut),
		NumTurns:      numTurns,
		TokensIn:      tokIn,
		TokensOut:     tokOut,
		LogPath:       logPath,
		LimiteSessao:  limite,
		DetalheLimite: detalheLimite,
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
		return nil, fmt.Errorf("codex terminou sem saida util (scan: %v, exit: %v); log: %s", errScan, errWait, logPath)
	}
	if res.CustoUSD > 0 {
		fmt.Printf("  (run: ~US$ %.2f estimado, %d/%d tokens)\n", res.CustoUSD, tokIn, tokOut)
	} else if tokIn+tokOut > 0 {
		fmt.Printf("  (run: %d/%d tokens; custo em USD indisponivel p/ o modelo %q)\n", tokIn, tokOut, op.Modelo)
	}
	return res, nil
}

func textoErroCodex(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return string(raw)
	}
	var partes []string
	for _, k := range []string{"message", "type", "code", "status"} {
		if v, ok := m[k]; ok {
			partes = append(partes, fmt.Sprint(v))
		}
	}
	if len(partes) == 0 {
		return string(raw)
	}
	return strings.Join(partes, " ")
}

func limiteCodexAtingido(texto string) bool {
	t := strings.ToLower(texto)
	if strings.Contains(t, "429") {
		return true
	}
	for _, trecho := range []string{"rate limit", "usage limit", "quota", "too many requests", "insufficient_quota"} {
		if strings.Contains(t, trecho) {
			return true
		}
	}
	return false
}

func linhaLimiteCodex(texto string) string {
	for _, l := range strings.Split(texto, "\n") {
		if limiteCodexAtingido(l) {
			return strings.TrimSpace(l)
		}
	}
	return strings.TrimSpace(texto)
}

// schemaStrictOpenAI adapta um JSON Schema para o modo strict exigido pela
// Responses API: objetos recusam propriedades extras e listam todas as suas
// propriedades em required.
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

func strictNo(n any) {
	switch v := n.(type) {
	case []any:
		for _, item := range v {
			strictNo(item)
		}
	case map[string]any:
		if tipo, _ := v["type"].(string); tipo == "object" {
			if props, ok := v["properties"].(map[string]any); ok {
				v["additionalProperties"] = false
				chaves := make([]string, 0, len(props))
				for k := range props {
					chaves = append(chaves, k)
				}
				sort.Strings(chaves)
				required := make([]any, 0, len(chaves))
				for _, k := range chaves {
					required = append(required, k)
				}
				v["required"] = required
			}
		}
		for _, item := range v {
			strictNo(item)
		}
	}
}
