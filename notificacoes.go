package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const nomeNotificacoes = "notificacoes.ini"

func caminhoNotificacoes(raiz string) string {
	return filepath.Join(raiz, dirAutomacao, nomeNotificacoes)
}

// iniArquivo e um .ini simples decodificado: secao -> chave -> valor.
type iniArquivo map[string]map[string]string

// parseINI decodifica um .ini minimo: linhas "[secao]", "chave = valor" e
// comentarios comecando por '#' ou ';'. Tolerante a espacos. Sem dependencias.
func parseINI(texto string) iniArquivo {
	ini := iniArquivo{}
	secao := ""
	for _, bruta := range strings.Split(texto, "\n") {
		l := strings.TrimSpace(bruta)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, ";") {
			continue
		}
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			secao = strings.ToLower(strings.TrimSpace(l[1 : len(l)-1]))
			if ini[secao] == nil {
				ini[secao] = map[string]string{}
			}
			continue
		}
		i := strings.IndexByte(l, '=')
		if i < 0 {
			continue
		}
		chave := strings.ToLower(strings.TrimSpace(l[:i]))
		valor := strings.TrimSpace(l[i+1:])
		if ini[secao] == nil {
			ini[secao] = map[string]string{}
		}
		ini[secao][chave] = valor
	}
	return ini
}

func lerINI(caminho string) (iniArquivo, error) {
	b, err := os.ReadFile(caminho)
	if err != nil {
		return nil, err
	}
	return parseINI(string(b)), nil
}

func (ini iniArquivo) get(secao, chave string) string {
	if ini == nil {
		return ""
	}
	if m := ini[strings.ToLower(secao)]; m != nil {
		return m[strings.ToLower(chave)]
	}
	return ""
}

// ativa diz se a secao tem "ativo = sim".
func (ini iniArquivo) ativa(secao string) bool {
	return ehSim(ini.get(secao, "ativo"))
}

// Notificador envia avisos remotos (Telegram/Discord/Slack/Google Chat/webhook
// generico) lidos do autopilot.json a cada uso. E best-effort: falhas nunca
// derrubam a rodada.
type Notificador struct {
	raiz string
	cli  *http.Client
}

// carregarNotificador devolve um Notificador sempre utilizavel; a config e
// relida do disco no momento do envio.
func carregarNotificador(raiz string) *Notificador {
	return &Notificador{raiz: raiz, cli: &http.Client{Timeout: 10 * time.Second}}
}

func (n *Notificador) habilitado() bool {
	cfg, ok := n.config()
	if !ok {
		return false
	}
	return algumCanalAtivo(cfg.Notificacoes)
}

// enviar preserva a chamada antiga, sem filtrar por evento. Novos pontos do
// pipeline devem preferir enviarEvento/notificarEvento.
func (n *Notificador) enviar(titulo, corpo string) {
	cfg, ok := n.config()
	if !ok || !algumCanalAtivo(cfg.Notificacoes) {
		return
	}
	n.enviarComConfig(cfg, titulo, corpo)
}

func (n *Notificador) enviarEvento(chave, titulo, corpo string) {
	cfg, ok := n.config()
	if !ok || !eventoLigado(cfg.Notificacoes, chave) || !algumCanalAtivo(cfg.Notificacoes) {
		return
	}
	n.enviarComConfig(cfg, titulo, corpo)
}

func notificarEvento(raiz, chave, titulo, corpo string) {
	carregarNotificador(raiz).enviarEvento(chave, titulo, corpo)
}

func (n *Notificador) config() (*Config, bool) {
	if n == nil {
		return nil, false
	}
	cfg, err := carregarConfig(n.raiz)
	if err != nil {
		return nil, false
	}
	return cfg, true
}

func (n *Notificador) enviarComConfig(cfg *Config, titulo, corpo string) {
	texto := strings.TrimSpace(titulo)
	if c := strings.TrimSpace(corpo); c != "" {
		texto += "\n" + c
	}
	canais := cfg.Notificacoes.Canais
	if c := canais["telegram"]; c.Ativo {
		n.enviarTelegram(c, texto)
	}
	if c := canais["discord"]; c.Ativo {
		n.postJSON(c.WebhookURL, map[string]string{"content": texto}, "")
	}
	if c := canais["slack"]; c.Ativo {
		n.postJSON(c.WebhookURL, map[string]string{"text": texto}, "")
	}
	if c := canais["google_chat"]; c.Ativo {
		n.postJSON(c.WebhookURL, map[string]string{"text": texto}, "")
	}
	if c := canais["webhook"]; c.Ativo {
		n.postJSON(c.URL, map[string]string{"titulo": titulo, "texto": corpo}, c.Header)
	}
}

func (n *Notificador) enviarTelegram(c CanalNotificacao, texto string) {
	token := firstNonEmpty(c.BotToken, c.Token)
	chat := c.ChatID
	if token == "" || chat == "" {
		return
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	n.postJSON(url, map[string]string{"chat_id": chat, "text": texto}, "")
}

func algumCanalAtivo(n NotificacoesConfig) bool {
	for _, c := range n.Canais {
		if c.Ativo {
			return true
		}
	}
	return false
}

func eventoLigado(n NotificacoesConfig, chave string) bool {
	if n.Eventos == nil {
		return eventosPadrao()[chave]
	}
	v, ok := n.Eventos[chave]
	if !ok {
		return eventosPadrao()[chave]
	}
	return v
}

// postJSON faz um POST best-effort com corpo JSON e um cabecalho opcional no
// formato "Nome: valor". Erros sao apenas avisados no console.
func (n *Notificador) postJSON(url string, corpo map[string]string, header string) {
	if strings.TrimSpace(url) == "" {
		return
	}
	b, err := json.Marshal(corpo)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		fmt.Printf("AVISO: notificacao (%s): %v\n", encurtarURL(url), err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if h := strings.TrimSpace(header); h != "" {
		if i := strings.IndexByte(h, ':'); i > 0 {
			req.Header.Set(strings.TrimSpace(h[:i]), strings.TrimSpace(h[i+1:]))
		}
	}
	resp, err := n.cli.Do(req)
	if err != nil {
		fmt.Printf("AVISO: notificacao (%s): %v\n", encurtarURL(url), err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		fmt.Printf("AVISO: notificacao (%s) devolveu HTTP %d\n", encurtarURL(url), resp.StatusCode)
	}
}

// encurtarURL esconde tokens/segredos ao logar uma URL (mostra so host+path).
func encurtarURL(url string) string {
	s := url
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i] + "/…"
	}
	return s
}

// resumoAndamento monta um panorama textual da fila para acompanhar os marcos:
// quantas fases concluidas, pendentes, falhas etc. e o custo acumulado.
func resumoAndamento(fases []*Fase) string {
	cont := map[string]int{}
	total := 0
	var custo float64
	for _, f := range fases {
		st := f.Status
		if st == "" {
			st = StPendente
		}
		cont[st]++
		total++
		custo += f.CustoUSD
	}
	partes := []string{fmt.Sprintf("%d/%d concluídas", cont[StConcluida], total)}
	if cont[StFalhou] > 0 {
		partes = append(partes, fmt.Sprintf("%d falharam", cont[StFalhou]))
	}
	pend := cont[StPendente] + cont[StExecutando] + cont[StPausada]
	if pend > 0 {
		partes = append(partes, fmt.Sprintf("%d pendentes", pend))
	}
	if cont[StBloqueada] > 0 {
		partes = append(partes, fmt.Sprintf("%d bloqueadas", cont[StBloqueada]))
	}
	partes = append(partes, fmt.Sprintf("US$ %.2f", custo))
	return "Andamento: " + strings.Join(partes, " · ")
}

// conteudoNotificacoesExemplo devolve o .ini modelo, com exemplos comentados
// para cada plataforma e a secao de autenticacao do painel.
func conteudoNotificacoesExemplo() string {
	return `# Notificações e segurança do Praxis
# ATENÇÃO: este arquivo contém segredos (tokens/senhas) e NÃO é versionado.
# Ative um ou mais canais mudando "ativo = sim" e preenchendo os campos.

# --- Telegram -------------------------------------------------------------
# 1. Fale com @BotFather, use /newbot e copie o token.
# 2. Descubra seu chat_id (ex.: fale com @userinfobot).
[telegram]
ativo   = nao
token   =
chat_id =

# --- Discord --------------------------------------------------------------
# Servidor > Configurações > Integrações > Webhooks > Novo Webhook > Copiar URL
[discord]
ativo       = nao
webhook_url =

# --- Slack ----------------------------------------------------------------
# https://api.slack.com/messaging/webhooks (Incoming Webhooks)
[slack]
ativo       = nao
webhook_url =

# --- Google Chat ----------------------------------------------------------
# No espaço/sala do Google Chat: Apps e integrações > Webhooks > Adicionar
# webhook > copie a URL (contém key & token).
[google_chat]
ativo       = nao
webhook_url =

# --- Webhook genérico (POST JSON: {"titulo","texto"}) ---------------------
[webhook]
ativo  = nao
url    =
# cabeçalho opcional, ex.: Authorization: Bearer SEU_TOKEN
header =

# --- Painel web (Basic Auth) ----------------------------------------------
# Protege o painel com usuário/senha. Gere a credencial base64 com:
#     praxis auth
# e cole o valor em "base64" abaixo (formato de usuario:senha em base64).
[painel]
auth   = nao
base64 =
# bind opcional: 127.0.0.1 restringe o painel ao próprio servidor
# (acesse de fora via túnel SSH). Vazio = todas as interfaces da rede.
bind   =
`
}

// escreverNotificacoesExemplo cria o notificacoes.ini modelo se ele ainda nao
// existir. Devolve true se criou o arquivo, false se ja existia.
func escreverNotificacoesExemplo(raiz string) (bool, error) {
	caminho := caminhoNotificacoes(raiz)
	if _, err := os.Stat(caminho); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(caminho, []byte(conteudoNotificacoesExemplo()), 0o600); err != nil {
		return false, err
	}
	return true, nil
}
