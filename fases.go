package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	StPendente   = "pendente"
	StExecutando = "executando"
	StConcluida  = "concluida"
	StFalhou     = "falhou"
	StBloqueada  = "bloqueada"
	StAdiada     = "adiada"
)

var cabecalhoCSV = []string{
	"fase", "titulo", "status", "depende_de", "requer_humano", "gate_extra",
	"modelo", "tentativas", "custo_usd", "concluido_em", "observacao",
}

type Fase struct {
	Fase         string
	Titulo       string
	Status       string
	DependeDe    []string // fases separadas por '+' no CSV
	RequerHumano bool
	GateExtra    string
	Modelo       string
	Tentativas   int
	CustoUSD     float64
	ConcluidoEm  string
	Observacao   string
}

func carregarFases(caminho string) ([]*Fase, error) {
	arq, err := os.Open(caminho)
	if err != nil {
		return nil, fmt.Errorf("nao achei %s — rode `praxis inicializar` primeiro (%w)", caminho, err)
	}
	defer arq.Close()
	r := csv.NewReader(arq)
	r.Comma = ';'
	r.FieldsPerRecord = -1
	linhas, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("csv invalido em %s: %w", caminho, err)
	}
	if len(linhas) == 0 {
		return nil, fmt.Errorf("csv vazio: %s", caminho)
	}
	idx := map[string]int{}
	for i, c := range linhas[0] {
		idx[strings.ToLower(strings.TrimSpace(c))] = i
	}
	campo := func(l []string, nome string) string {
		i, ok := idx[nome]
		if !ok || i >= len(l) {
			return ""
		}
		return strings.TrimSpace(l[i])
	}
	var fases []*Fase
	for _, l := range linhas[1:] {
		if campo(l, "fase") == "" {
			continue
		}
		tent, _ := strconv.Atoi(campo(l, "tentativas"))
		custo, _ := strconv.ParseFloat(campo(l, "custo_usd"), 64)
		f := &Fase{
			Fase:         campo(l, "fase"),
			Titulo:       campo(l, "titulo"),
			Status:       campo(l, "status"),
			DependeDe:    separarDeps(campo(l, "depende_de")),
			RequerHumano: strings.EqualFold(campo(l, "requer_humano"), "sim"),
			GateExtra:    campo(l, "gate_extra"),
			Modelo:       campo(l, "modelo"),
			Tentativas:   tent,
			CustoUSD:     custo,
			ConcluidoEm:  campo(l, "concluido_em"),
			Observacao:   campo(l, "observacao"),
		}
		if f.Status == "" {
			f.Status = StPendente
		}
		fases = append(fases, f)
	}
	return fases, nil
}

func salvarFases(caminho string, fases []*Fase) error {
	arq, err := os.Create(caminho)
	if err != nil {
		return err
	}
	defer arq.Close()
	w := csv.NewWriter(arq)
	w.Comma = ';'
	if err := w.Write(cabecalhoCSV); err != nil {
		return err
	}
	for _, f := range fases {
		humano := "nao"
		if f.RequerHumano {
			humano = "sim"
		}
		tent := ""
		if f.Tentativas > 0 {
			tent = strconv.Itoa(f.Tentativas)
		}
		custo := ""
		if f.CustoUSD > 0 {
			custo = strconv.FormatFloat(f.CustoUSD, 'f', 2, 64)
		}
		linha := []string{
			f.Fase, f.Titulo, f.Status, strings.Join(f.DependeDe, "+"), humano,
			f.GateExtra, f.Modelo, tent, custo, f.ConcluidoEm, f.Observacao,
		}
		if err := w.Write(linha); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func separarDeps(s string) []string {
	var deps []string
	for _, d := range strings.Split(s, "+") {
		if d = strings.TrimSpace(d); d != "" {
			deps = append(deps, d)
		}
	}
	return deps
}

func buscarFase(fases []*Fase, nome string) *Fase {
	for _, f := range fases {
		if strings.EqualFold(f.Fase, nome) {
			return f
		}
	}
	return nil
}

// proximaPronta devolve a proxima fase executavel na ordem do arquivo:
// pendente, com todas as dependencias concluidas e sem exigir humano.
// Fases pendentes prontas mas com requer_humano=sim sao marcadas bloqueadas
// (o chamador persiste). Quando nao ha fase pronta, devolve o motivo.
func proximaPronta(fases []*Fase) (*Fase, string) {
	done := map[string]bool{}
	for _, f := range fases {
		if f.Status == StConcluida {
			done[f.Fase] = true
		}
	}
	restam := false
	for _, f := range fases {
		switch f.Status {
		case StConcluida, StAdiada:
			continue
		case StPendente:
			restam = true
			pronta := true
			for _, d := range f.DependeDe {
				if !done[d] {
					pronta = false
					break
				}
			}
			if !pronta {
				continue
			}
			if f.RequerHumano {
				f.Status = StBloqueada
				continue
			}
			return f, ""
		default: // executando, falhou, bloqueada — exigem acao humana
			restam = true
		}
	}
	if !restam {
		return nil, "fila vazia — todas as fases concluidas/adiadas"
	}
	return nil, "nenhuma fase pronta — as restantes dependem de fases nao concluidas ou exigem acao humana (requer_humano/bloqueada/falhou)"
}
