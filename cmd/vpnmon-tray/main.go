// Comando vpnmon-tray: a bandeja do VPN Monitor, uma por sessão de usuário
// (§7). Iniciada pelo HKLM\...\Run que o MSI grava (Marco C); a bandeja não
// se registra sozinha.
package main

import (
	"os"
	"strings"
)

// Preenchidos por -ldflags no build de release.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

// appVersion é a versão mostrada em "Sobre / versão" e enviada no hello.
// Fora de uma tag, o git describe já começa pelo commit ("5210ef3-dirty"):
// aí o commit não se repete entre parênteses.
func appVersion() string {
	var extra []string
	if commit != "" && !strings.HasPrefix(version, commit) {
		extra = append(extra, commit)
	}
	if date != "" {
		extra = append(extra, date)
	}
	if len(extra) == 0 {
		return version
	}
	return version + " (" + strings.Join(extra, ", ") + ")"
}

func main() { os.Exit(run()) }
