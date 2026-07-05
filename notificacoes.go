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
// generico) lidos do notificacoes.ini. E best-effort: falhas nunca derrubam a
// rodada. Os metodos sao seguros em receptor nil (quando o arquivo nao existe).
type Notificador struct {
	ini iniArquivo
	cli *http.Client
}

// carregarNotificador le o notificacoes.ini (se existir). Devolve um
// Notificador sempre utilizavel; se o arquivo faltar ou nenhum canal estiver
// ativo, habilitado() e false e enviar() vira no-op.
func carregarNotificador(raiz string) *Notificador {
	ini, err := lerINI(caminhoNotificacoes(raiz))
	if err != nil {
		ini = iniArquivo{}
	}
	return &Notificador{ini: ini, cli: &http.Client{Timeout: 10 * time.Second}}
}

func (n *Notificador) habilitado() bool {
	if n == nil || n.ini == nil {
		return false
	}
	return n.ini.ativa("telegram") || n.ini.ativa("discord") ||
		n.ini.ativa("slack") || n.ini.ativa("google_chat") || n.ini.ativa("webhook")
}

// enviar dispara o aviso para todos os canais ativos. Best-effort.
func (n *Notificador) enviar(titulo, corpo string) {
	if !n.habilitado() {
		return
	}
	texto := strings.TrimSpace(titulo)
	if c := strings.TrimSpace(corpo); c != "" {
		texto += "\n" + c
	}
	if n.ini.ativa("telegram") {
		n.enviarTelegram(texto)
	}
	if n.ini.ativa("discord") {
		n.postJSON(n.ini.get("discord", "webhook_url"), map[string]string{"content": texto}, "")
	}
	if n.ini.ativa("slack") {
		n.postJSON(n.ini.get("slack", "webhook_url"), map[string]string{"text": texto}, "")
	}
	if n.ini.ativa("google_chat") {
		n.postJSON(n.ini.get("google_chat", "webhook_url"), map[string]string{"text": texto}, "")
	}
	if n.ini.ativa("webhook") {
		n.postJSON(n.ini.get("webhook", "url"),
			map[string]string{"titulo": titulo, "texto": corpo}, n.ini.get("webhook", "header"))
	}
}

func (n *Notificador) enviarTelegram(texto string) {
	token := n.ini.get("telegram", "token")
	chat := n.ini.get("telegram", "chat_id")
	if token == "" || chat == "" {
		return
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	n.postJSON(url, map[string]string{"chat_id": chat, "text": texto}, "")
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
