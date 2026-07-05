package main

import (
	"bufio"
	"crypto/subtle"
	"encoding/base64"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// authPainel e a configuracao de Basic Auth do painel, lida do autopilot.json.
type authPainel struct {
	ativo  bool
	base64 string
	bind   string
}

func carregarAuthPainel(raiz string) authPainel {
	cfg, err := carregarConfig(raiz)
	if err != nil {
		return authPainel{}
	}
	return authPainel{
		ativo:  cfg.Painel.AuthAtivo,
		base64: strings.TrimSpace(cfg.Painel.CredencialBase64),
		bind:   strings.TrimSpace(cfg.Painel.Bind),
	}
}

// comBasicAuth protege um handler com HTTP Basic Auth, relendo a credencial do
// autopilot.json a cada requisicao.
func comBasicAuth(raiz string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, ativo, semCred := painelRequestAutorizado(raiz, r)
		if !ativo {
			h.ServeHTTP(w, r)
			return
		}
		if semCred {
			http.Error(w, "painel com auth ativo, mas sem credencial configurada", http.StatusServiceUnavailable)
			return
		}
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="Praxis"`)
			http.Error(w, "autenticacao necessaria", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func painelRequestAutorizado(raiz string, r *http.Request) (ok, ativo, semCred bool) {
	auth := carregarAuthPainel(raiz)
	if !auth.ativo {
		return false, false, false
	}
	if auth.base64 == "" {
		return false, true, true
	}
	esperado := "Basic " + auth.base64
	fornecido := r.Header.Get("Authorization")
	return subtle.ConstantTimeCompare([]byte(fornecido), []byte(esperado)) == 1, true, false
}

func servirPainel(raiz string, est *estadoExecucao, auth authPainel) http.Handler {
	h := handlerPainel(raiz, est)
	if auth.ativo {
		fmt.Println("painel protegido por Basic Auth")
		return comBasicAuth(raiz, h)
	}
	return h
}

// cmdAuth gera a credencial base64 (usuario:senha) para o Basic Auth do painel
// e imprime o trecho pronto para colar no autopilot.json.
func cmdAuth(argv []string) error {
	fs := flag.NewFlagSet("auth", flag.ExitOnError)
	user := fs.String("user", "", "usuario")
	pass := fs.String("pass", "", "senha (atencao: fica no historico do shell)")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	entrada := bufio.NewReader(os.Stdin)
	u := strings.TrimSpace(*user)
	if u == "" {
		fmt.Print("Usuario: ")
		l, _ := entrada.ReadString('\n')
		u = strings.TrimSpace(l)
	}
	p := *pass
	if p == "" {
		fmt.Print("Senha (sera exibida; evite em telas compartilhadas): ")
		l, _ := entrada.ReadString('\n')
		p = strings.TrimRight(l, "\r\n")
	}
	if u == "" || p == "" {
		return fmt.Errorf("usuario e senha sao obrigatorios")
	}
	cred := base64.StdEncoding.EncodeToString([]byte(u + ":" + p))
	fmt.Printf(`
Credencial gerada. Cole no bloco "painel" de automacao/autopilot.json:

  "painel": {
    "auth_ativo": true,
    "credencial_base64": "%s",
    "bind": ""
  }

Para testar:
  curl -H "Authorization: Basic %s" http://localhost:%d/

Dica: mantenha o painel so no servidor com "bind = 127.0.0.1" e acesse por tunel SSH.
`, cred, cred, portaPainelPadrao)
	return nil
}
