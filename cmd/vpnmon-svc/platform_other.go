//go:build !windows

package main

import (
	"errors"
	"io"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform"
)

func defaultDataDir() (string, error) { return "", platform.ErrNotSupported }

func realPlatform() (Platform, error) { return Platform{}, platform.ErrNotSupported }

func readPassword(io.Reader) (string, error) {
	return "", errors.New("leitura sem eco só no Windows; use --password-stdin")
}

func stdinIsConsole() bool { return false }
