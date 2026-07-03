package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OpcoesRun descreve uma execucao headless de um motor de codigo (contexto
// limpo). Os campos sao intencoes genericas; cada motor as traduz para as
// flags nativas que tiver — e emula o que nao tiver (ver Capacidades). Assim
// nenhuma feature e "desligada" por causa do motor: usa-se o caminho nativo
// quando existe, ou um fallback equivalente quando nao.
type OpcoesRun struct {
	Raiz           string
	Prompt         string
	Modelo         string
	AddDirs        []string
	BudgetUSD      float64
	TimeoutMin     int
	Schema         string // se != "", quer saida estruturada conforme este JSON Schema
	SomenteLeitura bool   // revisor: nao pode editar arquivos nem commitar
	ProibirCommit  bool   // executor/corretor: quem comita e o orquestrador
	RotuloLog      string // prefixo do arquivo de log em automacao/logs/
}

// ResultadoRun e a saida normalizada de qualquer motor.
type ResultadoRun struct {
	IsError     bool
	Subtipo     string
	Resultado   string          // texto final do run
	Estruturado json.RawMessage // saida estruturada nativa, quando houver
	CustoUSD    float64
	NumTurns    int
	TokensIn    int
	TokensOut   int
	LogPath     string
}

// Capacidades declara o que um motor faz nativamente. O orquestrador usa isso
// para escolher entre o caminho nativo e o fallback — nunca para desativar
// uma feature.
type Capacidades struct {
	SchemaNativo   bool // saida estruturada por flag do proprio motor
	BudgetNativo   bool // corta o custo em USD durante o run
	CustoUSDNativo bool // o run reporta custo em USD (senao estimamos por tokens)
}

// Motor e um backend de execucao de codigo (Claude Code, Codex, OpenCode...).
type Motor interface {
	Nome() string
	Capacidades() Capacidades
	Rodar(op OpcoesRun) (*ResultadoRun, error)
}

func selecionarMotor(nome string) (Motor, error) {
	switch strings.ToLower(strings.TrimSpace(nome)) {
	case "", "claude":
		return motorClaude{}, nil
	case "codex":
		return motorCodex{}, nil
	case "opencode":
		return motorOpencode{}, nil
	default:
		return nil, fmt.Errorf("motor desconhecido: %q (use claude|codex|opencode)", nome)
	}
}

// modeloPadrao devolve o modelo default por motor. Para os que nao sao Claude
// devolvemos "" para deixar o proprio CLI usar o modelo configurado por ele.
func modeloPadrao(motor string) string {
	switch strings.ToLower(strings.TrimSpace(motor)) {
	case "", "claude":
		return "opus"
	default:
		return ""
	}
}

// coAuthorTrailer devolve o trailer de co-autoria para o commit da fase.
func coAuthorTrailer(motor string) string {
	switch motor {
	case "claude":
		return "Co-Authored-By: Claude <noreply@anthropic.com>"
	case "codex":
		return "Co-Authored-By: Codex <noreply@openai.com>"
	case "opencode":
		return "Co-Authored-By: opencode <noreply@opencode.ai>"
	default:
		return ""
	}
}

// abrirLog cria o arquivo de log de um run em automacao/logs/.
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

// contextoTimeout devolve um contexto com o timeout do run (2h de teto padrao).
func contextoTimeout(min int) (context.Context, context.CancelFunc) {
	d := time.Duration(min) * time.Minute
	if d <= 0 {
		d = 2 * time.Hour
	}
	return context.WithTimeout(context.Background(), d)
}

// promptComSchema e o fallback para motores sem saida estruturada nativa:
// anexa o JSON Schema ao prompt e pede uma resposta apenas-JSON. O
// decodificarEstruturado depois extrai esse JSON do texto final.
func promptComSchema(prompt, schema string) string {
	if schema == "" {
		return prompt
	}
	return prompt + "\n\n---\nIMPORTANTE: ao final, responda APENAS com um unico objeto JSON valido " +
		"(sem cercas de markdown, sem texto em volta) que satisfaca EXATAMENTE este JSON Schema:\n" + schema
}

// decodificarEstruturado le a saida estruturada de um run: prefere o campo
// nativo (structured_output / --output-schema); na falta, extrai o JSON do
// texto final. Funciona igual para todos os motores.
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
// so reportam tokens (ex.: Codex). A tabela e best-effort: modelo desconhecido
// -> 0 (o custo aparece como estimado/indisponivel, nunca bloqueia). Precos por
// 1M de tokens {entrada, saida}.
func custoEstimado(modelo string, tokIn, tokOut int) float64 {
	tab := map[string][2]float64{
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
