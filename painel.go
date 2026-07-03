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
	"time"
)

const portaPainelPadrao = 7799

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

	ln, err := escutarPainel(*porta)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://localhost:%d", ln.Addr().(*net.TCPAddr).Port)
	imprimirEnderecos(ln.Addr().(*net.TCPAddr).Port)
	if ehSim(*abrirFlag) {
		abrirNavegador(url)
	}
	fmt.Println("\nCtrl+C para encerrar o painel.")
	return http.Serve(ln, handlerPainel(raiz))
}

// iniciarPainel sobe o painel em segundo plano (usado pelo `executar --painel`).
// Devolve a URL local; nunca aborta a execucao — falhas sao apenas avisadas.
func iniciarPainel(raiz string, porta int, abrir bool) string {
	ln, err := escutarPainel(porta)
	if err != nil {
		fmt.Printf("AVISO: nao consegui subir o painel: %v\n", err)
		return ""
	}
	url := fmt.Sprintf("http://localhost:%d", ln.Addr().(*net.TCPAddr).Port)
	imprimirEnderecos(ln.Addr().(*net.TCPAddr).Port)
	go func() { _ = http.Serve(ln, handlerPainel(raiz)) }()
	if abrir {
		abrirNavegador(url)
	}
	return url
}

func escutarPainel(porta int) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", porta))
	if err != nil {
		return nil, fmt.Errorf("nao consegui abrir a porta %d (ja em uso?): %w", porta, err)
	}
	return ln, nil
}

func imprimirEnderecos(porta int) {
	fmt.Printf("\n📊 Painel de acompanhamento:\n")
	fmt.Printf("   http://localhost:%d\n", porta)
	for _, ip := range ipsLocais() {
		fmt.Printf("   http://%s:%d  (na rede local)\n", ip, porta)
	}
}

func handlerPainel(raiz string) http.Handler {
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
		resp := montarStatus(raiz)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/api/logs", handlerLogs(raiz))
	return mux
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
	Projeto    string         `json:"projeto"`
	Plano      string         `json:"plano"`
	Atualizado string         `json:"atualizado"`
	Erro       string         `json:"erro,omitempty"`
	Resumo     map[string]int `json:"resumo"`
	Total      int            `json:"total"`
	CustoTotal float64        `json:"custo_total"`
	Fases      []painelFase   `json:"fases"`
}

func montarStatus(raiz string) painelStatus {
	st := painelStatus{
		Atualizado: agoraLegivel(),
		Resumo:     map[string]int{},
		Fases:      []painelFase{},
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
  .b-pendente{background:rgba(107,119,147,.18);color:var(--pend)}
  .fase-id{font-weight:700;color:var(--accent)}
  .muted{color:var(--muted)}
  .deps{font-size:12px;color:var(--muted)}
  .obs{font-size:12px;color:var(--muted);max-width:280px}
  .tag{font-size:11px;background:var(--card2);border:1px solid var(--line);border-radius:6px;padding:1px 6px;color:var(--muted)}
  .err{background:rgba(255,107,107,.12);border:1px solid var(--fail);color:#ffd0d0;padding:14px 16px;border-radius:10px}
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
  <div class="sub" id="upd"></div>
</header>
<main>
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
const ORDEM = ["executando","falhou","bloqueada","pendente","concluida","adiada"];
const ROTULO = {concluida:"Concluídas",executando:"Executando",falhou:"Falharam",bloqueada:"Bloqueadas",adiada:"Adiadas",pendente:"Pendentes"};
const ICONE = {concluida:"✅",executando:"🔄",falhou:"❌",bloqueada:"⏸️",adiada:"⏭️",pendente:"⬜"};
function esc(s){return (s??"").replace(/[&<>"]/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c]));}
function badge(s){return '<span class="badge b-'+esc(s)+'">'+(ICONE[s]||"")+' '+esc(s)+'</span>';}
async function tick(){
  try{
    const r = await fetch("/api/status",{cache:"no-store"});
    const d = await r.json();
    render(d);
  }catch(e){ document.getElementById("sub").textContent = "sem conexão com o servidor…"; }
}
function render(d){
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
  const cor = {concluida:"var(--ok)",executando:"var(--run)",falhou:"var(--fail)",bloqueada:"var(--block)",adiada:"var(--wait)",pendente:"var(--pend)"};
  bar.innerHTML = total ? ORDEM.map(s=>{const n=res[s]||0; return n? '<span style="width:'+(n/total*100)+'%;background:'+cor[s]+'"></span>':"";}).join("") : "";

  const linhas = (d.fases||[]).map(f=>{
    const deps = (f.depende_de&&f.depende_de.length)? f.depende_de.map(x=>'<span class="tag">'+esc(x)+'</span>').join(" ") : '<span class="muted">—</span>';
    const humano = f.requer_humano ? ' <span class="tag">humano</span>' : "";
    const custo = f.custo_usd ? "$"+f.custo_usd.toFixed(2) : '<span class="muted">—</span>';
    const obs = f.observacao ? esc(f.observacao) : "";
    return '<tr>'+
      '<td class="fase-id">'+esc(f.fase)+humano+'</td>'+
      '<td>'+esc(f.titulo)+'</td>'+
      '<td>'+badge(f.status)+'</td>'+
      '<td class="hide-sm deps">'+deps+'</td>'+
      '<td class="hide-sm">'+(f.tentativas||0)+'</td>'+
      '<td class="hide-sm">'+custo+'</td>'+
      '<td class="hide-sm obs">'+obs+'</td>'+
    '</tr>';
  }).join("");
  document.getElementById("linhas").innerHTML = linhas || '<tr><td colspan="7" class="muted">sem fases</td></tr>';
}
tick();
setInterval(tick, 3000);

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
