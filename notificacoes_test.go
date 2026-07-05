package main

import "testing"

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
	n := &Notificador{ini: parseINI("")}
	if n.habilitado() {
		t.Fatal("sem canais ativos, o notificador deveria estar desabilitado")
	}
	// nao deve entrar em panico nem fazer nada
	n.enviar("titulo", "corpo")
}

func TestNotificadorHabilitadoComGoogleChat(t *testing.T) {
	n := &Notificador{ini: parseINI("[google_chat]\nativo = sim\nwebhook_url = https://chat.googleapis.com/v1/spaces/x/messages?key=k&token=t\n")}
	if !n.habilitado() {
		t.Fatal("google_chat ativo deveria habilitar o notificador")
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
