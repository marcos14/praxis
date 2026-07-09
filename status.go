package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

func cmdStatus(argv []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	raizFlag := fs.String("raiz", "", "raiz do projeto (padrao: deteccao automatica)")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	raiz := resolverRaiz(*raizFlag)
	fases, err := carregarFases(caminhoCSV(raiz))
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "FASE\tSTATUS\tDEPENDE DE\tHUMANO\tCUSTO\tCONCLUIDA EM\tTITULO")
	for _, f := range fases {
		humano := ""
		if f.RequerHumano {
			humano = "sim"
		}
		custo := ""
		if f.CustoUSD > 0 {
			custo = fmt.Sprintf("$%.2f", f.CustoUSD)
		}
		fmt.Fprintf(w, "%s\t%s %s\t%s\t%s\t%s\t%s\t%s\n",
			f.Fase, iconeStatus(f.Status), f.Status, strings.Join(f.DependeDe, "+"),
			humano, custo, f.ConcluidoEm, f.Titulo)
	}
	return w.Flush()
}

func iconeStatus(s string) string {
	switch s {
	case StConcluida:
		return "✅"
	case StExecutando:
		return "🔄"
	case StFalhou:
		return "❌"
	case StBloqueada:
		return "⏸️"
	case StPausada:
		return "⏯️"
	case StAdiada:
		return "⏭️"
	case StAvaliar:
		return "🔍"
	default:
		return "⬜"
	}
}
