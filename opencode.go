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

// motorOpencode executa o OpenCode CLI headless (`opencode run --format json`).
// O OpenCode nao oferece schema nem budget nativos: a saida estruturada e
// pedida via prompt e o custo em USD nao e reportado no stream (so tokens),
// entao o teto fica para o orquestrador validar entre runs.
type motorOpencode struct{}

func (motorOpencode) Nome() string { return "opencode" }

func (motorOpencode) Capacidades() Capacidades {
	return Capacidades{SchemaNativo: false, BudgetNativo: false, CustoUSDNativo: false}
}

// eventoOpencode cobre o subconjunto do fluxo JSON de `opencode run --format
// json`. Cada linha e um objeto {type, timestamp, sessionID, ...}.
type eventoOpencode struct {
	Type string `json:"type"`
	Part struct {
		Type   string `json:"type"`
		Text   string `json:"text"`
		Tool   string `json:"tool"`
		Tokens struct {
			Input  int `json:"input"`
			Output int `json:"output"`
		} `json:"tokens"`
		State struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"state"`
	} `json:"part"`
	Error json.RawMessage `json:"error"`
}

func (motorOpencode) Rodar(op OpcoesRun) (*ResultadoRun, error) {
	prompt := op.Prompt
	if op.Schema != "" {
		prompt = promptComSchema(op.Prompt, op.Schema)
	}

	args := []string{"run", "--format", "json", "--dir", op.Raiz}
	if op.Modelo != "" {
		args = append(args, "--model", op.Modelo)
	}
	if op.Esforco != "" {
		args = append(args, "--variant", op.Esforco)
	}
	if !op.SomenteLeitura {
		fmt.Println("  AVISO: OpenCode em modo BYPASS (--auto): auto-aprova permissoes nao negadas explicitamente. Use apenas em ambiente controlado.")
		args = append(args, "--auto")
	}

	logFile, logPath, err := abrirLog(op.Raiz, op.RotuloLog, "jsonl")
	if err != nil {
		return nil, err
	}
	defer logFile.Close()

	ctx, cancel, timeout := contextoTimeout(op.Ctx, op.TimeoutMin)
	defer cancel()

	cmd := exec.CommandContext(ctx, "opencode", args...)
	cmd.Dir = op.Raiz
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = append(cmd.Environ(), "OPENCODE_PERMISSION="+permissoesOpencode(op))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("nao consegui executar `opencode` (esta no PATH e logado?): %w", err)
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
		var ev eventoOpencode
		if json.Unmarshal(linha, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "tool_use":
			if ev.Part.Tool != "" {
				fmt.Printf("  -> %s\n", ev.Part.Tool)
			}
		case "text":
			if t := strings.TrimSpace(ev.Part.Text); t != "" {
				fmt.Println(indentar(t, "  | "))
				ultimoTexto = ev.Part.Text
			}
		case "step_finish":
			numTurns++
			tokIn += ev.Part.Tokens.Input
			tokOut += ev.Part.Tokens.Output
		case "error":
			isError = true
			msg := textoErroOpencode(ev.Error)
			if msg == "" {
				msg = strings.TrimSpace(ev.Part.State.Error)
			}
			if msg != "" {
				subtipo = primeirasLinhas(msg, 1)
			}
			if limiteOpencodeAtingido(msg) {
				limite = true
				detalheLimite = linhaLimiteOpencode(msg)
			}
		}
	}
	errScan := sc.Err()
	errWait := cmd.Wait()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("opencode excedeu o timeout de %v; log: %s", timeout, logPath)
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
		if limiteOpencodeAtingido(stderrTexto) {
			limite = true
			detalheLimite = linhaLimiteOpencode(stderrTexto)
		}
	}

	res := &ResultadoRun{
		IsError:       isError,
		Subtipo:       subtipo,
		Resultado:     ultimoTexto,
		NumTurns:      numTurns,
		TokensIn:      tokIn,
		TokensOut:     tokOut,
		LogPath:       logPath,
		LimiteSessao:  limite,
		DetalheLimite: detalheLimite,
	}
	if res.Resultado == "" && errScan != nil {
		return nil, fmt.Errorf("opencode terminou sem saida util (scan: %v, exit: %v); log: %s", errScan, errWait, logPath)
	}
	if tokIn+tokOut > 0 {
		fmt.Printf("  (run: %d/%d tokens; custo em USD indisponivel para o opencode)\n", tokIn, tokOut)
	}
	return res, nil
}

// permissoesOpencode traduz as intencoes genericas de leitura/commit no JSON
// inline que o OpenCode aceita via OPENCODE_PERMISSION. Sem isso, edit e bash
// tem default "allow" e o revisor conseguiria editar/commitar.
func permissoesOpencode(op OpcoesRun) string {
	bash := map[string]string{}
	if op.ProibirCommit || op.SomenteLeitura {
		bash["git commit *"] = "deny"
		bash["git push *"] = "deny"
	}
	perm := map[string]any{}
	if op.SomenteLeitura {
		perm["edit"] = "deny"
	}
	if len(bash) > 0 {
		perm["bash"] = bash
	}
	if len(perm) == 0 {
		return "{}"
	}
	b, err := json.Marshal(perm)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func textoErroOpencode(raw json.RawMessage) string {
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
	if data, ok := m["data"].(map[string]any); ok {
		if msg, ok := data["message"]; ok {
			return fmt.Sprint(msg)
		}
	}
	for _, k := range []string{"message", "name", "type", "code", "status"} {
		if v, ok := m[k]; ok {
			return fmt.Sprint(v)
		}
	}
	return string(raw)
}

func limiteOpencodeAtingido(texto string) bool {
	t := strings.ToLower(texto)
	if strings.Contains(t, "429") {
		return true
	}
	for _, trecho := range []string{"rate limit", "usage limit", "quota", "too many requests", "insufficient_quota", "overloaded"} {
		if strings.Contains(t, trecho) {
			return true
		}
	}
	return false
}

func linhaLimiteOpencode(texto string) string {
	for _, l := range strings.Split(texto, "\n") {
		if limiteOpencodeAtingido(l) {
			return strings.TrimSpace(l)
		}
	}
	return strings.TrimSpace(texto)
}
