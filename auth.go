package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"flag"
	"fmt"
	"net/http"
	"strings"
)

// cabecalhoToken e o header HTTP onde o painel envia o token de edicao.
const cabecalhoToken = "X-Praxis-Token"

// authPainel e a configuracao de acesso do painel, lida do autopilot.json.
type authPainel struct {
	token string
	bind  string
}

func carregarAuthPainel(raiz string) authPainel {
	cfg, err := carregarConfig(raiz)
	if err != nil {
		return authPainel{}
	}
	return authPainel{
		token: strings.TrimSpace(cfg.Painel.Token),
		bind:  strings.TrimSpace(cfg.Painel.Bind),
	}
}

// gerarToken cria um token aleatorio url-safe, guardado no autopilot.json. Quem
// tem acesso ao projeto pega esse token e autentica no painel para editar.
func gerarToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// tokenDaRequisicao extrai o token do header X-Praxis-Token ou de um
// "Authorization: Bearer <token>".
func tokenDaRequisicao(r *http.Request) string {
	if t := strings.TrimSpace(r.Header.Get(cabecalhoToken)); t != "" {
		return t
	}
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(a, "Bearer "))
	}
	return ""
}

// painelRequestAutorizado diz se a requisicao trouxe o token valido do painel.
// Leitura e sempre liberada; so a edicao (config/status) exige o token.
func painelRequestAutorizado(raiz string, r *http.Request) bool {
	auth := carregarAuthPainel(raiz)
	if auth.token == "" {
		return false
	}
	fornecido := tokenDaRequisicao(r)
	if fornecido == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(fornecido), []byte(auth.token)) == 1
}

func servirPainel(raiz string, est *estadoExecucao, _ authPainel) http.Handler {
	return handlerPainel(raiz, est)
}

// cmdAuth mostra (ou regenera) o token do painel gravado no autopilot.json.
func cmdAuth(argv []string) error {
	fs := flag.NewFlagSet("auth", flag.ExitOnError)
	raizFlag := fs.String("raiz", "", "raiz do projeto (padrao: deteccao automatica)")
	regenerar := fs.Bool("regenerar", false, "gera um novo token, invalidando o anterior")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	raiz := resolverRaiz(*raizFlag)
	cfg, err := carregarConfig(raiz)
	if err != nil {
		return err
	}
	if *regenerar || strings.TrimSpace(cfg.Painel.Token) == "" {
		cfg.Painel.Token = gerarToken()
		if err := salvarConfig(raiz, cfg); err != nil {
			return err
		}
	}
	fmt.Printf(`
Token do painel (bloco "painel" de automacao/autopilot.json):

  %s

Abra o painel, clique em "Entrar" e cole esse token para liberar a edicao de
motores, notificacoes e do status das fases. Compartilhe apenas com quem pode
alterar a execucao. Use "praxis auth --regenerar" para invalidar o token atual.
`, cfg.Painel.Token)
	return nil
}
