//go:build windows

package main

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"

	"golang.org/x/sys/windows"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/instance"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/client"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/view"
)

// run monta a bandeja: trava de sessão → cliente do pipe → view walk.
func run() int {
	release, err := instance.Acquire(instance.TrayMutex)
	if errors.Is(err, instance.ErrAlreadyRunning) {
		return 0 // já há uma bandeja nesta sessão (login repetido, clique duplo)
	}
	if err != nil {
		fatal("Não foi possível iniciar a bandeja: " + err.Error())
		return 1
	}
	defer release()

	ver := appVersion()
	log, closeLog := trayLog()
	defer closeLog()
	log.Info("bandeja iniciada", "versao", ver)
	defer log.Info("bandeja encerrada")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// ipc.Dial confere o PID do servidor do pipe contra o do serviço (§6.1).
	c := client.New(client.Options{Dial: ipc.Dial, AppVersion: ver})
	go c.Run(ctx)

	code, err := view.Run(view.Options{Events: c.Events(), Caller: c, Stats: c.Stats, AppVersion: ver, Log: log})
	if err != nil {
		log.Error("bandeja", "erro", err)
		fatal(err.Error())
	}
	return code
}

// trayLog abre o log em %LOCALAPPDATA%\VPNMonitor; sem a pasta, a bandeja
// segue sem log (descarta).
func trayLog() (*slog.Logger, func()) {
	if dir, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0); err == nil {
		if l, closeLog, err := openTrayLog(filepath.Join(dir, "VPNMonitor")); err == nil {
			return l, closeLog
		}
	}
	return slog.New(slog.DiscardHandler), func() {}
}

// fatal avisa numa caixa de mensagem: windowsgui não tem console.
func fatal(msg string) {
	text, _ := windows.UTF16PtrFromString(msg)
	title, _ := windows.UTF16PtrFromString("VPN Monitor")
	windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR)
}
