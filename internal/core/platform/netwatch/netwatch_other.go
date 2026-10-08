//go:build !windows

package netwatch

import "github.com/guibsu/vpn-tray-monitor/internal/core/platform"

// New fora do Windows não observa nada.
func New() (Watcher, error) { return nil, platform.ErrNotSupported }
