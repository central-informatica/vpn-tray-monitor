//go:build !windows

package instance

import "github.com/guibsu/vpn-tray-monitor/internal/core/platform"

// Acquire fora do Windows não há mutex nomeado.
func Acquire(string) (func(), error) { return nil, platform.ErrNotSupported }
