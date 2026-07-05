Voce vai preparar um plano para o **Praxis**: um orquestrador que executa fases de desenvolvimento uma a uma, cada fase numa execucao independente de um motor de codigo (contexto limpo), com testes como gate e um commit por fase.

O plano do usuario esta em `{PLANO}`. Leia-o por inteiro. Leia tambem as instrucoes do projeto (AGENTS.md/CLAUDE.md/README, se existirem) para entender a stack e os comandos de verificacao.

Sua tarefa tem 3 partes:

**1. Quebre o plano em micro-fases**, cada uma executavel confortavelmente em uma unica execucao de motor. Criterios de tamanho:
- Uma fatia vertical de valor: algo testavel/demonstravel ao final.
- Escopo de poucas horas de trabalho, nao uma reescrita de subsistema.
- Tem os proprios testes; a fase so conta como pronta com testes verdes.
- Declara dependencias explicitas de outras fases, apenas as reais.
- Fases que exigem hardware fisico, aprovacao externa ou decisao de negocio devem ser marcadas com `requer_humano` e o motivo em `observacao`.
- Se uma fase do plano original for grande demais, divida-a (ex.: 4a, 4b). Se ja estiver do tamanho certo, mantenha como esta. Fases ja concluidas recebem `status=concluida`.

**2. Edite `{PLANO}`** para refletir as micro-fases: cada fase com meta, checklist de tarefas (checkboxes `- [ ]`), linha "Depende de:" e criterios de teste. Preserve conteudo e historico existentes; reorganize, nao apague. Se o plano ainda nao tiver uma secao **Registro de Andamento**, crie-a. Ela e a memoria compartilhada entre fases.

**3. Identifique os gates de verificacao** do(s) projeto(s): comandos deterministicos que provam que o codigo esta saudavel (build, analise estatica, formatacao, testes). Use comandos documentados pelo projeto. Para repositorios adicionais, marque `somente_se_mudou=true`. Se houver verificacoes lentas ou com setup especial, modele-as como `gates_extra` e referencie na coluna `gate_extra` apenas das fases que precisam.

Cada gate roda a partir de um diretorio: o campo `dir` (relativo a raiz). Em monorepos, aponte `dir` para a pasta onde o comando funciona, como onde esta o `go.mod` ou `package.json`. Deixe vazio apenas quando o comando realmente roda na raiz.

Os gates rodam pelo shell do sistema operacional do orquestrador. Portanto os comandos precisam ser portaveis:
- Nao use caminhos POSIX como `./node_modules/.bin/tsc`; prefira `npx tsc`, `npm run <script>`, `go`, `python -m <mod>`, `dotnet`, etc.
- Use ferramentas no PATH.
- Evite operadores/sintaxe especificos de um shell (`&&`, `source`, aspas simples POSIX). Cada comando e uma entrada separada em `comandos`.

Sua resposta final deve ser apenas o JSON estruturado solicitado (`fases`, `gates` e, se aplicavel, `gates_extra`).
