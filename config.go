package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

const (
	dirAutomacao      = "automacao"
	nomeConfig        = "autopilot.json"
	nomeConfigExemplo = "autopilot.exemplo.json"
	nomeCSV           = "fases.csv"
)

// Gate e um bloco de comandos deterministicos de verificacao (build/lint/test)
// rodados pelo orquestrador.
type Gate struct {
	Nome           string   `json:"nome"`
	Dir            string   `json:"dir"`
	SomenteSeMudou bool     `json:"somente_se_mudou,omitempty"`
	Comandos       []string `json:"comandos"`
}

// GateExtra e um gate opcional referenciado pela coluna gate_extra do CSV.
type GateExtra struct {
	Nome     string   `json:"nome"`
	Dir      string   `json:"dir,omitempty"`
	Comandos []string `json:"comandos"`
}

type Config struct {
	Plano              string             `json:"plano"`
	Projeto            string             `json:"projeto,omitempty"` // nome amigavel exibido nas notificacoes
	Autor              string             `json:"autor,omitempty"`   // responsavel; ajuda a distinguir execucoes simultaneas
	Modelo             string             `json:"modelo,omitempty"`  // legado: default do Claude
	Motor              string             `json:"motor,omitempty"`   // legado: default por operacao
	AddDirs            []string           `json:"add_dirs"`
	MaxBudgetUSD       float64            `json:"max_budget_usd"`
	TimeoutMin         int                `json:"timeout_min"`
	MaxCorrecoes       int                `json:"max_correcoes"`
	MaxCiclosRevisao   int                `json:"max_ciclos_revisao"`
	MaxFasesNovas      int                `json:"max_fases_novas"` // teto de fases descobertas inseridas por rodada; -1 desativa
	VersionarAutomacao bool               `json:"versionar_automacao"`
	Gates              []Gate             `json:"gates"`
	GatesExtra         []GateExtra        `json:"gates_extra,omitempty"`
	Motores            MotoresConfig      `json:"motores"`
	Notificacoes       NotificacoesConfig `json:"notificacoes"`
	Painel             PainelConfig       `json:"painel"`
}

type MotoresConfig struct {
	Operacoes map[string]string `json:"operacoes"`
	Modelos   map[string]string `json:"modelos"`
	Esforcos  map[string]string `json:"esforcos,omitempty"`
	Fallback  FallbackConfig    `json:"fallback"`
}

type FallbackConfig struct {
	Ativo bool     `json:"ativo"`
	Ordem []string `json:"ordem"`
}

type NotificacoesConfig struct {
	Canais  map[string]CanalNotificacao `json:"canais"`
	Eventos map[string]bool             `json:"eventos"`
}

type CanalNotificacao struct {
	Ativo      bool   `json:"ativo"`
	BotToken   string `json:"bot_token,omitempty"`
	Token      string `json:"token,omitempty"` // aceito para migracao/compatibilidade
	ChatID     string `json:"chat_id,omitempty"`
	WebhookURL string `json:"webhook_url,omitempty"`
	URL        string `json:"url,omitempty"`
	Template   string `json:"template,omitempty"`
	Header     string `json:"header,omitempty"`
}

type PainelConfig struct {
	AuthAtivo        bool   `json:"auth_ativo"`
	CredencialBase64 string `json:"credencial_base64,omitempty"`
	Bind             string `json:"bind,omitempty"`
}

var operacoesValidas = []string{"planejar", "executar", "corrigir", "revisar"}

type eventoNotificavel struct {
	Chave   string
	Default bool
}

var catalogoEventos = []eventoNotificavel{
	{"inicializacao_concluida", true},
	{"planejamento_iniciado", false},
	{"fase_iniciada", false},
	{"gates_falharam", false},
	{"correcao_iniciada", false},
	{"revisor_reprovou", false},
	{"troca_de_harness", true},
	{"franquia_esgotada", true},
	{"fase_concluida", true},
	{"fases_novas_inseridas", true},
	{"fases_novas_descartadas", true},
	{"marco_concluido", true},
	{"rodada_concluida", true},
	{"rodada_parou", true},
	{"pausa", false},
	{"erro_interno", true},
}

func configPadrao() *Config {
	return &Config{
		Plano:              "PLANO.md",
		Modelo:             "opus",
		AddDirs:            []string{},
		MaxBudgetUSD:       100,
		TimeoutMin:         120,
		MaxCorrecoes:       4,
		MaxCiclosRevisao:   2,
		MaxFasesNovas:      10,
		VersionarAutomacao: true,
		Gates:              []Gate{},
		GatesExtra:         []GateExtra{},
		Motores:            motoresPadrao("opus", ""),
		Notificacoes:       notificacoesPadrao(),
		Painel:             PainelConfig{},
	}
}

func motoresPadrao(modeloLegado, motorLegado string) MotoresConfig {
	if strings.TrimSpace(modeloLegado) == "" {
		modeloLegado = "opus"
	}
	motorDefault := normalizarNomeMotor(motorLegado)
	if motorDefault == "" {
		motorDefault = "claude"
	}
	ops := map[string]string{}
	for _, op := range operacoesValidas {
		ops[op] = motorDefault
	}
	return MotoresConfig{
		Operacoes: ops,
		Modelos: map[string]string{
			"claude":   modeloLegado,
			"codex":    "gpt-5.5",
			"opencode": "",
		},
		Esforcos: map[string]string{
			"claude":   "high",
			"codex":    "high",
			"opencode": "",
		},
		Fallback: FallbackConfig{Ativo: false, Ordem: []string{"claude", "codex"}},
	}
}

func notificacoesPadrao() NotificacoesConfig {
	canais := map[string]CanalNotificacao{}
	for _, nome := range []string{"telegram", "discord", "slack", "google_chat", "webhook"} {
		canais[nome] = CanalNotificacao{}
	}
	return NotificacoesConfig{Canais: canais, Eventos: eventosPadrao()}
}

// camposCanal define quais campos cada canal expoe no autopilot.json. Diferente
// das tags `omitempty` do struct, esses campos aparecem sempre (mesmo vazios),
// para que a tag (ex.: `webhook_url`/`url`) fique visivel e editavel no arquivo.
var camposCanal = map[string][]string{
	"telegram":    {"bot_token", "chat_id"},
	"discord":     {"webhook_url"},
	"slack":       {"webhook_url"},
	"google_chat": {"webhook_url"},
	"webhook":     {"url", "header", "template"},
}

// MarshalJSON serializa os canais mostrando sempre os campos relevantes de cada
// canal (inclusive vazios), sem poluir com campos de outros canais.
func (n NotificacoesConfig) MarshalJSON() ([]byte, error) {
	var canais map[string]json.RawMessage
	if n.Canais != nil {
		canais = make(map[string]json.RawMessage, len(n.Canais))
		for nome, c := range n.Canais {
			b, err := marshalCanal(nome, c)
			if err != nil {
				return nil, err
			}
			canais[nome] = b
		}
	}
	aux := struct {
		Canais  map[string]json.RawMessage `json:"canais"`
		Eventos map[string]bool            `json:"eventos"`
	}{Canais: canais, Eventos: n.Eventos}
	return json.Marshal(aux)
}

func marshalCanal(nome string, c CanalNotificacao) ([]byte, error) {
	campos, ok := camposCanal[nome]
	if !ok {
		// Canal desconhecido: preserva o comportamento padrao com `omitempty`.
		type canalAlias CanalNotificacao
		return json.Marshal(canalAlias(c))
	}
	valores := map[string]string{
		"bot_token":   c.BotToken,
		"chat_id":     c.ChatID,
		"webhook_url": c.WebhookURL,
		"url":         c.URL,
		"header":      c.Header,
		"template":    c.Template,
	}
	m := map[string]any{"ativo": c.Ativo}
	for _, campo := range campos {
		m[campo] = valores[campo]
	}
	if strings.TrimSpace(c.Token) != "" {
		m["token"] = c.Token
	}
	return json.Marshal(m)
}

func eventosPadrao() map[string]bool {
	m := map[string]bool{}
	for _, ev := range catalogoEventos {
		m[ev.Chave] = ev.Default
	}
	return m
}

func caminhoConfig(raiz string) string { return filepath.Join(raiz, dirAutomacao, nomeConfig) }
func caminhoConfigExemplo(raiz string) string {
	return filepath.Join(raiz, dirAutomacao, nomeConfigExemplo)
}
func caminhoCSV(raiz string) string { return filepath.Join(raiz, dirAutomacao, nomeCSV) }
func dirLogs(raiz string) string    { return filepath.Join(raiz, dirAutomacao, "logs") }
func dirPrompts(raiz string) string { return filepath.Join(raiz, dirAutomacao, "prompts") }

func carregarConfig(raiz string) (*Config, error) {
	caminho := caminhoConfig(raiz)
	b, err := os.ReadFile(caminho)
	if err != nil {
		return nil, fmt.Errorf("nao achei %s; rode `praxis inicializar` primeiro (%w)", caminho, err)
	}
	var bruto map[string]json.RawMessage
	if err := json.Unmarshal(b, &bruto); err != nil {
		return nil, fmt.Errorf("config invalida em %s: %w", caminho, err)
	}
	cfg := configPadrao()
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("config invalida em %s: %w", caminho, err)
	}
	if _, ok := bruto["motores"]; !ok {
		cfg.Motores = MotoresConfig{}
	}
	if _, ok := bruto["notificacoes"]; !ok {
		cfg.Notificacoes = NotificacoesConfig{}
	}
	if _, ok := bruto["painel"]; !ok {
		cfg.Painel = PainelConfig{}
	}
	antes := clonarConfig(cfg)
	if err := normalizarConfig(raiz, cfg); err != nil {
		return nil, err
	}
	if cfg.Plano == "" {
		return nil, fmt.Errorf("config sem `plano` em %s", caminho)
	}
	if !reflect.DeepEqual(antes, cfg) {
		if err := salvarConfig(raiz, cfg); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func normalizarConfig(raiz string, cfg *Config) error {
	if cfg.Plano == "" {
		cfg.Plano = "PLANO.md"
	}
	if strings.TrimSpace(cfg.Projeto) == "" {
		cfg.Projeto = nomeProjetoPadrao(raiz)
	}
	if cfg.Modelo == "" {
		cfg.Modelo = "opus"
	}
	if cfg.MaxBudgetUSD == 0 {
		cfg.MaxBudgetUSD = 100
	}
	if cfg.TimeoutMin == 0 {
		cfg.TimeoutMin = 120
	}
	if cfg.MaxCorrecoes == 0 {
		cfg.MaxCorrecoes = 4
	}
	if cfg.MaxCiclosRevisao == 0 {
		cfg.MaxCiclosRevisao = 2
	}
	if cfg.MaxFasesNovas == 0 {
		cfg.MaxFasesNovas = 10
	}
	if len(cfg.Motores.Operacoes) == 0 {
		cfg.Motores.Operacoes = motoresPadrao(cfg.Modelo, cfg.Motor).Operacoes
	}
	if len(cfg.Motores.Modelos) == 0 {
		cfg.Motores.Modelos = motoresPadrao(cfg.Modelo, cfg.Motor).Modelos
	}
	if len(cfg.Motores.Esforcos) == 0 {
		cfg.Motores.Esforcos = motoresPadrao(cfg.Modelo, cfg.Motor).Esforcos
	}
	for _, motor := range motoresConhecidos() {
		if strings.TrimSpace(cfg.Motores.Esforcos[motor]) == "" {
			if e := esforcoPadrao(motor); e != "" {
				cfg.Motores.Esforcos[motor] = e
			}
		}
	}
	if len(cfg.Motores.Fallback.Ordem) == 0 {
		cfg.Motores.Fallback.Ordem = []string{"claude", "codex"}
	}
	if cfg.Notificacoes.Canais == nil && cfg.Notificacoes.Eventos == nil {
		importarNotificacoesLegadas(raiz, cfg)
	}
	if len(cfg.Notificacoes.Canais) == 0 {
		cfg.Notificacoes.Canais = notificacoesPadrao().Canais
	}
	if len(cfg.Notificacoes.Eventos) == 0 {
		cfg.Notificacoes.Eventos = eventosPadrao()
	}
	for _, ev := range catalogoEventos {
		if _, ok := cfg.Notificacoes.Eventos[ev.Chave]; !ok {
			cfg.Notificacoes.Eventos[ev.Chave] = ev.Default
		}
	}
	for _, nome := range []string{"telegram", "discord", "slack", "google_chat", "webhook"} {
		if _, ok := cfg.Notificacoes.Canais[nome]; !ok {
			cfg.Notificacoes.Canais[nome] = CanalNotificacao{}
		}
	}
	return validarConfigMotores(cfg)
}

func validarConfigMotores(cfg *Config) error {
	for op, motor := range cfg.Motores.Operacoes {
		if !operacaoValida(op) {
			return fmt.Errorf("operacao de motor desconhecida em config: %q", op)
		}
		if _, err := selecionarMotor(motor); err != nil {
			return fmt.Errorf("motor da operacao %s invalido: %w", op, err)
		}
	}
	for _, motor := range cfg.Motores.Fallback.Ordem {
		if _, err := selecionarMotor(motor); err != nil {
			return fmt.Errorf("motor invalido na ordem de fallback: %w", err)
		}
	}
	for motor, esforco := range cfg.Motores.Esforcos {
		motor = normalizarNomeMotor(motor)
		if _, err := selecionarMotor(motor); err != nil {
			return fmt.Errorf("esforco configurado para motor desconhecido %q", motor)
		}
		if !esforcoValidoParaMotor(motor, esforco) {
			return fmt.Errorf("esforco invalido para %s: %q", motor, esforco)
		}
	}
	return nil
}

func operacaoValida(op string) bool {
	for _, v := range operacoesValidas {
		if op == v {
			return true
		}
	}
	return false
}

func salvarConfig(raiz string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(caminhoConfig(raiz)), 0o755); err != nil {
		return err
	}
	if err := escreverJSONAtomico(caminhoConfig(raiz), cfg, 0o600); err != nil {
		return err
	}
	return escreverConfigExemplo(raiz, cfg)
}

func escreverJSONAtomico(caminho string, v any, perm os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(caminho), "."+filepath.Base(caminho)+".tmp-*")
	if err != nil {
		return err
	}
	tmpNome := tmp.Name()
	defer os.Remove(tmpNome)
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpNome, caminho)
}

func escreverConfigExemplo(raiz string, cfg *Config) error {
	ex := clonarConfig(cfg)
	for nome, canal := range ex.Notificacoes.Canais {
		canal.BotToken = ""
		canal.Token = ""
		canal.ChatID = ""
		canal.WebhookURL = ""
		canal.URL = ""
		canal.Template = ""
		canal.Header = ""
		ex.Notificacoes.Canais[nome] = canal
	}
	ex.Painel.CredencialBase64 = ""
	return escreverJSONAtomico(caminhoConfigExemplo(raiz), ex, 0o644)
}

func clonarConfig(cfg *Config) *Config {
	b, _ := json.Marshal(cfg)
	cp := configPadrao()
	_ = json.Unmarshal(b, cp)
	return cp
}

func importarNotificacoesLegadas(raiz string, cfg *Config) {
	ini, err := lerINI(caminhoNotificacoes(raiz))
	if err != nil {
		cfg.Notificacoes = notificacoesPadrao()
		return
	}
	n := notificacoesPadrao()
	n.Canais["telegram"] = CanalNotificacao{
		Ativo:    ini.ativa("telegram"),
		BotToken: firstNonEmpty(ini.get("telegram", "bot_token"), ini.get("telegram", "token")),
		ChatID:   ini.get("telegram", "chat_id"),
	}
	for _, nome := range []string{"discord", "slack", "google_chat"} {
		n.Canais[nome] = CanalNotificacao{Ativo: ini.ativa(nome), WebhookURL: ini.get(nome, "webhook_url")}
	}
	n.Canais["webhook"] = CanalNotificacao{
		Ativo:    ini.ativa("webhook"),
		URL:      ini.get("webhook", "url"),
		Template: ini.get("webhook", "template"),
		Header:   ini.get("webhook", "header"),
	}
	cfg.Notificacoes = n
	cfg.Painel = PainelConfig{
		AuthAtivo:        ehSim(ini.get("painel", "auth")),
		CredencialBase64: strings.TrimSpace(ini.get("painel", "base64")),
		Bind:             strings.TrimSpace(ini.get("painel", "bind")),
	}
	_ = os.Rename(caminhoNotificacoes(raiz), caminhoNotificacoes(raiz)+".bak")
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// nomeProjetoPadrao deriva um nome amigavel a partir do diretorio raiz, usado
// para identificar o projeto nas notificacoes quando `projeto` nao foi definido.
func nomeProjetoPadrao(raiz string) string {
	r := strings.TrimSpace(raiz)
	if r == "" {
		r = "."
	}
	if abs, err := filepath.Abs(r); err == nil && abs != "" {
		r = abs
	}
	nome := filepath.Base(r)
	if nome == "." || nome == string(filepath.Separator) || nome == "" {
		return "praxis"
	}
	return nome
}

func motorParaOperacao(cfg *Config, operacao string) string {
	if cfg == nil {
		return "claude"
	}
	if m := normalizarNomeMotor(cfg.Motores.Operacoes[operacao]); m != "" {
		return m
	}
	if m := normalizarNomeMotor(cfg.Motor); m != "" {
		return m
	}
	return "claude"
}

func modeloParaMotor(cfg *Config, motor string) string {
	motor = normalizarNomeMotor(motor)
	if cfg == nil {
		return modeloPadrao(motor)
	}
	if modelo := strings.TrimSpace(cfg.Motores.Modelos[motor]); modelo != "" {
		return modelo
	}
	if motor == "claude" && strings.TrimSpace(cfg.Modelo) != "" {
		return strings.TrimSpace(cfg.Modelo)
	}
	return modeloPadrao(motor)
}

func esforcoParaMotor(cfg *Config, motor string) string {
	motor = normalizarNomeMotor(motor)
	if cfg == nil {
		return esforcoPadrao(motor)
	}
	if esforco := strings.TrimSpace(cfg.Motores.Esforcos[motor]); esforco != "" {
		return strings.ToLower(esforco)
	}
	return esforcoPadrao(motor)
}

func esforcoValidoParaMotor(motor, esforco string) bool {
	esforco = strings.ToLower(strings.TrimSpace(esforco))
	if esforco == "" {
		return true
	}
	switch normalizarNomeMotor(motor) {
	case "claude":
		return esforco == "low" || esforco == "medium" || esforco == "high" || esforco == "xhigh" || esforco == "max"
	case "codex":
		return esforco == "low" || esforco == "medium" || esforco == "high"
	case "opencode":
		// --variant e especifico do provider (ex.: minimal/low/medium/high/max);
		// aceitamos qualquer valor nao vazio.
		return true
	default:
		return false
	}
}

func normalizarNomeMotor(nome string) string {
	return strings.ToLower(strings.TrimSpace(nome))
}

func eventosConhecidos() []string {
	out := make([]string, 0, len(catalogoEventos))
	for _, ev := range catalogoEventos {
		out = append(out, ev.Chave)
	}
	sort.Strings(out)
	return out
}

func motoresConhecidos() []string {
	out := make([]string, 0, len(motoresRegistrados))
	for nome := range motoresRegistrados {
		out = append(out, nome)
	}
	sort.Strings(out)
	return out
}

// resolverRaiz descobre a raiz do projeto: a flag, o cwd, ou o pai.
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

// resolverDir transforma um dir de gate/add_dir em caminho utilizavel.
func resolverDir(raiz, dir string) string {
	if dir == "" || dir == "." {
		return raiz
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(raiz, dir)
}
