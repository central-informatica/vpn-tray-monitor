// Comando vpnmon-tray: a bandeja do VPN Monitor, uma por sessão de usuário
// (§7). Iniciada pelo HKLM\...\Run que o MSI grava (Marco C); a bandeja não
// se registra sozinha.
package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
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

// Log da bandeja: um por usuário, em %LOCALAPPDATA%\VPNMonitor (o usuário não
// lê a pasta do serviço). Pequeno e rotativo: só avisos da interface e o
// ciclo de vida da bandeja. O mesmo usuário em duas sessões (console + RDP)
// tem duas bandejas no mesmo arquivo: o log é aberto com compartilhamento de
// exclusão/renomeação, então uma rotação funciona mesmo com a outra aberta; a
// outra segue escrevendo no arquivo renomeado (vpnmon-tray.1.log) até
// reabri-lo, e algumas linhas vão parar lá (nada se perde nem trava).
const (
	trayLogName     = "vpnmon-tray.log"
	trayLogMaxBytes = 1 << 20
	trayLogMaxFiles = 3
)

// openTrayLog abre o log da bandeja em dir (criada se preciso), com a mesma
// redação de segredos do serviço. close fecha o arquivo.
func openTrayLog(dir string) (log *slog.Logger, closeLog func(), err error) {
	w, err := logging.OpenRotating(filepath.Join(dir, trayLogName), trayLogMaxBytes, trayLogMaxFiles)
	if err != nil {
		return nil, nil, err
	}
	return logging.New(w, new(slog.LevelVar)), func() { _ = w.Close() }, nil
}
