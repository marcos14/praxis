package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseINI(t *testing.T) {
	texto := `
# comentario
[telegram]
ativo = sim
token = 123:ABC
chat_id = 42

[discord]
; outro comentario
ativo=nao
webhook_url = https://discord.com/api/webhooks/x
`
	ini := parseINI(texto)
	if !ini.ativa("telegram") {
		t.Fatal("telegram deveria estar ativo")
	}
	if ini.ativa("discord") {
		t.Fatal("discord nao deveria estar ativo")
	}
	if ini.get("telegram", "token") != "123:ABC" {
		t.Fatalf("token errado: %q", ini.get("telegram", "token"))
	}
	if ini.get("telegram", "CHAT_ID") != "42" {
		t.Fatalf("chave deveria ser case-insensitive, veio %q", ini.get("telegram", "CHAT_ID"))
	}
	if ini.get("discord", "webhook_url") != "https://discord.com/api/webhooks/x" {
		t.Fatalf("webhook_url errado: %q", ini.get("discord", "webhook_url"))
	}
}

func TestNotificadorDesabilitadoQuandoVazio(t *testing.T) {
	raiz := t.TempDir()
	cfg := configPadrao()
	if err := salvarConfig(raiz, cfg); err != nil {
		t.Fatal(err)
	}
	n := carregarNotificador(raiz)
	if n.habilitado() {
		t.Fatal("sem canais ativos, o notificador deveria estar desabilitado")
	}
	// nao deve entrar em panico nem fazer nada
	n.enviar("titulo", "corpo")
}

func TestNotificadorHabilitadoComGoogleChat(t *testing.T) {
	raiz := t.TempDir()
	cfg := configPadrao()
	c := cfg.Notificacoes.Canais["google_chat"]
	c.Ativo = true
	c.WebhookURL = "https://chat.googleapis.com/v1/spaces/x/messages?key=k&token=t"
	cfg.Notificacoes.Canais["google_chat"] = c
	if err := salvarConfig(raiz, cfg); err != nil {
		t.Fatal(err)
	}
	n := carregarNotificador(raiz)
	if !n.habilitado() {
		t.Fatal("google_chat ativo deveria habilitar o notificador")
	}
}

func TestEventoLigadoDefaults(t *testing.T) {
	n := notificacoesPadrao()
	if !eventoLigado(n, "rodada_concluida") {
		t.Fatal("rodada_concluida deveria vir ligado")
	}
	if eventoLigado(n, "fase_iniciada") {
		t.Fatal("fase_iniciada deveria vir desligado")
	}
}

func TestNotificarEventoRespeitaCanalEEvento(t *testing.T) {
	chamadas := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chamadas++
	}))
	defer srv.Close()

	raiz := t.TempDir()
	cfg := configPadrao()
	wh := cfg.Notificacoes.Canais["webhook"]
	wh.Ativo = true
	wh.URL = srv.URL
	cfg.Notificacoes.Canais["webhook"] = wh
	cfg.Notificacoes.Eventos["fase_iniciada"] = false
	cfg.Notificacoes.Eventos["fase_concluida"] = true
	if err := salvarConfig(raiz, cfg); err != nil {
		t.Fatal(err)
	}
	notificarEvento(raiz, "fase_iniciada", "titulo", "corpo")
	if chamadas != 0 {
		t.Fatalf("evento desligado nao deveria chamar webhook, chamou %d", chamadas)
	}
	notificarEvento(raiz, "fase_concluida", "titulo", "corpo")
	if chamadas != 1 {
		t.Fatalf("evento ligado deveria chamar webhook 1x, chamou %d", chamadas)
	}
}

func TestResumoAndamento(t *testing.T) {
	fases := []*Fase{
		{Fase: "1", Status: StConcluida, CustoUSD: 10},
		{Fase: "2", Status: StConcluida, CustoUSD: 5},
		{Fase: "3", Status: StFalhou},
		{Fase: "4", Status: StPendente},
		{Fase: "5", Status: StBloqueada},
	}
	got := resumoAndamento(fases)
	want := "Andamento: 2/5 concluídas · 1 falharam · 1 pendentes · 1 bloqueadas · US$ 15.00"
	if got != want {
		t.Fatalf("resumo:\n got: %q\nwant: %q", got, want)
	}
}

func TestEncurtarURLEscondeToken(t *testing.T) {
	got := encurtarURL("https://api.telegram.org/bot123:SECRET/sendMessage")
	if got != "api.telegram.org/…" {
		t.Fatalf("encurtarURL deveria esconder o token, veio %q", got)
	}
}
