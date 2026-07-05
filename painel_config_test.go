package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIConfigMascaraSegredos(t *testing.T) {
	raiz := t.TempDir()
	cfg := configPadrao()
	tg := cfg.Notificacoes.Canais["telegram"]
	tg.Ativo = true
	tg.BotToken = "SECRET"
	tg.ChatID = "42"
	cfg.Notificacoes.Canais["telegram"] = tg
	cfg.Painel.AuthAtivo = true
	cfg.Painel.CredencialBase64 = base64.StdEncoding.EncodeToString([]byte("u:p"))
	if err := salvarConfig(raiz, cfg); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Header.Set("Authorization", "Basic "+cfg.Painel.CredencialBase64)
	rr := httptest.NewRecorder()
	handlerConfigPainel(raiz).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/config: %d %s", rr.Code, rr.Body.String())
	}
	var resp painelConfigPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Editavel {
		t.Fatal("GET autenticado deveria ser editavel")
	}
	if got := resp.Notificacoes.Canais["telegram"].BotToken; got != mascaraSegredo {
		t.Fatalf("segredo nao mascarado: %q", got)
	}
}

func TestAPIConfigPostPreservaMascara(t *testing.T) {
	raiz := t.TempDir()
	cfg := configPadrao()
	tg := cfg.Notificacoes.Canais["telegram"]
	tg.Ativo = true
	tg.BotToken = "SECRET"
	tg.ChatID = "42"
	cfg.Notificacoes.Canais["telegram"] = tg
	cfg.Painel.AuthAtivo = true
	cfg.Painel.CredencialBase64 = base64.StdEncoding.EncodeToString([]byte("u:p"))
	if err := salvarConfig(raiz, cfg); err != nil {
		t.Fatal(err)
	}
	payload := payloadConfigPainel(cfg, true)
	payload.Motores.Operacoes["executar"] = "codex"
	payload.Motores.Esforcos["codex"] = "medium"
	c := payload.Notificacoes.Canais["telegram"]
	c.BotToken = mascaraSegredo
	c.ChatID = "99"
	payload.Notificacoes.Canais["telegram"] = c
	b, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(string(b)))
	req.Header.Set("Authorization", "Basic "+cfg.Painel.CredencialBase64)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handlerConfigPainel(raiz).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/config: %d %s", rr.Code, rr.Body.String())
	}
	lida, err := carregarConfig(raiz)
	if err != nil {
		t.Fatal(err)
	}
	if got := lida.Motores.Operacoes["executar"]; got != "codex" {
		t.Fatalf("motor executar nao salvo: %q", got)
	}
	if got := lida.Motores.Esforcos["codex"]; got != "medium" {
		t.Fatalf("esforco codex nao salvo: %q", got)
	}
	if got := lida.Notificacoes.Canais["telegram"].BotToken; got != "SECRET" {
		t.Fatalf("mascara deveria preservar segredo, veio %q", got)
	}
	if got := lida.Notificacoes.Canais["telegram"].ChatID; got != "99" {
		t.Fatalf("chat_id deveria atualizar, veio %q", got)
	}
}

func TestAPIConfigPostSemAuthFalha(t *testing.T) {
	raiz := t.TempDir()
	cfg := configPadrao()
	if err := salvarConfig(raiz, cfg); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	handlerConfigPainel(raiz).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("POST sem auth deveria falhar com 403, veio %d", rr.Code)
	}
}

func TestAPIConfigPostEventoInvalidoFalha(t *testing.T) {
	raiz := t.TempDir()
	cfg := configPadrao()
	cfg.Painel.AuthAtivo = true
	cfg.Painel.CredencialBase64 = base64.StdEncoding.EncodeToString([]byte("u:p"))
	if err := salvarConfig(raiz, cfg); err != nil {
		t.Fatal(err)
	}
	payload := payloadConfigPainel(cfg, true)
	payload.Notificacoes.Eventos["evento_inexistente"] = true
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(string(b)))
	req.Header.Set("Authorization", "Basic "+cfg.Painel.CredencialBase64)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handlerConfigPainel(raiz).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("POST com evento invalido deveria falhar com 400, veio %d: %s", rr.Code, rr.Body.String())
	}
}
