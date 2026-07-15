package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const tokenTeste = "tok-teste-123"

func TestAPIConfigMascaraSegredos(t *testing.T) {
	raiz := t.TempDir()
	cfg := configPadrao()
	tg := cfg.Notificacoes.Canais["telegram"]
	tg.Ativo = true
	tg.BotToken = "SECRET"
	tg.ChatID = "42"
	cfg.Notificacoes.Canais["telegram"] = tg
	cfg.Painel.Token = tokenTeste
	if err := salvarConfig(raiz, cfg); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Header.Set(cabecalhoToken, tokenTeste)
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
	cfg.Painel.Token = tokenTeste
	if err := salvarConfig(raiz, cfg); err != nil {
		t.Fatal(err)
	}
	payload := payloadConfigPainel(cfg, true)
	payload.Motores.ClaudeConfigDirs = map[string]string{"claude_alt": "C:/Users/dev/.claude-alt"}
	payload.Motores.Operacoes["executar"] = "claude_alt"
	payload.Motores.Fallback.Ordem = []string{"claude", "claude_alt", "codex"}
	payload.Motores.Esforcos["codex"] = "medium"
	c := payload.Notificacoes.Canais["telegram"]
	c.BotToken = mascaraSegredo
	c.ChatID = "99"
	payload.Notificacoes.Canais["telegram"] = c
	b, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(string(b)))
	req.Header.Set(cabecalhoToken, tokenTeste)
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
	if got := lida.Motores.Operacoes["executar"]; got != "claude_alt" {
		t.Fatalf("motor executar nao salvo: %q", got)
	}
	if got := lida.Motores.ClaudeConfigDirs["claude_alt"]; got != "C:/Users/dev/.claude-alt" {
		t.Fatalf("claude_config_dirs nao salvo: %q", got)
	}
	if got := lida.Motores.Fallback.Ordem; len(got) != 3 || got[1] != "claude_alt" {
		t.Fatalf("fallback.ordem nao persistiu alias: %+v", got)
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
	cfg.Painel.Token = tokenTeste
	if err := salvarConfig(raiz, cfg); err != nil {
		t.Fatal(err)
	}
	payload := payloadConfigPainel(cfg, true)
	payload.Notificacoes.Eventos["evento_inexistente"] = true
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(string(b)))
	req.Header.Set(cabecalhoToken, tokenTeste)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handlerConfigPainel(raiz).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("POST com evento invalido deveria falhar com 400, veio %d: %s", rr.Code, rr.Body.String())
	}
}

func prepararFasesCSV(t *testing.T, raiz string) {
	t.Helper()
	cfg := configPadrao()
	cfg.Painel.Token = tokenTeste
	if err := salvarConfig(raiz, cfg); err != nil {
		t.Fatal(err)
	}
	fases := []*Fase{{Fase: "1", Titulo: "primeira", Status: StPendente}}
	if err := salvarFases(caminhoCSV(raiz), fases); err != nil {
		t.Fatal(err)
	}
}

func TestAPIFaseStatusAtualiza(t *testing.T) {
	raiz := t.TempDir()
	prepararFasesCSV(t, raiz)

	body := `{"fase":"1","status":"concluida"}`
	req := httptest.NewRequest(http.MethodPost, "/api/fase-status", strings.NewReader(body))
	req.Header.Set(cabecalhoToken, tokenTeste)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handlerFaseStatus(raiz, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/fase-status: %d %s", rr.Code, rr.Body.String())
	}
	fases, err := carregarFases(caminhoCSV(raiz))
	if err != nil {
		t.Fatal(err)
	}
	if got := buscarFase(fases, "1").Status; got != StConcluida {
		t.Fatalf("status nao atualizado, veio %q", got)
	}
}

func TestAPIFaseStatusSemTokenFalha(t *testing.T) {
	raiz := t.TempDir()
	prepararFasesCSV(t, raiz)
	req := httptest.NewRequest(http.MethodPost, "/api/fase-status", strings.NewReader(`{"fase":"1","status":"concluida"}`))
	rr := httptest.NewRecorder()
	handlerFaseStatus(raiz, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("POST sem token deveria falhar com 403, veio %d", rr.Code)
	}
}

func TestAPIFaseStatusInvalidoFalha(t *testing.T) {
	raiz := t.TempDir()
	prepararFasesCSV(t, raiz)
	req := httptest.NewRequest(http.MethodPost, "/api/fase-status", strings.NewReader(`{"fase":"1","status":"voando"}`))
	req.Header.Set(cabecalhoToken, tokenTeste)
	rr := httptest.NewRecorder()
	handlerFaseStatus(raiz, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status invalido deveria falhar com 400, veio %d: %s", rr.Code, rr.Body.String())
	}
}
