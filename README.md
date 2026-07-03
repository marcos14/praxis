# Praxis — orquestrador de fases para Claude Code

Ferramenta em **Go** que executa as fases de um plano (`.md`) uma a uma, cada
fase numa execução independente do `claude -p` (**contexto limpo**), com:

```
executor → gates determinísticos (build/lint/test) → corretor (se falhar)
        → revisor (contexto limpo, veredito JSON)   → commit local (sem push)
```

O conhecimento entre as fases não fica em memória de sessão nenhuma: fica no
**próprio plano** (seção *Registro de Andamento*), que cada execução lê e
atualiza. O controle da fila fica em `fases.csv` (editável no Excel).

**Pré-requisitos na máquina que vai rodar:** [Go](https://go.dev) (só para
compilar), `claude` (Claude Code CLI) **logado**, `git`, e os toolchains dos
gates do seu projeto (ex.: `go`, `npm`, `python`) no PATH.

> O arquivo de configuração continua se chamando `autopilot.json` (para não
> quebrar projetos já inicializados). O que mudou foi o nome da ferramenta e do
> binário: **Praxis / `praxis.exe`**.

---

## 1. Compilar

```powershell
cd praxis             # pasta com o código-fonte
go build -o praxis.exe .
go test ./...         # opcional: testes unitários
```

Isso gera o `praxis.exe` (autossuficiente: os prompts padrão vão **embutidos**
no binário — não precisa carregar mais nada junto).

Para compilar para outra plataforma (ex.: um servidor Linux):

```powershell
$env:GOOS='linux'; go build -o praxis .; Remove-Item env:GOOS
```

## 2. Instalar num projeto novo

1. **Copie apenas o `praxis.exe`** para a pasta `automacao\` do projeto que vai
   receber o plano (crie a pasta):

   ```powershell
   mkdir C:\projetos\meu_projeto\automacao
   copy praxis.exe C:\projetos\meu_projeto\automacao\
   ```

   (Opcional: copie também a pasta `defaults\` renomeada para `prompts\` se
   quiser personalizar os prompts antes do primeiro uso — senão o
   `inicializar` cria `automacao\prompts\` sozinho com os padrões.)

2. **Tenha um plano** `.md` na raiz do projeto (ex.: `PLANO.md`) descrevendo o
   que precisa ser feito. Pode ser um plano "cru" — o próximo passo quebra ele
   em fases do tamanho certo.

O `.gitignore` do projeto **é gerenciado pelo `inicializar`** (bloco entre
marcadores `# >>> praxis ... >>>`), conforme sua resposta sobre versionar o
estado — veja abaixo. Não precisa editar à mão.

## 3. Inicializar (uma vez por projeto)

Na **raiz do projeto** (não dentro de `automacao\`):

```powershell
.\automacao\praxis.exe inicializar
```

Ele pergunta:
- **Caminho do plano** (.md) — ex.: `PLANO.md`;
- **Diretórios adicionais** que o Claude pode editar (outros repositórios) —
  separados por vírgula, vazio se nenhum;
- **Modelo** para executar as fases (padrão: `opus`);
- **Versionar o estado do Praxis** (`automacao/fases.csv`) no git? (padrão:
  **sim**) — veja *Versionar o estado* abaixo.

Então roda o Claude, que: lê o plano inteiro, **quebra em micro-fases** (cada
uma executável numa única run do Opus, com testes próprios e dependências
explícitas), **edita o próprio .md** com a estrutura de fases + Registro de
Andamento, e detecta os **gates** (comandos de build/lint/test da stack). No
final você tem:

| Arquivo gerado | Papel |
|---|---|
| `automacao/fases.csv` | Fila de fases (edite no Excel: dependências, `requer_humano`) |
| `automacao/autopilot.json` | Config: plano, modelo, `add_dirs`, budget/timeout por run, **gates**, `versionar_automacao` |
| `automacao/prompts/*.md` | Prompts personalizáveis (executor/corretor/revisor/inicializador) |
| `automacao/logs/` | Logs de cada execução |

**Revise `fases.csv` e `autopilot.json` antes de executar** — confira se os
gates fazem sentido e se as fases com hardware físico/aprovação externa estão
com `requer_humano=sim`.

Modo não-interativo: `inicializar --plano PLANO.md --add-dirs "C:\projetos\outro_repo" --modelo opus --versionar sim`.

## Versionar o estado (`versionar_automacao`)

O `fases.csv` é a memória da fila. Escolha na inicialização se ele entra no git:

- **`sim` (padrão, recomendado):** `fases.csv`, `autopilot.json` e os prompts
  ficam **versionados**. O status de conclusão de uma fase é gravado *depois*
  do commit da fase, então o Praxis faz um **commit de bookkeeping por fase**
  (`chore(praxis): estado apos Fase N [concluida]`). Assim o progresso fica no
  histórico e a árvore volta limpa — em projetos grandes você não perde de vista
  o que já rodou. O `.gitignore` gerenciado ignora só o transitório
  (`logs/`, `*.exe`, `fases-*.bak.csv`).
- **`nao`:** a pasta `automacao/` **inteira** entra no `.gitignore` e para de
  ser rastreada (`git rm --cached`). O estado vira puramente local — o registro
  canônico de progresso passa a ser só o *Registro de Andamento* do plano.

Independentemente da escolha, a **pré-checagem de árvore limpa ignora tudo sob
`automacao/`**: churn de bookkeeping do próprio Praxis nunca bloqueia a próxima
fase (só o trabalho do usuário bloqueia).

## 4. Acompanhar — até onde foi, o que falta

```powershell
.\automacao\praxis.exe status
```

```
FASE  STATUS        DEPENDE DE  HUMANO  CUSTO   CONCLUIDA EM      TITULO
2c    ✅ concluida  2+3b                $12.30  2026-07-02 15:10  Tabelas de preco...
2d    ⬜ pendente   1                                             Campos Visiveis...
4     ⏸️ bloqueada  2f+3e       sim                               Sync delta...
```

- **Fonte da verdade da fila:** `automacao/fases.csv` (pode editar no Excel —
  ex.: voltar uma fase `falhou` para `pendente` depois de resolver a causa).
- **O que foi feito em cada fase:** seção *Registro de Andamento* do plano `.md`
  + o commit `Fase N: ... [praxis]` de cada fase.
- **Detalhe de cada run:** `automacao/logs/fase-*-executor-*.jsonl` (conversa
  completa), `fase-*-gates-*.log` (saída dos testes) e `RESUMO-*.md` (rodada).

### Painel web (microsite)

Para acompanhar visualmente — inclusive do celular/tablet na mesma rede — suba
o painel, que lê o `fases.csv` **ao vivo** e mostra as fases, seus status, a
barra de progresso e o custo:

```powershell
.\automacao\praxis.exe painel                 # porta 7799, abre o navegador
.\automacao\praxis.exe painel --porta 8080     # outra porta
.\automacao\praxis.exe painel --abrir nao      # não abrir o navegador sozinho
```

Ao subir, ele imprime a URL local e também a **URL com o IP da rede local** —
basta abrir esse endereço no celular/tablet para acompanhar de longe:

```
📊 Painel de acompanhamento:
   http://localhost:7799
   http://192.168.0.42:7799  (na rede local)
```

O painel:

- Atualiza sozinho a cada **3 s** (lê o `fases.csv` a cada requisição, então
  reflete o andamento em tempo real enquanto o `executar` roda).
- Mostra cartões de resumo por status, barra de progresso, custo total e uma
  tabela com fase, título, status (com ícones), dependências, tentativas, custo
  e observação — as mesmas informações do CSV.
- Não tem dependências externas nem grava nada: é **somente leitura**. Encerre
  com **Ctrl+C**.

Para subir o painel **junto** com a execução (abre o navegador e vai
atualizando conforme as fases avançam), use `executar --painel` — veja abaixo.

## 5. Executar

```powershell
.\automacao\praxis.exe executar          # tudo que estiver pronto, em sequência
.\automacao\praxis.exe executar 2d       # só a fase 2d
.\automacao\praxis.exe executar 2d,2e    # lote, em ordem
.\automacao\praxis.exe executar --forcar 3c   # ignora checagem de dependências
.\automacao\praxis.exe executar --painel      # sobe o painel web e abre o navegador
```

- Exige **árvore git limpa** em todos os repositórios envolvidos (commite ou
  guarde seu WIP antes) — é a garantia de que cada commit contém só a fase. Os
  arquivos do próprio Praxis (`automacao/`) são desconsiderados nessa checagem.
- No modo sequência, para sozinho quando: a fila acaba, uma fase **falha**
  (gates vermelhos após as correções, ou revisor reprova 2x) ou só restam fases
  `requer_humano`/bloqueadas. Ao parar: banner + toast do Windows +
  `logs/RESUMO-*.md`.
- Cada fase concluída vira **um commit local** em cada repositório tocado.
  **Nunca faz `git push`** — revisar e subir é seu.
- `--painel` (opcional, com `--porta <n>`) sobe o painel web em segundo plano e
  abre o navegador antes de começar, atualizando conforme as fases avançam.
- Fase que falhou: veja o motivo em `status`/`observacao` e nos logs, corrija
  (ou não) manualmente, e rode `executar <fase>` de novo — reexecutar uma fase
  explícita é permitido para status `falhou`.

## Arquivos por projeto (referência)

| Arquivo | Papel |
|---|---|
| `<plano>.md` | Plano canônico: fases com checkboxes, "Depende de:" e Registro de Andamento (memória entre fases) |
| `automacao/autopilot.json` | Config: plano, modelo, `add_dirs` (outros repos), budget/timeout, gates, `versionar_automacao` |
| `automacao/fases.csv` | Fila (`;`): `fase;titulo;status;depende_de;requer_humano;gate_extra;modelo;tentativas;custo_usd;concluido_em;observacao` |
| `automacao/prompts/*.md` | Prompts personalizáveis; se apagados, valem os embutidos no binário |
| `automacao/logs/` | `.jsonl` por run do claude, log dos gates, `RESUMO-*.md` por rodada |

Estados no CSV: `pendente` · `executando` · `concluida` · `falhou` ·
`bloqueada` (requer humano) · `adiada`. Dependências: `2f+3e`. Fases com
hardware físico/aprovação externa: `requer_humano=sim` — o runner nunca as
executa.

`.\automacao\praxis.exe ajuda` imprime esta referência no terminal.

## Evoluindo o código

Um único pacote Go, sem dependências externas:

| Arquivo | Responsabilidade |
|---|---|
| `main.go` | CLI e ajuda |
| `executar.go` | pipeline por fase (o coração) |
| `inicializar.go` | quebra do plano em micro-fases via Claude |
| `claude.go` | invocação headless do `claude -p` (stream-json, `--json-schema`) |
| `gates.go` | gates determinísticos |
| `fases.go` / `config.go` | fila CSV e configuração |
| `status.go` / `painel.go` | acompanhamento no terminal e painel web |
| `git.go` / `notificar.go` / `prompts.go` | apoio |
| `defaults/*.md` | prompts padrão (embutidos no binário via `go:embed`) |

Segurança embutida: pré-checagem de árvore limpa (nunca faz reset/stash);
executor/corretor proibidos de `git commit/push`; revisor somente-leitura;
orçamento (`max_budget_usd`) e timeout por run; gates rodados pelo orquestrador,
nunca autorrelatados pelo modelo.
