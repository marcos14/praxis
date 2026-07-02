package main

import (
	"strings"
	"time"
)

func agoraTS() string { return time.Now().Format("20060102-150405") }

func agoraLegivel() string { return time.Now().Format("2006-01-02 15:04") }

func ultimasLinhas(s string, n int) string {
	linhas := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(linhas) > n {
		linhas = linhas[len(linhas)-n:]
	}
	return strings.Join(linhas, "\n")
}

func primeirasLinhas(s string, n int) string {
	linhas := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(linhas) > n {
		linhas = append(linhas[:n:n], "(...)")
	}
	return strings.Join(linhas, "\n")
}

// renderPrompt substitui placeholders {CHAVE} pelos valores dados.
func renderPrompt(tpl string, valores map[string]string) string {
	pares := make([]string, 0, len(valores)*2)
	for k, v := range valores {
		pares = append(pares, "{"+k+"}", v)
	}
	return strings.NewReplacer(pares...).Replace(tpl)
}

func indentar(s, prefixo string) string {
	return prefixo + strings.ReplaceAll(s, "\n", "\n"+prefixo)
}
