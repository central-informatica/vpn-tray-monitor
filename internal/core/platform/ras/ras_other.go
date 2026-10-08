//go:build !windows

package ras

import "github.com/guibsu/vpn-tray-monitor/internal/core/platform"

// NewClient fora do Windows: não há RAS.
func NewClient() (Client, error) { return nil, platform.ErrNotSupported }
