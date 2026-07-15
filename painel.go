package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const portaPainelPadrao = 7799

// estadoExecucao guarda, em memoria, o estado ao vivo de uma rodada do
// `executar --painel`, para que o painel continue de pe e mostre por que a
// execucao terminou — fim normal, falha de fase, interrupcao (Ctrl+C) ou
// panico. E compartilhado entre o pipeline e o servidor HTTP do painel.
type estadoExecucao struct {
	mu        sync.Mutex
	ativa     bool   // a rodada ainda esta em andamento
	situacao  string // executando|concluido|falhou|interrompido|erro
	motivo    string // mensagem/motivo da parada
	faseAtual string // fase corrente (informativo)
}

// execInfo e a projecao serializavel de estadoExecucao exposta no /api/status.
type execInfo struct {
	Ativa     bool   `json:"ativa"`
	Situacao  string `json:"situacao"`
	Motivo    string `json:"motivo"`
	FaseAtual string `json:"fase_atual"`
}

func novoEstadoExecucao() *estadoExecucao {
	return &estadoExecucao{ativa: true, situacao: "executando"}
}

func (e *estadoExecucao) definir(situacao, motivo string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.situacao, e.motivo = situacao, motivo
	e.mu.Unlock()
}

func (e *estadoExecucao) definirFase(fase string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.faseAtual = fase
	e.mu.Unlock()
}

func (e *estadoExecucao) encerrar(situacao, motivo string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.ativa, e.situacao, e.motivo = false, situacao, motivo
	e.mu.Unlock()
}

// emAndamento diz se a rodada ainda nao foi finalizada (usado para decidir se
// um Ctrl+C interrompe a rodada ou fecha o painel).
func (e *estadoExecucao) emAndamento() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ativa
}

func (e *estadoExecucao) info() *execInfo {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return &execInfo{Ativa: e.ativa, Situacao: e.situacao, Motivo: e.motivo, FaseAtual: e.faseAtual}
}

// cmdPainel sobe o microsite de acompanhamento e bloqueia servindo ate o
// usuario interromper (Ctrl+C). Le o fases.csv a cada requisicao, entao
// reflete o andamento ao vivo enquanto o `executar` roda.
func cmdPainel(argv []string) error {
	fs := flag.NewFlagSet("painel", flag.ExitOnError)
	raizFlag := fs.String("raiz", "", "raiz do projeto (padrao: deteccao automatica)")
	porta := fs.Int("porta", portaPainelPadrao, "porta HTTP do painel")
	abrirFlag := fs.String("abrir", "sim", "abrir o navegador automaticamente: sim|nao")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	raiz := resolverRaiz(*raizFlag)
	// falha cedo se o projeto nao foi inicializado
	if _, err := carregarFases(caminhoCSV(raiz)); err != nil {
		return err
	}

	auth := carregarAuthPainel(raiz)
	ln, err := escutarPainel(auth.bind, *porta)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://localhost:%d", ln.Addr().(*net.TCPAddr).Port)
	imprimirEnderecos(auth.bind, ln.Addr().(*net.TCPAddr).Port)
	if ehSim(*abrirFlag) {
		abrirNavegador(url)
	}
	fmt.Println("\nCtrl+C para encerrar o painel.")
	return http.Serve(ln, servirPainel(raiz, nil, auth))
}

// iniciarPainel sobe o painel em segundo plano (usado pelo `executar --painel`).
// Devolve a URL local; nunca aborta a execucao — falhas sao apenas avisadas.
// O estado da rodada (est) e compartilhado para o painel refletir uma parada.
func iniciarPainel(raiz string, porta int, abrir bool, est *estadoExecucao) string {
	auth := carregarAuthPainel(raiz)
	ln, err := escutarPainel(auth.bind, porta)
	if err != nil {
		fmt.Printf("AVISO: nao consegui subir o painel: %v\n", err)
		return ""
	}
	url := fmt.Sprintf("http://localhost:%d", ln.Addr().(*net.TCPAddr).Port)
	imprimirEnderecos(auth.bind, ln.Addr().(*net.TCPAddr).Port)
	go func() { _ = http.Serve(ln, servirPainel(raiz, est, auth)) }()
	if abrir {
		abrirNavegador(url)
	}
	return url
}

func escutarPainel(bind string, porta int) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", bind, porta))
	if err != nil {
		return nil, fmt.Errorf("nao consegui abrir a porta %d (ja em uso?): %w", porta, err)
	}
	return ln, nil
}

func imprimirEnderecos(bind string, porta int) {
	fmt.Printf("\n📊 Painel de acompanhamento:\n")
	fmt.Printf("   http://localhost:%d\n", porta)
	// com bind restrito ao loopback, nao ha endereco de rede a anunciar
	if bind == "127.0.0.1" || bind == "localhost" || bind == "::1" {
		fmt.Println("   (restrito ao localhost por 'bind' — acesse de fora via túnel SSH)")
		return
	}
	for _, ip := range ipsLocais() {
		fmt.Printf("   http://%s:%d  (na rede local)\n", ip, porta)
	}
}

func handlerPainel(raiz string, est *estadoExecucao) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(paginaPainel))
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		resp := montarStatus(raiz, est)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/api/config", handlerConfigPainel(raiz))
	mux.HandleFunc("/api/fase-status", handlerFaseStatus(raiz, est))
	mux.HandleFunc("/api/logs", handlerLogs(raiz))
	return mux
}

const mascaraSegredo = "********"

type painelConfigPayload struct {
	Editavel           bool               `json:"editavel"`
	Motores            MotoresConfig      `json:"motores"`
	Notificacoes       NotificacoesConfig `json:"notificacoes"`
	Painel             PainelConfig       `json:"painel"`
	MotoresDisponiveis []string           `json:"motores_disponiveis,omitempty"`
	OperacoesValidas   []string           `json:"operacoes_validas,omitempty"`
	EventosConhecidos  []string           `json:"eventos_conhecidos,omitempty"`
	CanaisConhecidos   []string           `json:"canais_conhecidos,omitempty"`
}

func handlerConfigPainel(raiz string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			cfg, err := carregarConfig(raiz)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			resp := payloadConfigPainel(cfg, painelRequestAutorizado(raiz, r))
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(resp)
		case http.MethodPost:
			if !painelRequestAutorizado(raiz, r) {
				http.Error(w, "edicao de config exige token valido do painel", http.StatusForbidden)
				return
			}
			var req painelConfigPayload
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "JSON invalido: "+err.Error(), http.StatusBadRequest)
				return
			}
			cfg, err := carregarConfig(raiz)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if err := aplicarPayloadConfig(cfg, req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := salvarConfig(raiz, cfg); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			resp := payloadConfigPainel(cfg, true)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.Error(w, "metodo nao permitido", http.StatusMethodNotAllowed)
		}
	}
}

func payloadConfigPainel(cfg *Config, editavel bool) painelConfigPayload {
	cp := clonarConfig(cfg)
	mascararSegredos(&cp.Notificacoes, &cp.Painel)
	return painelConfigPayload{
		Editavel:           editavel,
		Motores:            cp.Motores,
		Notificacoes:       cp.Notificacoes,
		Painel:             cp.Painel,
		MotoresDisponiveis: motoresConhecidos(),
		OperacoesValidas:   append([]string(nil), operacoesValidas...),
		EventosConhecidos:  eventosConhecidos(),
		CanaisConhecidos:   []string{"telegram", "discord", "slack", "google_chat", "webhook"},
	}
}

func mascararSegredos(n *NotificacoesConfig, p *PainelConfig) {
	for nome, c := range n.Canais {
		c.BotToken = mascararSePreenchido(c.BotToken)
		c.Token = mascararSePreenchido(c.Token)
		c.ChatID = mascararSePreenchido(c.ChatID)
		c.WebhookURL = mascararSePreenchido(c.WebhookURL)
		c.URL = mascararSePreenchido(c.URL)
		c.Template = mascararSePreenchido(c.Template)
		c.Header = mascararSePreenchido(c.Header)
		n.Canais[nome] = c
	}
	p.Token = mascararSePreenchido(p.Token)
}

func mascararSePreenchido(v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return mascaraSegredo
}

func aplicarPayloadConfig(cfg *Config, req painelConfigPayload) error {
	if req.Motores.ClaudeConfigDirs != nil {
		dirs := map[string]string{}
		for alias, dir := range req.Motores.ClaudeConfigDirs {
			alias = normalizarNomeMotor(alias)
			dir = strings.TrimSpace(dir)
			if alias == "" {
				continue
			}
			if dir == "" {
				return fmt.Errorf("claude_config_dirs[%s] nao pode ser vazio", alias)
			}
			if alias != "claude" {
				if _, ok := motoresRegistrados[alias]; ok {
					return fmt.Errorf("alias de claude conflita com motor registrado: %s", alias)
				}
			}
			dirs[alias] = dir
		}
		cfg.Motores.ClaudeConfigDirs = dirs
	}
	if req.Motores.Operacoes != nil {
		ops := map[string]string{}
		for _, op := range operacoesValidas {
			m := normalizarNomeMotor(req.Motores.Operacoes[op])
			if m == "" {
				m = motorParaOperacao(cfg, op)
			}
			if _, err := resolverMotorConfig(cfg, m); err != nil {
				return fmt.Errorf("motor invalido para %s: %w", op, err)
			}
			ops[op] = m
		}
		for op := range req.Motores.Operacoes {
			if !operacaoValida(op) {
				return fmt.Errorf("operacao invalida: %s", op)
			}
		}
		cfg.Motores.Operacoes = ops
	}
	if req.Motores.Modelos != nil {
		modelos := map[string]string{}
		for motor, modelo := range req.Motores.Modelos {
			motor = normalizarNomeMotor(motor)
			if _, err := resolverMotorConfig(cfg, motor); err != nil {
				return fmt.Errorf("modelo configurado para motor desconhecido %q", motor)
			}
			modelos[motor] = strings.TrimSpace(modelo)
		}
		if len(modelos) > 0 {
			cfg.Motores.Modelos = modelos
			if modeloClaude := strings.TrimSpace(modelos["claude"]); modeloClaude != "" {
				cfg.Modelo = modeloClaude
			}
		}
	}
	if req.Motores.Esforcos != nil {
		esforcos := map[string]string{}
		for motor, esforco := range req.Motores.Esforcos {
			motor = normalizarNomeMotor(motor)
			resolvido, err := resolverMotorConfig(cfg, motor)
			if err != nil {
				return fmt.Errorf("esforco configurado para motor desconhecido %q", motor)
			}
			esforco = strings.ToLower(strings.TrimSpace(esforco))
			if !esforcoValidoParaMotor(resolvido.NomeBase, esforco) {
				return fmt.Errorf("esforco invalido para %s: %q", motor, esforco)
			}
			esforcos[motor] = esforco
		}
		if len(esforcos) > 0 {
			cfg.Motores.Esforcos = esforcos
		}
	}
	if req.Motores.Fallback.Ordem != nil {
		var ordem []string
		for _, motor := range req.Motores.Fallback.Ordem {
			motor = normalizarNomeMotor(motor)
			if motor == "" {
				continue
			}
			if _, err := resolverMotorConfig(cfg, motor); err != nil {
				return fmt.Errorf("motor invalido no fallback: %w", err)
			}
			ordem = append(ordem, motor)
		}
		if len(ordem) == 0 {
			return fmt.Errorf("fallback.ordem nao pode ficar vazia")
		}
		cfg.Motores.Fallback.Ordem = ordem
	}
	cfg.Motores.Fallback.Ativo = req.Motores.Fallback.Ativo

	if req.Notificacoes.Canais != nil {
		canais := cfg.Notificacoes.Canais
		if canais == nil {
			canais = notificacoesPadrao().Canais
		}
		conhecidos := map[string]bool{"telegram": true, "discord": true, "slack": true, "google_chat": true, "webhook": true}
		for nome, canalReq := range req.Notificacoes.Canais {
			if !conhecidos[nome] {
				return fmt.Errorf("canal invalido: %s", nome)
			}
			atual := canais[nome]
			canalReq.BotToken = preservarMascara(canalReq.BotToken, atual.BotToken)
			canalReq.Token = preservarMascara(canalReq.Token, atual.Token)
			canalReq.ChatID = preservarMascara(canalReq.ChatID, atual.ChatID)
			canalReq.WebhookURL = preservarMascara(canalReq.WebhookURL, atual.WebhookURL)
			canalReq.URL = preservarMascara(canalReq.URL, atual.URL)
			canalReq.Template = preservarMascara(canalReq.Template, atual.Template)
			canalReq.Header = preservarMascara(canalReq.Header, atual.Header)
			canais[nome] = canalReq
		}
		cfg.Notificacoes.Canais = canais
	}
	if req.Notificacoes.Eventos != nil {
		eventos := eventosPadrao()
		conhecidos := map[string]bool{}
		for _, ev := range catalogoEventos {
			conhecidos[ev.Chave] = true
		}
		for chave, ligado := range req.Notificacoes.Eventos {
			if !conhecidos[chave] {
				return fmt.Errorf("evento invalido: %s", chave)
			}
			eventos[chave] = ligado
		}
		cfg.Notificacoes.Eventos = eventos
	}
	cfg.Painel.Bind = strings.TrimSpace(req.Painel.Bind)
	return validarConfigMotores(cfg)
}

func preservarMascara(recebido, atual string) string {
	if recebido == mascaraSegredo {
		return atual
	}
	return strings.TrimSpace(recebido)
}

type faseStatusPayload struct {
	Fase   string `json:"fase"`
	Status string `json:"status"`
}

// handlerFaseStatus permite editar remotamente o status de uma fase no
// fases.csv. Exige token valido do painel.
func handlerFaseStatus(raiz string, est *estadoExecucao) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "metodo nao permitido", http.StatusMethodNotAllowed)
			return
		}
		if !painelRequestAutorizado(raiz, r) {
			http.Error(w, "edicao de status exige token valido do painel", http.StatusForbidden)
			return
		}
		var req faseStatusPayload
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "JSON invalido: "+err.Error(), http.StatusBadRequest)
			return
		}
		req.Fase = strings.TrimSpace(req.Fase)
		req.Status = strings.TrimSpace(req.Status)
		if req.Fase == "" {
			http.Error(w, "fase obrigatoria", http.StatusBadRequest)
			return
		}
		if !statusValido(req.Status) {
			http.Error(w, "status invalido: "+req.Status, http.StatusBadRequest)
			return
		}
		csvPath := caminhoCSV(raiz)
		fases, err := carregarFases(csvPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		f := buscarFase(fases, req.Fase)
		if f == nil {
			http.Error(w, "fase nao encontrada: "+req.Fase, http.StatusNotFound)
			return
		}
		f.Status = req.Status
		if err := salvarFases(csvPath, fases); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp := montarStatus(raiz, est)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type painelFase struct {
	Fase         string   `json:"fase"`
	Titulo       string   `json:"titulo"`
	Status       string   `json:"status"`
	DependeDe    []string `json:"depende_de"`
	RequerHumano bool     `json:"requer_humano"`
	GateExtra    string   `json:"gate_extra"`
	Modelo       string   `json:"modelo"`
	Tentativas   int      `json:"tentativas"`
	CustoUSD     float64  `json:"custo_usd"`
	ConcluidoEm  string   `json:"concluido_em"`
	Observacao   string   `json:"observacao"`
}

type painelStatus struct {
	Projeto       string         `json:"projeto"`
	Plano         string         `json:"plano"`
	Atualizado    string         `json:"atualizado"`
	Erro          string         `json:"erro,omitempty"`
	Resumo        map[string]int `json:"resumo"`
	Total         int            `json:"total"`
	CustoTotal    float64        `json:"custo_total"`
	Execucao      *execInfo      `json:"execucao,omitempty"`
	StatusValidos []string       `json:"status_validos,omitempty"`
	Fases         []painelFase   `json:"fases"`
}

func montarStatus(raiz string, est *estadoExecucao) painelStatus {
	st := painelStatus{
		Atualizado:    agoraLegivel(),
		Resumo:        map[string]int{},
		Execucao:      est.info(),
		StatusValidos: append([]string(nil), statusValidos...),
		Fases:         []painelFase{},
	}
	if abs, err := filepath.Abs(raiz); err == nil {
		st.Projeto = filepath.Base(abs)
	}
	if cfg, err := carregarConfig(raiz); err == nil {
		st.Plano = cfg.Plano
	}
	fases, err := carregarFases(caminhoCSV(raiz))
	if err != nil {
		st.Erro = err.Error()
		return st
	}
	for _, f := range fases {
		st.Resumo[f.Status]++
		st.Total++
		st.CustoTotal += f.CustoUSD
		st.Fases = append(st.Fases, painelFase{
			Fase: f.Fase, Titulo: f.Titulo, Status: f.Status,
			DependeDe: f.DependeDe, RequerHumano: f.RequerHumano,
			GateExtra: f.GateExtra, Modelo: f.Modelo, Tentativas: f.Tentativas,
			CustoUSD: f.CustoUSD, ConcluidoEm: f.ConcluidoEm, Observacao: f.Observacao,
		})
	}
	return st
}

// ipsLocais devolve os IPv4 privados das interfaces ativas, para acompanhar o
// painel de outro aparelho na mesma rede (celular/tablet).
func ipsLocais() []string {
	var ips []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		enderecos, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range enderecos {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipn.IP.To4()
			if ip4 == nil || !ip4.IsPrivate() {
				continue
			}
			ips = append(ips, ip4.String())
		}
	}
	return ips
}

// abrirNavegador abre a URL no navegador padrao (melhor-esforco).
func abrirNavegador(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// handlerLogs transmite (SSE) o log mais recente da pasta logs em tempo real:
// segue o arquivo enquanto ele cresce e troca sozinho quando surge um log mais
// novo (nova fase/gate). Somente leitura.
func handlerLogs(raiz string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming nao suportado", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		dir := dirLogs(raiz)
		ctx := r.Context()
		tick := time.NewTicker(600 * time.Millisecond)
		defer tick.Stop()

		var atual, pendente string
		var offset int64
		primeiro := true
		ocioso := 0

		fmt.Fprint(w, ": conectado\n\n")
		flusher.Flush()

		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}

			novo := logMaisRecente(dir)
			if novo == "" {
				if ocioso++; ocioso%25 == 0 {
					fmt.Fprint(w, ": ping\n\n")
					flusher.Flush()
				}
				continue
			}
			if novo != atual {
				atual, pendente, offset = novo, "", 0
				if primeiro { // no primeiro anexo, comeca perto do fim
					offset = deslocamentoInicial(atual)
				}
				primeiro = false
				escreverSSE(w, "arquivo", filepath.Base(atual))
				flusher.Flush()
			}

			f, err := os.Open(atual)
			if err != nil {
				continue
			}
			info, err := f.Stat()
			if err != nil {
				f.Close()
				continue
			}
			if info.Size() < offset { // truncado/rotacionado
				offset, pendente = 0, ""
			}
			enviou := false
			if info.Size() > offset {
				buf := make([]byte, info.Size()-offset)
				n, _ := f.ReadAt(buf, offset)
				offset += int64(n)
				pendente += string(buf[:n])
				for {
					i := strings.IndexByte(pendente, '\n')
					if i < 0 {
						break
					}
					linha := strings.TrimRight(pendente[:i], "\r")
					pendente = pendente[i+1:]
					if txt, ok := formatarLogLinha(atual, linha); ok {
						escreverSSE(w, "", txt)
						enviou = true
					}
				}
			}
			f.Close()
			if enviou {
				flusher.Flush()
				ocioso = 0
			} else if ocioso++; ocioso%25 == 0 {
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			}
		}
	}
}

// deslocamentoInicial devolve um offset perto do fim do arquivo (ultimos ~16
// KB, alinhado ao inicio da proxima linha) para nao despejar o historico
// inteiro ao abrir o painel.
func deslocamentoInicial(caminho string) int64 {
	const cauda = 16 * 1024
	info, err := os.Stat(caminho)
	if err != nil || info.Size() <= cauda {
		return 0
	}
	off := info.Size() - cauda
	f, err := os.Open(caminho)
	if err != nil {
		return off
	}
	defer f.Close()
	b := make([]byte, cauda)
	n, _ := f.ReadAt(b, off)
	if i := bytes.IndexByte(b[:n], '\n'); i >= 0 {
		off += int64(i) + 1
	}
	return off
}

// logMaisRecente devolve o caminho do log modificado mais recentemente em dir
// (.jsonl do claude ou .log dos gates).
func logMaisRecente(dir string) string {
	entradas, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var melhor string
	var melhorT time.Time
	for _, e := range entradas {
		if e.IsDir() {
			continue
		}
		nome := e.Name()
		if !strings.HasSuffix(nome, ".jsonl") && !strings.HasSuffix(nome, ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(melhorT) {
			melhorT = info.ModTime()
			melhor = filepath.Join(dir, nome)
		}
	}
	return melhor
}

// formatarLogLinha transforma uma linha de log no texto exibido no terminal do
// painel. Linhas .jsonl (stream-json do claude) viram texto legivel; .log dos
// gates saem como estao. O bool diz se a linha deve ser mostrada.
func formatarLogLinha(caminho, linha string) (string, bool) {
	if strings.TrimSpace(linha) == "" {
		return "", false
	}
	if !strings.HasSuffix(caminho, ".jsonl") {
		return linha, true
	}
	var tipo struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(linha), &tipo) != nil {
		return "", false
	}
	// Logs do Codex tambem sao .jsonl, mas usam outro esquema de eventos
	// (item.*/turn.*/error); roteia para o formatador especifico.
	if strings.HasPrefix(tipo.Type, "item.") || strings.HasPrefix(tipo.Type, "turn.") || tipo.Type == "error" {
		return formatarLogLinhaCodex(linha)
	}
	var ev eventoStream
	if json.Unmarshal([]byte(linha), &ev) != nil {
		return "", false
	}
	switch ev.Type {
	case "assistant":
		var partes []string
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "text":
				if t := strings.TrimSpace(c.Text); t != "" {
					partes = append(partes, t)
				}
			case "tool_use":
				partes = append(partes, "→ "+c.Name)
			}
		}
		if len(partes) == 0 {
			return "", false
		}
		return strings.Join(partes, "\n"), true
	case "result":
		if ev.TotalCostUSD > 0 {
			return fmt.Sprintf("── run: US$ %.2f · %d turnos ──", ev.TotalCostUSD, ev.NumTurns), true
		}
	}
	return "", false
}

// formatarLogLinhaCodex transforma uma linha do JSONL do Codex (esquema
// item.*/turn.*/error de `codex exec --json`) no texto exibido no terminal do
// painel, espelhando o que o motorCodex imprime no console.
func formatarLogLinhaCodex(linha string) (string, bool) {
	var ev eventoCodex
	if json.Unmarshal([]byte(linha), &ev) != nil {
		return "", false
	}
	switch ev.Type {
	case "item.started":
		if ev.Item.Type == "command_execution" && strings.TrimSpace(ev.Item.Command) != "" {
			return "→ " + primeirasLinhas(ev.Item.Command, 1), true
		}
	case "item.completed":
		if ev.Item.Type == "agent_message" {
			if t := strings.TrimSpace(ev.Item.Text); t != "" {
				return t, true
			}
		}
	case "turn.completed":
		if ev.Usage.InputTokens+ev.Usage.OutputTokens > 0 {
			return fmt.Sprintf("── turno: %d/%d tokens ──", ev.Usage.InputTokens, ev.Usage.OutputTokens), true
		}
	case "turn.failed", "error":
		if msg := textoErroCodex(ev.Error); msg != "" {
			return "⚠ " + primeirasLinhas(msg, 1), true
		}
	}
	return "", false
}

// escreverSSE emite um evento Server-Sent Events (nome opcional + dados, que
// podem ser multilinha).
func escreverSSE(w http.ResponseWriter, evento, dados string) {
	if evento != "" {
		fmt.Fprintf(w, "event: %s\n", evento)
	}
	for _, l := range strings.Split(dados, "\n") {
		fmt.Fprintf(w, "data: %s\n", l)
	}
	fmt.Fprint(w, "\n")
}

const paginaPainel = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Praxis — Painel</title>
<style>
  :root{
    --bg:#0f1420; --card:#171e2e; --card2:#1e2740; --line:#2a3550;
    --fg:#e6ebf5; --muted:#8b98b8; --accent:#5b8cff;
    --ok:#3ecf8e; --run:#5b8cff; --fail:#ff6b6b; --block:#ffcf5b; --wait:#c58bff; --pend:#6b7793;
  }
  *{box-sizing:border-box}
  body{margin:0;font-family:system-ui,Segoe UI,Roboto,Arial,sans-serif;background:var(--bg);color:var(--fg)}
  header{padding:20px 24px;border-bottom:1px solid var(--line);display:flex;flex-wrap:wrap;align-items:center;gap:12px}
  header h1{font-size:18px;margin:0;font-weight:650}
  header .sub{color:var(--muted);font-size:13px}
  .spacer{flex:1}
  .dot{width:9px;height:9px;border-radius:50%;background:var(--ok);display:inline-block;box-shadow:0 0 0 0 rgba(62,207,142,.6);animation:pulse 2s infinite}
  @keyframes pulse{0%{box-shadow:0 0 0 0 rgba(62,207,142,.5)}70%{box-shadow:0 0 0 8px rgba(62,207,142,0)}100%{box-shadow:0 0 0 0 rgba(62,207,142,0)}}
  main{padding:24px;max-width:1100px;margin:0 auto}
  .cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(120px,1fr));gap:12px;margin-bottom:18px}
  .card{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:14px 16px}
  .card .n{font-size:26px;font-weight:700}
  .card .l{color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.04em;margin-top:2px}
  .bar{height:10px;background:var(--card2);border-radius:6px;overflow:hidden;display:flex;margin-bottom:22px;border:1px solid var(--line)}
  .bar span{height:100%}
  table{width:100%;border-collapse:collapse;background:var(--card);border:1px solid var(--line);border-radius:12px;overflow:hidden}
  th,td{text-align:left;padding:11px 14px;border-bottom:1px solid var(--line);font-size:14px;vertical-align:top}
  th{color:var(--muted);font-size:11px;text-transform:uppercase;letter-spacing:.05em;background:var(--card2)}
  tr:last-child td{border-bottom:none}
  .badge{display:inline-flex;align-items:center;gap:6px;padding:3px 9px;border-radius:20px;font-size:12px;font-weight:600;white-space:nowrap}
  .b-concluida{background:rgba(62,207,142,.15);color:var(--ok)}
  .b-executando{background:rgba(91,140,255,.18);color:var(--run)}
  .b-falhou{background:rgba(255,107,107,.15);color:var(--fail)}
  .b-bloqueada{background:rgba(255,207,91,.15);color:var(--block)}
  .b-adiada{background:rgba(197,139,255,.15);color:var(--wait)}
  .b-pausada{background:rgba(197,139,255,.18);color:var(--wait)}
  .b-pendente{background:rgba(107,119,147,.18);color:var(--pend)}
  .fase-id{font-weight:700;color:var(--accent)}
  .muted{color:var(--muted)}
  .deps{font-size:12px;color:var(--muted)}
  .obs{font-size:12px;color:var(--muted);max-width:280px}
  .tag{font-size:11px;background:var(--card2);border:1px solid var(--line);border-radius:6px;padding:1px 6px;color:var(--muted)}
  .err{background:rgba(255,107,107,.12);border:1px solid var(--fail);color:#ffd0d0;padding:14px 16px;border-radius:10px}
  .banner{padding:14px 16px;border-radius:10px;margin-bottom:16px;border:1px solid var(--line)}
  .banner b{font-size:15px}
  .banner-motivo{margin-top:6px;font-size:13px;white-space:pre-wrap;word-break:break-word;font-family:ui-monospace,SFMono-Regular,Consolas,monospace}
  .banner-sub{margin-top:6px;font-size:12px;color:var(--muted)}
  .banner-ok{background:rgba(62,207,142,.12);border-color:var(--ok)}
  .banner-fail{background:rgba(255,107,107,.12);border-color:var(--fail);color:#ffd7d7}
  .banner-warn{background:rgba(255,207,91,.12);border-color:var(--block);color:#ffe9b0}
  .banner-off{background:rgba(139,152,184,.14);border-color:var(--muted);color:var(--fg)}
  .dot.off{background:var(--muted);animation:none;box-shadow:none}
  footer{color:var(--muted);font-size:12px;text-align:center;padding:18px}
  .term-wrap{margin-top:24px;background:#080b12;border:1px solid var(--line);border-radius:12px;overflow:hidden}
  .term-head{display:flex;align-items:center;gap:10px;padding:8px 14px;background:#0f1626;border-bottom:1px solid var(--line);font-size:12px;color:var(--muted)}
  .term-dots{display:inline-flex;gap:6px}
  .term-dots i{width:11px;height:11px;border-radius:50%}
  .term-dots i:nth-child(1){background:#ff5f56}.term-dots i:nth-child(2){background:#ffbd2e}.term-dots i:nth-child(3){background:#27c93f}
  .term-file{font-family:ui-monospace,SFMono-Regular,Consolas,monospace;color:var(--fg)}
  .term-follow{display:flex;align-items:center;gap:6px;cursor:pointer;user-select:none}
  .term{margin:0;padding:14px 16px;height:360px;overflow:auto;font-family:ui-monospace,SFMono-Regular,Consolas,monospace;font-size:12.5px;line-height:1.55;color:#cfe3ff;white-space:pre-wrap;word-break:break-word}
  .term div{padding:1px 0}
  .term .l-tool{color:#5b8cff}
  .term .l-cost{color:#3ecf8e}
  .term .l-sys{color:#8b98b8}
  .cfg-wrap{margin-top:24px;background:var(--card);border:1px solid var(--line);border-radius:12px;padding:16px}
  .cfg-head{display:flex;align-items:center;gap:12px;margin-bottom:12px}
  .cfg-head h2{font-size:15px;margin:0}
  .cfg-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:12px}
  .cfg-row{display:flex;align-items:center;justify-content:space-between;gap:10px;border-top:1px solid var(--line);padding:8px 0}
  .cfg-row:first-child{border-top:none}
  .cfg-row label{font-size:12px;color:var(--muted)}
  .cfg-row input,.cfg-row select{width:145px;background:var(--card2);color:var(--fg);border:1px solid var(--line);border-radius:6px;padding:6px}
  .cfg-row input[type=checkbox]{width:auto}
	.cfg-sub{margin-top:12px;margin-bottom:6px;color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.04em}
	.claude-aliases{display:flex;flex-direction:column;gap:8px;margin-top:6px}
	.claude-alias-row{display:grid;grid-template-columns:1fr 1.6fr auto;gap:8px;align-items:center}
	.claude-alias-row input{width:100%;background:var(--card2);color:var(--fg);border:1px solid var(--line);border-radius:6px;padding:6px}
	.claude-alias-row button{background:var(--card2);color:var(--fg);border:1px solid var(--line);border-radius:6px;padding:6px 9px;cursor:pointer}
	.claude-alias-head{display:grid;grid-template-columns:1fr 1.6fr auto;gap:8px;color:var(--muted);font-size:11px;text-transform:uppercase;letter-spacing:.04em}
	.cfg-mini{font-size:11px;color:var(--muted);margin-top:6px;line-height:1.4}
  .cfg-actions{display:flex;gap:10px;align-items:center;margin-top:12px}
  .cfg-actions button{background:var(--accent);color:white;border:0;border-radius:6px;padding:8px 12px;cursor:pointer}
  .cfg-actions button:disabled{background:var(--line);cursor:not-allowed}
  .cfg-note{font-size:12px;color:var(--muted)}
  .authbox{display:flex;align-items:center;gap:8px;font-size:12px;flex-wrap:wrap}
  .authbox input{width:170px;background:var(--card2);color:var(--fg);border:1px solid var(--line);border-radius:6px;padding:6px}
  .authbox button{background:var(--accent);color:#fff;border:0;border-radius:6px;padding:6px 10px;cursor:pointer}
  .authbox button.ghost{background:var(--card2);color:var(--fg);border:1px solid var(--line)}
  .authbox .who{color:var(--ok);display:inline-flex;align-items:center;gap:6px}
  .authbox .msg{color:var(--fail)}
  select.st{background:var(--card2);color:var(--fg);border:1px solid var(--line);border-radius:6px;padding:4px 6px;font-size:12px}
  @media(max-width:640px){.hide-sm{display:none}}
</style>
</head>
<body>
<header>
  <span class="dot" title="atualizando"></span>
  <div>
    <h1>Praxis <span id="proj" class="muted"></span></h1>
    <div class="sub" id="sub">carregando…</div>
  </div>
  <div class="spacer"></div>
  <div id="authbox" class="authbox"></div>
  <div class="sub" id="upd"></div>
</header>
<main>
  <div id="exec"></div>
  <div id="erro"></div>
  <div class="cards" id="cards"></div>
  <div class="bar" id="bar"></div>
  <table>
    <thead><tr>
      <th>Fase</th><th>Título</th><th>Status</th>
      <th class="hide-sm">Depende de</th><th class="hide-sm">Tent.</th>
      <th class="hide-sm">Custo</th><th class="hide-sm">Observação</th>
    </tr></thead>
    <tbody id="linhas"></tbody>
  </table>
  <section class="cfg-wrap">
    <div class="cfg-head"><h2>Configuração</h2><span id="cfg-state" class="cfg-note"></span></div>
    <div id="cfg"></div>
  </section>
  <section class="term-wrap">
    <div class="term-head">
      <span class="term-dots"><i></i><i></i><i></i></span>
      <span id="log-arq" class="term-file">logs</span>
      <span class="spacer"></span>
      <label class="term-follow"><input type="checkbox" id="seguir" checked> auto-scroll</label>
    </div>
    <div id="term" class="term"></div>
  </section>
</main>
<footer>Fases atualizam a cada 3 s · logs em tempo real (SSE) · Praxis</footer>
<script>
const ORDEM = ["executando","falhou","bloqueada","pausada","pendente","concluida","adiada"];
const ROTULO = {concluida:"Concluídas",executando:"Executando",falhou:"Falharam",bloqueada:"Bloqueadas",pausada:"Pausadas",adiada:"Adiadas",pendente:"Pendentes"};
const ICONE = {concluida:"✅",executando:"🔄",falhou:"❌",bloqueada:"⏸️",pausada:"⏯️",adiada:"⏭️",pendente:"⬜"};
function esc(s){return (s??"").replace(/[&<>"]/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c]));}
function badge(s){return '<span class="badge b-'+esc(s)+'">'+(ICONE[s]||"")+' '+esc(s)+'</span>';}
function getTok(){ return localStorage.getItem("praxis_token")||""; }
function setTok(t){ if(t) localStorage.setItem("praxis_token",t); else localStorage.removeItem("praxis_token"); }
function authHeaders(extra){ const h = Object.assign({}, extra||{}); const t=getTok(); if(t) h["X-Praxis-Token"]=t; return h; }
function renderAuth(msg){
  const el = document.getElementById("authbox");
  if(window.LOGGED){
    el.innerHTML = '<span class="who">🔓 autenticado</span><button class="ghost" onclick="sair()">Sair</button>';
  }else{
    el.innerHTML = '<input id="tok" type="password" placeholder="token do painel" onkeydown="if(event.key===&quot;Enter&quot;)entrar()">'+
      '<button onclick="entrar()">Entrar</button>'+(msg?'<span class="msg">'+esc(msg)+'</span>':'');
  }
}
async function entrar(){
  const t = (document.getElementById("tok").value||"").trim();
  if(!t){ renderAuth("informe o token"); return; }
  setTok(t);
  await carregarCfg();
  if(window.LOGGED){ tick(); } else { setTok(""); renderAuth("token invalido"); }
}
function sair(){ setTok(""); window.LOGGED=false; renderAuth(); carregarCfg(); tick(); }
async function tick(){
  try{
    const r = await fetch("/api/status",{cache:"no-store",headers:authHeaders()});
    const d = await r.json();
    setOffline(false);
    render(d);
  }catch(e){ setOffline(true); }
}
function setOffline(off){
  document.querySelector(".dot").classList.toggle("off", off);
  const el = document.getElementById("exec");
  if(off){
    document.getElementById("sub").textContent = "sem conexão com o Praxis…";
    el.innerHTML = '<div class="banner banner-off"><b>🔌 Sem conexão com o Praxis</b>'+
      '<div class="banner-sub">A aplicação foi encerrada ou está inacessível. Reabra o painel quando o Praxis voltar a rodar. Tentando reconectar…</div></div>';
    window.__offline = true;
  }else if(window.__offline){
    window.__offline = false; el.innerHTML = "";
  }
}
function renderExec(ex){
  const el = document.getElementById("exec");
  if(!ex || ex.ativa || !ex.situacao || ex.situacao==="executando"){ el.innerHTML=""; return; }
  const mapa = {
    concluido:    ["ok","✅","Execução concluída"],
    falhou:       ["fail","❌","Execução parou: uma fase falhou"],
    interrompido: ["warn","⛔","Execução interrompida"],
    pausado:      ["warn","⏯️","Execução pausada — retome com 'praxis executar'"],
    erro:         ["fail","💥","Execução abortada por erro interno"],
  };
  const m = mapa[ex.situacao] || ["warn","⚠","Execução encerrada"];
  el.innerHTML = '<div class="banner banner-'+m[0]+'"><b>'+m[1]+' '+m[2]+'</b>'+
    (ex.motivo? '<div class="banner-motivo">'+esc(ex.motivo)+'</div>':'')+
    '<div class="banner-sub">O painel continua no ar (somente leitura). O Praxis não está mais executando fases.</div></div>';
}
function render(d){
  window.__st = d;
  renderExec(d.execucao);
  document.getElementById("proj").textContent = d.projeto ? "· "+d.projeto : "";
  document.getElementById("upd").textContent = "atualizado " + (d.atualizado||"");
  const erro = document.getElementById("erro");
  erro.innerHTML = d.erro ? '<div class="err">'+esc(d.erro)+'</div>' : "";
  const sub = d.plano ? ("Plano: "+esc(d.plano)+" · "+d.total+" fases · custo US$ "+(d.custo_total||0).toFixed(2)) : "";
  document.getElementById("sub").innerHTML = sub;

  const res = d.resumo||{};
  const cards = [];
  for(const s of ORDEM){ if(res[s]) cards.push('<div class="card"><div class="n">'+res[s]+'</div><div class="l">'+ROTULO[s]+'</div></div>'); }
  document.getElementById("cards").innerHTML = cards.join("");

  const total = d.total||0;
  const bar = document.getElementById("bar");
  const cor = {concluida:"var(--ok)",executando:"var(--run)",falhou:"var(--fail)",bloqueada:"var(--block)",adiada:"var(--wait)",pausada:"var(--wait)",pendente:"var(--pend)"};
  bar.innerHTML = total ? ORDEM.map(s=>{const n=res[s]||0; return n? '<span style="width:'+(n/total*100)+'%;background:'+cor[s]+'"></span>':"";}).join("") : "";

  const linhas = (d.fases||[]).map(f=>{
    const deps = (f.depende_de&&f.depende_de.length)? f.depende_de.map(x=>'<span class="tag">'+esc(x)+'</span>').join(" ") : '<span class="muted">—</span>';
    const humano = f.requer_humano ? ' <span class="tag">humano</span>' : "";
    const custo = f.custo_usd ? "$"+f.custo_usd.toFixed(2) : '<span class="muted">—</span>';
    const obs = f.observacao ? esc(f.observacao) : "";
    return '<tr>'+
      '<td class="fase-id">'+esc(f.fase)+humano+'</td>'+
      '<td>'+esc(f.titulo)+'</td>'+
      '<td>'+statusCell(f, d.status_validos||[])+'</td>'+
      '<td class="hide-sm deps">'+deps+'</td>'+
      '<td class="hide-sm">'+(f.tentativas||0)+'</td>'+
      '<td class="hide-sm">'+custo+'</td>'+
      '<td class="hide-sm obs">'+obs+'</td>'+
    '</tr>';
  }).join("");
  document.getElementById("linhas").innerHTML = linhas || '<tr><td colspan="7" class="muted">sem fases</td></tr>';
}
function statusCell(f, validos){
  if(!window.LOGGED || !validos.length) return badge(f.status);
  const opts = validos.map(s=>'<option value="'+esc(s)+'" '+(s===f.status?'selected':'')+'>'+esc(s)+'</option>').join("");
  return '<select class="st" onchange="mudarStatus(&quot;'+esc(f.fase)+'&quot;,this.value)">'+opts+'</select>';
}
async function mudarStatus(fase, status){
  try{
    const r = await fetch("/api/fase-status",{method:"POST",headers:authHeaders({"Content-Type":"application/json"}),body:JSON.stringify({fase:fase,status:status})});
    if(!r.ok){ alert(await r.text()); tick(); return; }
    render(await r.json());
  }catch(e){ alert("falha ao atualizar status"); tick(); }
}
tick();
setInterval(tick, 3000);

let CFG = null;
function opt(v, cur){ return '<option value="'+esc(v)+'" '+(v===cur?'selected':'')+'>'+esc(v)+'</option>'; }
function dis(){ return CFG && CFG.editavel ? '' : 'disabled'; }
function row(label, html){ return '<div class="cfg-row"><label>'+esc(label)+'</label>'+html+'</div>'; }
function motoresComAliases(d){
	const base = (d.motores_disponiveis||[]).slice();
	const dirs = ((d.motores||{}).claude_config_dirs||{});
	for(const alias of Object.keys(dirs||{})){
		if(!base.includes(alias)) base.push(alias);
	}
	return base;
}
function aliasesClaudeOrdenados(d){
	const dirs = ((d.motores||{}).claude_config_dirs||{});
	const out = [];
	for(const alias of Object.keys(dirs||{})){
		const a = (alias||"").trim();
		const dir = (dirs[alias]||"").trim();
		if(!a || !dir || a === "claude") continue;
		out.push({alias:a, dir:dir});
	}
	out.sort((x,y)=>x.alias.localeCompare(y.alias));
	return out;
}
function linhaAliasClaude(alias, dir){
	const travado = dis();
	const rm = (CFG && CFG.editavel)
		? '<button type="button" onclick="this.closest(\'.claude-alias-row\').remove()">remover</button>'
		: '<button type="button" disabled>remover</button>';
	return '<div class="claude-alias-row">'+
		'<input data-role="alias" placeholder="ex.: claude_alt" value="'+esc(alias||"")+'" '+travado+'>'+
		'<input data-role="dir" placeholder="C:/Users/voce/.claude-alt" value="'+esc(dir||"")+'" '+travado+'>'+
		rm+
	'</div>';
}
function renderAliasClaudeUI(d){
	const host = document.getElementById("cfg-claude-aliases");
	if(!host) return;
	const itens = aliasesClaudeOrdenados(d);
	let h = '<div class="claude-alias-head"><span>Alias</span><span>CLAUDE_CONFIG_DIR</span><span>Ações</span></div>';
	h += '<div class="claude-aliases" id="cfg-claude-alias-list">';
	if(!itens.length){
		h += linhaAliasClaude("", "");
	}else{
		for(const it of itens) h += linhaAliasClaude(it.alias, it.dir);
	}
	h += '</div>';
	if(CFG && CFG.editavel){
		h += '<div style="margin-top:8px"><button type="button" onclick="addAliasClaude()">+ adicionar conta Claude</button></div>';
	}
	h += '<div class="cfg-mini">Use aliases para contas extras (ex.: claude_alt). Depois você pode escolher o alias nas operações e no fallback.</div>';
	host.innerHTML = h;
}
function addAliasClaude(){
	const el = document.getElementById("cfg-claude-alias-list");
	if(!el || !(CFG && CFG.editavel)) return;
	el.insertAdjacentHTML("beforeend", linhaAliasClaude("", ""));
}
function coletarAliasesClaude(){
	const host = document.getElementById("cfg-claude-alias-list");
	const out = {};
	if(!host) return out;
	const rows = host.querySelectorAll(".claude-alias-row");
	for(const row of rows){
		const aliasEl = row.querySelector('input[data-role="alias"]');
		const dirEl = row.querySelector('input[data-role="dir"]');
		const alias = ((aliasEl && aliasEl.value) || "").trim().toLowerCase();
		const dir = ((dirEl && dirEl.value) || "").trim();
		if(!alias && !dir) continue;
		if(!alias || !dir){
			document.getElementById("cfg-msg").textContent = "preencha alias e diretório em todas as contas Claude";
			return null;
		}
		if(alias === "claude"){
			document.getElementById("cfg-msg").textContent = "o alias 'claude' já é reservado para a conta padrão";
			return null;
		}
		if(!/^[a-z][a-z0-9_-]*$/.test(alias)){
			document.getElementById("cfg-msg").textContent = "alias inválido: use letras, números, _ ou -, começando com letra";
			return null;
		}
		out[alias] = dir;
	}
	return out;
}
async function carregarCfg(){
  try{
    const r = await fetch("/api/config",{cache:"no-store",headers:authHeaders()});
    CFG = await r.json();
    window.LOGGED = !!CFG.editavel;
    renderAuth();
    renderCfg();
    if(window.__st) render(window.__st);
  }catch(e){
    document.getElementById("cfg").innerHTML = '<div class="err">Falha ao carregar configuração</div>';
  }
}
function renderCfg(){
  const d = CFG;
	const motoresBase = d.motores_disponiveis || [];
	const motores = motoresComAliases(d);
  const ops = d.operacoes_validas || [];
  const evs = d.eventos_conhecidos || [];
  document.getElementById("cfg-state").textContent = d.editavel ? "editável" : "somente leitura";
  let h = '<div class="cfg-grid">';
  h += '<div><b>Motores</b>';
  for(const op of ops){
    const cur = (d.motores.operacoes||{})[op] || "claude";
    h += row(op, '<select id="cfg-op-'+op+'" '+dis()+'>'+motores.map(m=>opt(m,cur)).join("")+'</select>');
  }
	for(const m of motoresBase){
    const val = ((d.motores.modelos||{})[m] || "");
    h += row("modelo "+m, '<input id="cfg-model-'+m+'" value="'+esc(val)+'" '+dis()+'>');
  }
	for(const m of motoresBase){
    const cur = ((d.motores.esforcos||{})[m] || "");
    const esforcos = m==="claude" ? ["","low","medium","high","xhigh","max"] : ["","low","medium","high"];
    h += row("esforco "+m, '<select id="cfg-effort-'+m+'" '+dis()+'>'+esforcos.map(e=>opt(e,cur)).join("")+'</select>');
  }
	h += '<div class="cfg-sub">Contas Claude</div><div id="cfg-claude-aliases"></div>';
  const ordem = ((d.motores.fallback||{}).ordem||[]).join(",");
  h += row("fallback", '<input type="checkbox" id="cfg-fb" '+((d.motores.fallback||{}).ativo?'checked':'')+' '+dis()+'>');
  h += row("ordem", '<input id="cfg-fb-ordem" value="'+esc(ordem)+'" '+dis()+'>');
	h += '<div class="cfg-mini">ordem aceita motores base e aliases Claude separados por vírgula (ex.: claude,claude_alt,codex)</div>';
  h += '</div><div><b>Notificações</b>';
  const canais = d.notificacoes.canais || {};
  h += canalUI("telegram", canais.telegram || {}, ["bot_token","chat_id"]);
  h += canalUI("discord", canais.discord || {}, ["webhook_url"]);
  h += canalUI("slack", canais.slack || {}, ["webhook_url"]);
  h += canalUI("google_chat", canais.google_chat || {}, ["webhook_url"]);
  h += canalUI("webhook", canais.webhook || {}, ["url","header","template"]);
  h += '</div><div><b>Eventos</b>';
  for(const ev of evs){
    h += row(ev, '<input type="checkbox" id="cfg-ev-'+ev+'" '+((d.notificacoes.eventos||{})[ev]?'checked':'')+' '+dis()+'>');
  }
  h += '</div><div><b>Painel</b>';
  h += row("bind", '<input id="cfg-bind" value="'+esc((d.painel||{}).bind||"")+'" '+dis()+'>');
  h += '</div></div><div class="cfg-actions"><button id="cfg-save" '+dis()+' onclick="salvarCfg()">Salvar</button><span id="cfg-msg" class="cfg-note"></span></div>';
  document.getElementById("cfg").innerHTML = h;
	renderAliasClaudeUI(d);
}
function canalUI(nome, c, campos){
  let h = '<div style="margin-top:10px"><span class="tag">'+esc(nome)+'</span>';
  h += row(nome+" ativo", '<input type="checkbox" id="cfg-ch-'+nome+'-ativo" '+(c.ativo?'checked':'')+' '+dis()+'>');
  for(const campo of campos){
    h += row(campo, '<input id="cfg-ch-'+nome+'-'+campo+'" value="'+esc(c[campo]||"")+'" '+dis()+'>');
  }
  return h + '</div>';
}
function val(id){ const el=document.getElementById(id); return el ? el.value : ""; }
function chk(id){ const el=document.getElementById(id); return !!(el && el.checked); }
async function salvarCfg(){
  const d = CFG;
	const aliasDirs = coletarAliasesClaude();
	if(aliasDirs === null) return;
	const motores = {operacoes:{}, modelos:{}, esforcos:{}, claude_config_dirs:aliasDirs, fallback:{ativo:chk("cfg-fb"), ordem:val("cfg-fb-ordem").split(",").map(s=>s.trim()).filter(Boolean)}};
  for(const op of d.operacoes_validas||[]) motores.operacoes[op] = val("cfg-op-"+op);
  for(const m of d.motores_disponiveis||[]) motores.modelos[m] = val("cfg-model-"+m);
  for(const m of d.motores_disponiveis||[]) motores.esforcos[m] = val("cfg-effort-"+m);
  const canais = {
    telegram:{ativo:chk("cfg-ch-telegram-ativo"), bot_token:val("cfg-ch-telegram-bot_token"), chat_id:val("cfg-ch-telegram-chat_id")},
    discord:{ativo:chk("cfg-ch-discord-ativo"), webhook_url:val("cfg-ch-discord-webhook_url")},
    slack:{ativo:chk("cfg-ch-slack-ativo"), webhook_url:val("cfg-ch-slack-webhook_url")},
    google_chat:{ativo:chk("cfg-ch-google_chat-ativo"), webhook_url:val("cfg-ch-google_chat-webhook_url")},
    webhook:{ativo:chk("cfg-ch-webhook-ativo"), url:val("cfg-ch-webhook-url"), header:val("cfg-ch-webhook-header"), template:val("cfg-ch-webhook-template")}
  };
  const eventos = {};
  for(const ev of d.eventos_conhecidos||[]) eventos[ev] = chk("cfg-ev-"+ev);
  const payload = {motores:motores, notificacoes:{canais:canais,eventos:eventos}, painel:{bind:val("cfg-bind")}};
  document.getElementById("cfg-msg").textContent = "salvando...";
  const r = await fetch("/api/config",{method:"POST",headers:authHeaders({"Content-Type":"application/json"}),body:JSON.stringify(payload)});
  if(!r.ok){ document.getElementById("cfg-msg").textContent = await r.text(); return; }
  CFG = await r.json();
  document.getElementById("cfg-msg").textContent = "salvo";
  renderCfg();
}
renderAuth();
carregarCfg();

// --- terminal de logs ao vivo (SSE) ---
const termEl = document.getElementById("term");
const seguirEl = document.getElementById("seguir");
const arqEl = document.getElementById("log-arq");
function addLinha(txt, cls){
  const div = document.createElement("div");
  if(cls) div.className = cls;
  else if(txt.startsWith("→")) div.className = "l-tool";
  else if(txt.startsWith("──")) div.className = "l-cost";
  div.textContent = txt;
  termEl.appendChild(div);
  while(termEl.childNodes.length > 800) termEl.removeChild(termEl.firstChild);
  if(seguirEl.checked) termEl.scrollTop = termEl.scrollHeight;
}
function conectarLogs(){
  const es = new EventSource("/api/logs");
  es.addEventListener("arquivo", e => { arqEl.textContent = e.data; addLinha("──── "+e.data+" ────", "l-sys"); });
  es.onmessage = e => addLinha(e.data);
  // EventSource reconecta sozinho em caso de queda
}
conectarLogs();
</script>
</body>
</html>`
