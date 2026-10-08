//go:build windows

package main

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/acl"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/dpapi"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/icmp"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/netwatch"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

func defaultDataDir() (string, error) {
	pd, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(pd, "VPNMonitor"), nil
}

// realPlatform monta as implementações Windows.
func realPlatform() (Platform, error) {
	r, err := ras.NewClient()
	if err != nil {
		return Platform{}, err
	}
	nw, err := netwatch.New()
	if err != nil {
		return Platform{}, err
	}
	ev, err := logging.OpenEventSink()
	if err != nil {
		ev = logging.NopSink{} // origem não registrada (sem install/MSI): segue sem Event Log
	}
	return Platform{
		RAS: r, Pinger: icmp.New(), Net: nw, DPAPI: dpapi.New(), ACL: acl.New(),
		Listen: ipc.Listen, Events: ev, ReadSeed: config.ReadSeedRegistry,
	}, nil
}

// readPassword lê uma linha do console sem eco.
func readPassword(in io.Reader) (string, error) {
	h := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return "", errors.New("sem console para ler a senha; use --password-stdin")
	}
	if err := windows.SetConsoleMode(h, mode&^windows.ENABLE_ECHO_INPUT); err != nil {
		return "", err
	}
	defer func() { _ = windows.SetConsoleMode(h, mode) }()
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return trimEOL(line), nil
}
