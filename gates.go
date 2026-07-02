package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

type ResultadoGates struct {
	Ok      bool
	LogPath string
	Gate    string // "nome: comando" que falhou
	Erro    string // ultimas linhas da saida do comando que falhou
}

// rodarGates executa os comandos deterministicos de verificacao configurados
// (e o gate_extra da fase, se houver). Nao confia no autorrelato do modelo:
// so o exit code dos comandos decide.
func rodarGates(raiz string, cfg *Config, f *Fase) (*ResultadoGates, error) {
	if err := os.MkdirAll(dirLogs(raiz), 0o755); err != nil {
		return nil, err
	}
	logPath := filepath.Join(dirLogs(raiz), fmt.Sprintf("fase-%s-gates-%s.log", f.Fase, agoraTS()))
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()
	res := &ResultadoGates{Ok: true, LogPath: logPath}

	rodar := func(nome, dir string, comandos []string) bool {
		for _, c := range comandos {
			fmt.Printf("  gate %s: %s ... ", nome, c)
			fmt.Fprintf(logFile, "\n===== [%s] %s (em %s)\n", nome, c, dir)
			saida, err := execShell(dir, c, 30*time.Minute)
			logFile.Write(saida)
			if err != nil {
				fmt.Println("FALHOU")
				res.Ok = false
				res.Gate = nome + ": " + c
				res.Erro = ultimasLinhas(string(saida), 200)
				return false
			}
			fmt.Println("OK")
		}
		return true
	}

	for _, g := range cfg.Gates {
		dir := resolverDir(raiz, g.Dir)
		if g.SomenteSeMudou {
			if limpo, err := gitLimpo(dir); err == nil && limpo {
				fmt.Printf("  gate %s: sem mudancas em %s, pulado\n", g.Nome, dir)
				continue
			}
		}
		if !rodar(g.Nome, dir, g.Comandos) {
			return res, nil
		}
	}

	if f.GateExtra != "" {
		var extra *GateExtra
		for i := range cfg.GatesExtra {
			if cfg.GatesExtra[i].Nome == f.GateExtra {
				extra = &cfg.GatesExtra[i]
				break
			}
		}
		if extra == nil {
			fmt.Printf("  AVISO: gate_extra %q nao existe no autopilot.json — ignorado\n", f.GateExtra)
			return res, nil
		}
		if !rodar(extra.Nome, raiz, extra.Comandos) {
			return res, nil
		}
	}
	return res, nil
}

// execShell roda um comando via shell do sistema (cmd /c no Windows, sh -c
// nos demais), com timeout, devolvendo stdout+stderr combinados.
func execShell(dir, comando string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", comando)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", comando)
	}
	cmd.Dir = dir
	saida, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("timeout de %v: %s", timeout, comando)
	}
	return saida, err
}
