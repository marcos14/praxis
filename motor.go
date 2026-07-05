package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// OpcoesRun descreve uma execucao headless de um motor de codigo, sempre em
// contexto limpo. Cada motor traduz estas intencoes para suas flags nativas.
type OpcoesRun struct {
	Raiz           string
	Prompt         string
	Modelo         string
	Esforco        string
	AddDirs        []string
	BudgetUSD      float64
	TimeoutMin     int
	Schema         string // se != "", quer saida estruturada conforme este JSON Schema
	SomenteLeitura bool   // revisor: nao edita arquivos nem commita
	ProibirCommit  bool   // executor/corretor: quem commita e o orquestrador
	RotuloLog      string // prefixo do arquivo de log em automacao/logs/
	Ctx            context.Context
	PausaCh        <-chan struct{}
	OnEspera       func(detalhe string)
}

// ResultadoRun e a saida normalizada de qualquer motor.
type ResultadoRun struct {
	IsError       bool
	Subtipo       string
	Resultado     string
	Estruturado   json.RawMessage
	CustoUSD      float64
	NumTurns      int
	TokensIn      int
	TokensOut     int
	LogPath       string
	LimiteSessao  bool
	DetalheLimite string
}

// Aliases de compatibilidade ate o pipeline ser ligado por operacao nas fases
// seguintes do plano.
type OpcoesClaude = OpcoesRun
type ResultadoClaude = ResultadoRun

// Capacidades declara o que um motor faz nativamente.
type Capacidades struct {
	SchemaNativo   bool
	BudgetNativo   bool
	CustoUSDNativo bool
}

// Motor e um backend de execucao de codigo (Claude Code, Codex, etc.).
type Motor interface {
	Nome() string
	Capacidades() Capacidades
	Rodar(op OpcoesRun) (*ResultadoRun, error)
}

var motoresRegistrados = map[string]func() Motor{
	"claude": func() Motor { return motorClaude{} },
	"codex":  func() Motor { return motorCodex{} },
}

func selecionarMotor(nome string) (Motor, error) {
	nome = strings.ToLower(strings.TrimSpace(nome))
	if nome == "" {
		nome = "claude"
	}
	f, ok := motoresRegistrados[nome]
	if !ok {
		return nil, fmt.Errorf("motor desconhecido: %q", nome)
	}
	return f(), nil
}

func motorInstalado(nome string) bool {
	nome = normalizarNomeMotor(nome)
	if nome == "" {
		return false
	}
	_, err := exec.LookPath(nome)
	return err == nil
}

func motoresInstalados() []string {
	var out []string
	for _, nome := range motoresConhecidos() {
		if motorInstalado(nome) {
			out = append(out, nome)
		}
	}
	return out
}

// modeloPadrao devolve o modelo default por motor. Para os que nao sao Claude
// devolvemos "" para deixar o proprio CLI usar o modelo configurado por ele.
func modeloPadrao(nome string) string {
	switch strings.ToLower(strings.TrimSpace(nome)) {
	case "", "claude":
		return "opus"
	case "codex":
		return "gpt-5.5"
	default:
		return ""
	}
}

func esforcoPadrao(nome string) string {
	switch strings.ToLower(strings.TrimSpace(nome)) {
	case "claude", "codex":
		return "high"
	default:
		return ""
	}
}

func coAuthorTrailer(nome string) string {
	switch strings.ToLower(strings.TrimSpace(nome)) {
	case "", "claude":
		return "Co-Authored-By: Claude <noreply@anthropic.com>"
	case "codex":
		return "Co-Authored-By: Codex <noreply@openai.com>"
	default:
		return ""
	}
}

func abrirLog(raiz, rotulo, ext string) (*os.File, string, error) {
	if err := os.MkdirAll(dirLogs(raiz), 0o755); err != nil {
		return nil, "", err
	}
	p := filepath.Join(dirLogs(raiz), fmt.Sprintf("%s-%s.%s", rotulo, agoraTS(), ext))
	f, err := os.Create(p)
	if err != nil {
		return nil, "", err
	}
	return f, p, nil
}

func contextoTimeout(pai context.Context, min int) (context.Context, context.CancelFunc, time.Duration) {
	d := time.Duration(min) * time.Minute
	if d <= 0 {
		d = 2 * time.Hour
	}
	if pai == nil {
		pai = context.Background()
	}
	ctx, cancel := context.WithTimeout(pai, d)
	return ctx, cancel, d
}

// promptComSchema e o fallback para motores sem saida estruturada nativa.
func promptComSchema(prompt, schema string) string {
	if schema == "" {
		return prompt
	}
	return prompt + "\n\n---\nIMPORTANTE: ao final, responda APENAS com um unico objeto JSON valido " +
		"(sem cercas de markdown, sem texto em volta) que satisfaca EXATAMENTE este JSON Schema:\n" + schema
}

// decodificarEstruturado le a saida estruturada de um run: prefere o campo
// nativo; na falta, extrai o JSON do texto final.
func decodificarEstruturado(res *ResultadoRun, v any) error {
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

// custoEstimado aproxima o custo em USD a partir dos tokens, para motores que
// so reportam tokens. Modelo desconhecido retorna 0.
func custoEstimado(modelo string, tokIn, tokOut int) float64 {
	tab := map[string][2]float64{
		"gpt-5.5":     {5, 30},
		"gpt-5":       {1.25, 10},
		"gpt-5-codex": {1.25, 10},
		"gpt-5-mini":  {0.25, 2},
		"o4-mini":     {1.10, 4.40},
	}
	p, ok := tab[strings.ToLower(strings.TrimSpace(modelo))]
	if !ok {
		return 0
	}
	return float64(tokIn)/1e6*p[0] + float64(tokOut)/1e6*p[1]
}
