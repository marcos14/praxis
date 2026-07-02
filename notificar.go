package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// notificar avisa o usuario que o Praxis parou: banner no console, beep
// e (no Windows) um toast nativo — tudo melhor-esforco.
func notificar(titulo, corpo string) {
	fmt.Printf("\n╔══════════════════════════════════════════════╗\n")
	fmt.Printf("  %s\n", titulo)
	fmt.Printf("  %s\n", strings.ReplaceAll(corpo, "\n", "\n  "))
	fmt.Printf("╚══════════════════════════════════════════════╝\n\a")
	if runtime.GOOS != "windows" {
		return
	}
	script := fmt.Sprintf(`
$null = [Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType=WindowsRuntime]
$null = [Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom, ContentType=WindowsRuntime]
$x = New-Object Windows.Data.Xml.Dom.XmlDocument
$x.LoadXml('<toast><visual><binding template="ToastGeneric"><text>%s</text><text>%s</text></binding></visual></toast>')
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('Praxis').Show((New-Object Windows.UI.Notifications.ToastNotification $x))`,
		escaparToast(titulo), escaparToast(corpo))
	_ = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Run()
}

func escaparToast(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "'", "''", `"`, "&quot;", "\n", " · ")
	return r.Replace(s)
}

// escreverResumo grava o RESUMO-<ts>.md da rodada e devolve o caminho.
func escreverResumo(raiz string, rodadas []*Fase, motivoParada string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Praxis — resumo da rodada de %s\n\n", agoraLegivel())
	if len(rodadas) == 0 {
		b.WriteString("Nenhuma fase foi executada.\n")
	} else {
		b.WriteString("| Fase | Status | Custo (US$) | Observacao |\n|---|---|---|---|\n")
		for _, f := range rodadas {
			fmt.Fprintf(&b, "| %s — %s | %s | %.2f | %s |\n", f.Fase, f.Titulo, f.Status, f.CustoUSD, f.Observacao)
		}
	}
	fmt.Fprintf(&b, "\n**Parada:** %s\n", motivoParada)
	caminho := filepath.Join(dirLogs(raiz), fmt.Sprintf("RESUMO-%s.md", agoraTS()))
	_ = os.MkdirAll(dirLogs(raiz), 0o755)
	_ = os.WriteFile(caminho, []byte(b.String()), 0o644)
	return caminho
}
