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

// authPainel e a configuracao de Basic Auth do painel, lida da secao [painel]
// do notificacoes.ini.
type authPainel struct {
	ativo  bool
	base64 string // credencial usuario:senha em base64
	bind   string // interface de escuta (ex.: 127.0.0.1); vazio = todas
}

func carregarAuthPainel(raiz string) authPainel {
	ini, err := lerINI(caminhoNotificacoes(raiz))
	if err != nil {
		return authPainel{}
	}
	return authPainel{
		ativo:  ehSim(ini.get("painel", "auth")),
		base64: strings.TrimSpace(ini.get("painel", "base64")),
		bind:   strings.TrimSpace(ini.get("painel", "bind")),
	}
}

// comBasicAuth protege um handler com HTTP Basic Auth, comparando o cabecalho
// Authorization com a credencial base64 configurada em tempo constante.
func comBasicAuth(h http.Handler, cred64 string) http.Handler {
	esperado := "Basic " + cred64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fornecido := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(fornecido), []byte(esperado)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Praxis"`)
			http.Error(w, "autenticação necessária", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// servirPainel monta o handler do painel, aplicando Basic Auth quando a secao
// [painel] do notificacoes.ini estiver ativa e com credencial preenchida.
func servirPainel(raiz string, est *estadoExecucao, auth authPainel) http.Handler {
	h := handlerPainel(raiz, est)
	if auth.ativo && auth.base64 != "" {
		fmt.Println("🔒 painel protegido por Basic Auth")
		return comBasicAuth(h, auth.base64)
	}
	if auth.ativo && auth.base64 == "" {
		fmt.Println("AVISO: [painel] auth=sim mas base64 vazio — painel SEM autenticação. Gere a credencial com 'praxis auth'.")
	}
	return h
}

// cmdAuth gera a credencial base64 (usuario:senha) para o Basic Auth do painel
// e imprime o trecho pronto para colar no notificacoes.ini.
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
		fmt.Print("Usuário: ")
		l, _ := entrada.ReadString('\n')
		u = strings.TrimSpace(l)
	}
	p := *pass
	if p == "" {
		fmt.Print("Senha (será exibida — evite em telas compartilhadas): ")
		l, _ := entrada.ReadString('\n')
		p = strings.TrimRight(l, "\r\n")
	}
	if u == "" || p == "" {
		return fmt.Errorf("usuário e senha são obrigatórios")
	}
	cred := base64.StdEncoding.EncodeToString([]byte(u + ":" + p))
	fmt.Printf(`
Credencial gerada. Cole no automacao/notificacoes.ini, seção [painel]:

  [painel]
  auth   = sim
  base64 = %s

Para testar:
  curl -H "Authorization: Basic %s" http://localhost:%d/

Dica: mantenha o painel só no servidor com "bind = 127.0.0.1" e acesse por túnel SSH.
`, cred, cred, portaPainelPadrao)
	return nil
}
