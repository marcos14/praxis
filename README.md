# Praxis

Praxis e um orquestrador de fases para planos de desenvolvimento. Ele executa
uma fila em `automacao/fases.csv`, roda cada etapa em um harness de codigo
headless, valida com gates deterministicos e faz commits locais por fase.

Fluxo por fase:

```text
executor -> gates (build/lint/test) -> corretor (se falhar)
         -> revisor (veredito JSON) -> atualizacao do plano -> commit local
```

O conhecimento entre fases fica no proprio plano Markdown, na secao
`Registro de Andamento`. Cada run recebe contexto limpo e reler arquivos e
configuracao do disco.

## Requisitos

- `git`
- Toolchains dos gates do projeto (`go`, `npm`, `python`, `dotnet`, etc.)
- Pelo menos um motor configurado e logado:
  - `claude` para Claude Code
  - `codex` para OpenAI Codex CLI
- Go apenas para compilar o Praxis.

## Compilar

```powershell
go build -o praxis.exe .
go test ./...
```

## Inicializar um projeto

Copie o binario para `automacao/` do projeto e rode na raiz:

```powershell
.\automacao\praxis.exe inicializar
```

No modo interativo, o Praxis lista os harnesses encontrados no `PATH` (`claude`,
`codex`) e pergunta qual usar na inicializacao. O modelo fica no default do
harness; depois voce pode editar `motores.modelos` no `autopilot.json`.

O comando cria ou atualiza:

| Arquivo | Papel |
|---|---|
| `automacao/fases.csv` | fila de fases |
| `automacao/autopilot.json` | configuracao unica do Praxis |
| `automacao/autopilot.exemplo.json` | exemplo sem segredos |
| `automacao/prompts/*.md` | prompts editaveis |
| `automacao/logs/` | logs JSONL e logs dos gates |

`autopilot.json` e a fonte de verdade. Ele e relido antes de cada operacao e
antes de cada notificacao, entao editar o arquivo ou salvar pelo painel passa a
valer no proximo passo sem reiniciar.

## Configuracao unificada

Exemplo resumido de `automacao/autopilot.json`:

```json
{
  "plano": "PLANO.md",
  "add_dirs": [],
  "max_budget_usd": 25,
  "timeout_min": 120,
  "max_correcoes": 2,
  "max_ciclos_revisao": 1,
  "versionar_automacao": true,
  "gates": [],
  "gates_extra": [],
  "motores": {
    "operacoes": {
      "planejar": "claude",
      "executar": "codex",
      "corrigir": "codex",
      "revisar": "claude"
    },
    "modelos": {
      "claude": "opus",
      "codex": "gpt-5.5"
    },
    "esforcos": {
      "claude": "high",
      "codex": "high"
    },
    "fallback": {
      "ativo": true,
      "ordem": ["claude", "codex"]
    }
  },
  "notificacoes": {
    "canais": {
      "telegram": {"ativo": false, "bot_token": "", "chat_id": ""},
      "discord": {"ativo": false, "webhook_url": ""},
      "slack": {"ativo": false, "webhook_url": ""},
      "google_chat": {"ativo": false, "webhook_url": ""},
      "webhook": {"ativo": false, "url": "", "header": "", "template": ""}
    },
    "eventos": {
      "inicializacao_concluida": true,
      "planejamento_iniciado": false,
      "fase_iniciada": false,
      "gates_falharam": false,
      "correcao_iniciada": false,
      "revisor_reprovou": false,
      "troca_de_harness": true,
      "franquia_esgotada": true,
      "fase_concluida": true,
      "marco_concluido": true,
      "rodada_concluida": true,
      "rodada_parou": true,
      "pausa": false,
      "erro_interno": true
    }
  },
  "painel": {
    "auth_ativo": false,
    "credencial_base64": "",
    "bind": ""
  }
}
```

Campos legados `modelo` e `motor`, se existirem, ainda sao usados como default
quando os blocos novos nao definem valor.
`motores.esforcos` define o nivel de raciocinio por motor; por padrao Claude e
Codex usam `high`.
Durante a inicializacao, `--motor codex` configura todas as operacoes para
Codex; depois voce pode separar por funcao em `motores.operacoes`.

## Motores e fallback

`motores.operacoes` escolhe o harness por etapa:

- `planejar`: usado por `inicializar`
- `executar`: executor e atualizacao do plano
- `corrigir`: corretor apos gates/revisor
- `revisar`: revisor somente leitura

Exemplo comum:

```json
"operacoes": {
  "planejar": "claude",
  "executar": "codex",
  "corrigir": "codex",
  "revisar": "claude"
}
```

Se `motores.fallback.ativo=true`, quando o motor atual sinaliza limite de uso o
Praxis tenta o proximo motor em `fallback.ordem` que ainda nao esgotou na
rodada. Se nao houver outro motor, preserva o comportamento anterior: espera o
reset da franquia, aceita Enter para pausar e Ctrl+C para interromper.

Codex nao reporta custo em USD nativo; o Praxis estima por tokens e aplica
`max_budget_usd` como teto soft entre runs.

## Executar

```powershell
.\automacao\praxis.exe executar
.\automacao\praxis.exe executar 2d
.\automacao\praxis.exe executar 2d,2e
.\automacao\praxis.exe executar --painel
```

Praxis exige arvore git limpa fora de `automacao/`. Cada fase concluida vira um
commit local. Ele nunca faz `git push`.

## Painel

```powershell
.\automacao\praxis.exe painel
.\automacao\praxis.exe painel --porta 8080 --abrir nao
```

O painel mostra status, progresso e logs ao vivo. Ele tambem edita os blocos
`motores`, `notificacoes` e `painel` do `autopilot.json` via `GET/POST
/api/config`.

Edicao de configuracao exige Basic Auth ativo. Gere a credencial:

```powershell
.\automacao\praxis.exe auth
```

Cole em:

```json
"painel": {
  "auth_ativo": true,
  "credencial_base64": "BASE64_DE_USUARIO_SENHA",
  "bind": "127.0.0.1"
}
```

Quando o painel esta sem auth, a tela de configuracao fica somente leitura e
`POST /api/config` retorna erro.

## Notificacoes

Todos os canais e eventos ficam em `autopilot.json`. O arquivo legado
`automacao/notificacoes.ini`, se existir na primeira carga, e importado uma vez
para o JSON e renomeado para `.bak`.

Eventos ligados por padrao: `inicializacao_concluida`, `troca_de_harness`,
`franquia_esgotada`, `fase_concluida`, `marco_concluido`,
`rodada_concluida`, `rodada_parou`, `erro_interno`.

Eventos desligados por padrao: `planejamento_iniciado`, `fase_iniciada`,
`gates_falharam`, `correcao_iniciada`, `revisor_reprovou`, `pausa`.

Um aviso so e enviado se algum canal estiver ativo e o evento estiver ligado.

## Segredos e versionamento

`automacao/autopilot.json` contem tokens e credenciais, portanto entra no
`.gitignore` gerenciado pelo Praxis. `automacao/autopilot.exemplo.json` nao
contem segredos e pode ser versionado.

Com `versionar_automacao=true`, o Praxis continua versionando `fases.csv` e faz
commits de bookkeeping do estado. Com `false`, a pasta `automacao/` inteira fica
ignorada.

## Extensibilidade

Novo harness = implementar a interface `Motor`, registrar em `motoresRegistrados`
e, opcionalmente, definir modelo padrao e trailer de coautoria. O pipeline usa a
interface generica e nao precisa conhecer detalhes do harness.

## Arquivos principais

| Arquivo | Responsabilidade |
|---|---|
| `motor.go` | interface generica e registro de motores |
| `claude.go` | motor Claude Code |
| `codex.go` | motor Codex CLI |
| `fallback.go` | fallback e espera de reset |
| `executar.go` | pipeline por fase |
| `inicializar.go` | planejamento inicial |
| `config.go` | `autopilot.json` unificado |
| `painel.go` | painel e edicao de config |
| `notificacoes.go` / `auth.go` | notificacoes e Basic Auth |
