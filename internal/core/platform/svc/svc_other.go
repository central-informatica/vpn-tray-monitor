//go:build !windows

package svc

import (
	"os"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform"
)

func IsService() (bool, error)    { return false, nil }
func Run(Hooks) error             { return platform.ErrNotSupported }
func Install(string) error        { return platform.ErrNotSupported }
func Uninstall() error            { return platform.ErrNotSupported }
func ServicePID() (uint32, error) { return 0, platform.ErrNotSupported }
func IsElevated() bool            { return os.Geteuid() == 0 }
