package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	dirAutomacao = "automacao"
	nomeConfig   = "autopilot.json"
	nomeCSV      = "fases.csv"
)

// Gate e um bloco de comandos deterministicos de verificacao (build/lint/test)
// rodados pelo orquestrador — a prova de que a fase esta saudavel.
type Gate struct {
	Nome           string   `json:"nome"`
	Dir            string   `json:"dir"` // relativo a raiz do projeto, ou absoluto
	SomenteSeMudou bool     `json:"somente_se_mudou,omitempty"`
	Comandos       []string `json:"comandos"`
}

// GateExtra e um gate opcional referenciado pela coluna gate_extra do CSV
// (ex.: testes de integracao mais lentos ou que exigem setup especial).
type GateExtra struct {
	Nome     string   `json:"nome"`
	Comandos []string `json:"comandos"`
}

type Config struct {
	Plano              string      `json:"plano"`
	Modelo             string      `json:"modelo"`
	AddDirs            []string    `json:"add_dirs"`
	MaxBudgetUSD       float64     `json:"max_budget_usd"`
	TimeoutMin         int         `json:"timeout_min"`
	MaxCorrecoes       int         `json:"max_correcoes"`
	MaxCiclosRevisao   int         `json:"max_ciclos_revisao"`
	VersionarAutomacao bool        `json:"versionar_automacao"` // fases.csv versionado (commit de bookkeeping por fase) ou pasta automacao/ inteira no .gitignore
	Gates              []Gate      `json:"gates"`
	GatesExtra         []GateExtra `json:"gates_extra,omitempty"`
}

func configPadrao() *Config {
	return &Config{
		Plano:              "PLANO.md",
		Modelo:             "opus",
		MaxBudgetUSD:       25,
		TimeoutMin:         120,
		MaxCorrecoes:       2,
		MaxCiclosRevisao:   1,
		VersionarAutomacao: true,
	}
}

func caminhoConfig(raiz string) string { return filepath.Join(raiz, dirAutomacao, nomeConfig) }
func caminhoCSV(raiz string) string    { return filepath.Join(raiz, dirAutomacao, nomeCSV) }
func dirLogs(raiz string) string       { return filepath.Join(raiz, dirAutomacao, "logs") }
func dirPrompts(raiz string) string    { return filepath.Join(raiz, dirAutomacao, "prompts") }

func carregarConfig(raiz string) (*Config, error) {
	b, err := os.ReadFile(caminhoConfig(raiz))
	if err != nil {
		return nil, fmt.Errorf("nao achei %s — rode `praxis inicializar` primeiro (%w)", caminhoConfig(raiz), err)
	}
	cfg := configPadrao()
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("config invalida em %s: %w", caminhoConfig(raiz), err)
	}
	if cfg.Plano == "" {
		return nil, fmt.Errorf("config sem `plano` em %s", caminhoConfig(raiz))
	}
	return cfg, nil
}

func salvarConfig(raiz string, cfg *Config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(caminhoConfig(raiz), append(b, '\n'), 0o644)
}

// resolverRaiz descobre a raiz do projeto: a flag, o cwd, ou o pai (para
// quando o binario e executado de dentro de automacao/).
func resolverRaiz(flag string) string {
	if flag != "" {
		return flag
	}
	if _, err := os.Stat(filepath.Join(".", dirAutomacao, nomeConfig)); err == nil {
		return "."
	}
	if _, err := os.Stat(filepath.Join("..", dirAutomacao, nomeConfig)); err == nil {
		return ".."
	}
	return "."
}

// resolverDir transforma um dir de gate/add_dir em caminho utilizavel:
// absoluto fica como esta; relativo e resolvido contra a raiz.
func resolverDir(raiz, dir string) string {
	if dir == "" || dir == "." {
		return raiz
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(raiz, dir)
}
