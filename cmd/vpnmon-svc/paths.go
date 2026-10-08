package main

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/service"
)

// dataDirEnv permite apontar outra pasta de dados (testes, desenvolvimento).
const dataDirEnv = "VPNMON_DATA_DIR"

// layout são os caminhos dentro da pasta de dados (§5.1).
type layout struct {
	Dir         string
	Credentials string
	service.Paths
}

func newLayout(dir string) layout {
	return layout{
		Dir:         dir,
		Credentials: filepath.Join(dir, "credentials"),
		Paths: service.Paths{
			ConfigFile: filepath.Join(dir, "config.json"),
			StateFile:  filepath.Join(dir, "state.json"),
			LogFile:    filepath.Join(dir, "logs", "vpnmon.log"),
		},
	}
}

// dataDir resolve a pasta: VPNMON_DATA_DIR ou %ProgramData%\VPNMonitor.
func dataDir() (string, error) {
	if d := os.Getenv(dataDirEnv); d != "" {
		return d, nil
	}
	d, err := defaultDataDir()
	if err != nil {
		return "", errors.New("pasta de dados indefinida: defina " + dataDirEnv)
	}
	return d, nil
}
