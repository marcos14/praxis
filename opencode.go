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

// motorOpencode executa o OpenCode headless (`opencode run --format json`).
// Nativo: read-only (--agent plan) e custo em USD (reportado nos eventos).
// Fallback: nao ha saida estruturada por flag — o schema e injetado no prompt
// (promptComSchema) e o JSON e extraido do texto final. Budget vira teto por
// fase (soft), verificado pelo orquestrador. Multi-dir (add-dirs) nao e
// suportado; avisamos e seguimos com o dir principal.
type motorOpencode struct{}

func (motorOpencode) Nome() string { return "opencode" }

func (motorOpencode) Capacidades() Capacidades {
	return Capacidades{SchemaNativo: false, BudgetNativo: false, CustoUSDNativo: true}
}

// eventoOpencode e uma leitura tolerante do stream JSON do OpenCode: capturamos
// os campos de texto e custo mais provaveis. Os nomes exatos podem variar entre
// versoes — por isso guardamos tambem o stdout bruto como fallback do texto
// final (o extrairJSON depois acha o JSON, quando ha schema).
type eventoOpencode struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Part struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"part"`
	Cost  float64         `json:"cost"`
	Error json.RawMessage `json:"error"`
}

func (motorOpencode) Rodar(op OpcoesRun) (*ResultadoRun, error) {
	if len(op.AddDirs) > 0 {
		fmt.Printf("  (aviso: opencode nao suporta add-dirs; ignorando: %s)\n", strings.Join(op.AddDirs, ", "))
	}

	args := []string{"run", "--format", "json", "--dir", op.Raiz}
	if op.Modelo != "" {
		args = append(args, "--model", op.Modelo)
	}
	if op.SomenteLeitura {
		args = append(args, "--agent", "plan") // agente read-only: nega edicoes
	} else {
		args = append(args, "--auto") // auto-aprova o que nao for explicitamente negado
	}
	args = append(args, promptComSchema(op.Prompt, op.Schema))

	logFile, logPath, err := abrirLog(op.Raiz, op.RotuloLog, "jsonl")
	if err != nil {
		return nil, err
	}
	defer logFile.Close()

	ctx, cancel := contextoTimeout(op.TimeoutMin)
	defer cancel()

	cmd := exec.CommandContext(ctx, "opencode", args...)
	cmd.Dir = op.Raiz
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("nao consegui executar `opencode` (esta no PATH e logado?): %w", err)
	}

	var bruto bytes.Buffer
	var texto strings.Builder
	var custo float64
	isError := false
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		linha := sc.Bytes()
		logFile.Write(linha)
		logFile.Write([]byte{'\n'})
		bruto.Write(linha)
		bruto.WriteByte('\n')
		var ev eventoOpencode
		if json.Unmarshal(linha, &ev) != nil {
			continue
		}
		t := ev.Text
		if t == "" {
			t = ev.Part.Text
		}
		if s := strings.TrimSpace(t); s != "" {
			fmt.Println(indentar(s, "  │ "))
			texto.WriteString(t)
			texto.WriteString("\n")
		}
		if ev.Cost > 0 {
			custo = ev.Cost // o run reporta o custo acumulado
		}
		if ev.Type == "error" || len(ev.Error) > 0 {
			isError = true
		}
	}
	errScan := sc.Err()
	errWait := cmd.Wait()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("opencode excedeu o timeout — log: %s", logPath)
	}

	resultado := strings.TrimSpace(texto.String())
	if resultado == "" {
		resultado = strings.TrimSpace(bruto.String())
	}
	subtipo := ""
	if errWait != nil {
		isError = true
		subtipo = ultimasLinhas(stderr.String(), 3)
	}
	if resultado == "" && errScan != nil {
		return nil, fmt.Errorf("opencode terminou sem saida util (scan: %v, exit: %v) — log: %s", errScan, errWait, logPath)
	}

	res := &ResultadoRun{
		IsError:   isError,
		Subtipo:   subtipo,
		Resultado: resultado,
		CustoUSD:  custo,
		LogPath:   logPath,
	}
	if custo > 0 {
		fmt.Printf("  (run: US$ %.2f)\n", custo)
	}
	return res, nil
}
