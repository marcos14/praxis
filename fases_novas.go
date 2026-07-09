package main

import (
	"fmt"
	"os"
	"strings"
)

// FaseNova e uma pendencia fora do escopo da fase, declarada pelo revisor no
// veredito, que o orquestrador transforma em fase da fila.
type FaseNova struct {
	Titulo     string   `json:"titulo"`
	Descricao  string   `json:"descricao"`
	Checklist  []string `json:"checklist"`
	DependeDe  []string `json:"depende_de"`
	GateExtra  string   `json:"gate_extra"`
	Observacao string   `json:"observacao"`
	// Valor classifica o valor tecnico da pendencia: "alto" (essencial; entra
	// na fila e executa sozinha) ou "baixo" (melhoria/refinamento opcional; entra
	// como StAvaliar para um humano decidir se vale a pena). Qualquer coisa fora
	// de "alto" e tratada como baixo valor, por seguranca.
	Valor string `json:"valor"`
}

// altoValor informa se a fase nova deve entrar direto na fila de execucao.
// So "alto" (case-insensitive) qualifica; ausencia ou qualquer outro valor cai
// no caminho conservador (avaliar viabilidade), reduzindo fases futeis.
func (nf FaseNova) altoValor() bool {
	return strings.EqualFold(strings.TrimSpace(nf.Valor), "alto")
}

type descarteFaseNova struct{ Titulo, Motivo string }

// normalizarTitulo reduz um titulo a uma chave de comparacao: minusculas e
// espacos colapsados.
func normalizarTitulo(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// gerarIDFaseNova cria um id "Fase 1c.n1", "Fase 1c.n2"... derivado da fase de
// origem, garantidamente inedito em `todas` (buscarFase e case-insensitive).
// Nao usa ';' (separador do CSV) nem '+' (separador de depende_de).
func gerarIDFaseNova(todas []*Fase, origem string) string {
	for n := 1; ; n++ {
		id := fmt.Sprintf("%s.n%d", origem, n)
		if buscarFase(todas, id) == nil {
			return id
		}
	}
}

// prepararFasesNovas valida as fases sugeridas pelo revisor: deduplica por
// titulo normalizado (contra as fases existentes e contra as ja aceitas no
// lote), aplica o teto `restante` (vagas ainda disponiveis na rodada), gera
// IDs ineditos e saneia depende_de. Nao toca disco.
func prepararFasesNovas(todas []*Fase, atual *Fase, sugeridas []FaseNova, restante int) (aceitas []*Fase, specs []FaseNova, descartadas []descarteFaseNova) {
	titulos := map[string]bool{}
	for _, f := range todas {
		titulos[normalizarTitulo(f.Titulo)] = true
	}
	idPorTitulo := map[string]string{} // titulo normalizado -> id gerado (deps entre fases novas do lote)
	for _, nf := range sugeridas {
		chave := normalizarTitulo(nf.Titulo)
		switch {
		case chave == "" || strings.TrimSpace(nf.Descricao) == "":
			descartadas = append(descartadas, descarteFaseNova{nf.Titulo, "titulo/descricao vazios"})
			continue
		case titulos[chave]:
			descartadas = append(descartadas, descarteFaseNova{nf.Titulo, "duplicada (titulo ja existe na fila)"})
			continue
		case len(aceitas) >= restante:
			descartadas = append(descartadas, descarteFaseNova{nf.Titulo, "teto max_fases_novas da rodada atingido"})
			continue
		}
		// Saneia depende_de: mantem so o que resolve (fase existente ou fase
		// nova ja aceita neste lote); sempre inclui a fase de origem, que
		// garante a ordem e nunca trava a fila (ela esta prestes a concluir).
		deps := []string{atual.Fase}
		for _, d := range nf.DependeDe {
			d = strings.TrimSpace(d)
			if d == "" || strings.EqualFold(d, atual.Fase) {
				continue
			}
			if buscarFase(todas, d) != nil {
				deps = append(deps, d)
			} else if id, ok := idPorTitulo[normalizarTitulo(d)]; ok {
				deps = append(deps, id)
			}
		}
		nova := &Fase{
			Fase:      gerarIDFaseNova(todas, atual.Fase),
			Titulo:    strings.TrimSpace(nf.Titulo),
			Status:    StPendente,
			DependeDe: deps,
			GateExtra: strings.TrimSpace(nf.GateExtra),
			Observacao: firstNonEmpty(strings.TrimSpace(nf.Observacao),
				fmt.Sprintf("descoberta pelo revisor na Fase %s", atual.Fase)),
		}
		// Baixo valor tecnico (ou valor nao classificado): nao executa sozinha.
		// Nasce como StAvaliar e exige decisao humana antes de virar pendente,
		// evitando que o motor crie fases futeis que geram custo e atraso.
		if !nf.altoValor() {
			nova.Status = StAvaliar
			nova.RequerHumano = true
			nova.Observacao = firstNonEmpty(strings.TrimSpace(nf.Observacao),
				fmt.Sprintf("baixo valor tecnico segundo o revisor na Fase %s — avalie se vale implementar antes de mudar para pendente", atual.Fase))
		}
		todas = append(todas, nova) // copia local: so alimenta o dedupe/IDs do lote
		titulos[chave] = true
		idPorTitulo[chave] = nova.Fase
		aceitas = append(aceitas, nova)
		specs = append(specs, nf)
	}
	return aceitas, specs, descartadas
}

const marcadorFasesDescobertas = "## Fases descobertas (adicionadas pelo Praxis)"

// anexarSpecFaseNova acrescenta a especificacao da fase ao final do plano — e
// ela que o executor da fase nova vai ler. Escrita mecanica do orquestrador,
// sem motor envolvido.
func anexarSpecFaseNova(caminhoPlano string, f *Fase, nf FaseNova) error {
	b, err := os.ReadFile(caminhoPlano)
	if err != nil {
		return err
	}
	var s strings.Builder
	if !strings.Contains(string(b), marcadorFasesDescobertas) {
		s.WriteString("\n---\n\n" + marcadorFasesDescobertas + "\n\nFases inseridas automaticamente a partir de pendencias descobertas pelo revisor.\n")
	}
	fmt.Fprintf(&s, "\n### %s — %s\n\nStatus: %s\nDepende de: %s\n", f.Fase, f.Titulo, f.Status, strings.Join(f.DependeDe, ", "))
	if f.GateExtra != "" {
		fmt.Fprintf(&s, "Gate extra: `%s`\n", f.GateExtra)
	}
	if f.Status == StAvaliar {
		s.WriteString("\n> Baixo valor tecnico: aguarda avaliacao humana de viabilidade. Nao sera executada automaticamente enquanto o status for `avaliar viabilidade`.\n")
	}
	fmt.Fprintf(&s, "\nMeta: %s\n\n", strings.TrimSpace(nf.Descricao))
	for _, item := range nf.Checklist {
		if item = strings.TrimSpace(item); item != "" {
			fmt.Fprintf(&s, "- [ ] %s\n", item)
		}
	}
	if len(nf.Checklist) == 0 {
		s.WriteString("- [ ] Implementar a meta acima com testes.\n")
	}
	arq, err := os.OpenFile(caminhoPlano, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer arq.Close()
	if _, err := arq.WriteString(s.String()); err != nil {
		return err
	}
	return arq.Close()
}

func resumoFasesNovas(aceitas []*Fase, usadas, teto int) string {
	var linhas []string
	for _, f := range aceitas {
		linhas = append(linhas, fmt.Sprintf("%s — %s (depende de %s)", f.Fase, f.Titulo, strings.Join(f.DependeDe, ", ")))
	}
	linhas = append(linhas, fmt.Sprintf("Total na rodada: %d/%d", usadas, teto))
	return strings.Join(linhas, "\n")
}

func formatarDescartes(descartadas []descarteFaseNova) string {
	var linhas []string
	for _, d := range descartadas {
		linhas = append(linhas, fmt.Sprintf("%s — %s", d.Titulo, d.Motivo))
	}
	return strings.Join(linhas, "\n")
}
