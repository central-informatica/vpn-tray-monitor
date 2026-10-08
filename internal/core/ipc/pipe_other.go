//go:build !windows

package ipc

import (
	"context"
	"net"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform"
)

// Listen fora do Windows não há named pipe.
func Listen() (net.Listener, error) { return nil, platform.ErrNotSupported }

// Dial fora do Windows não há named pipe.
func Dial(context.Context) (net.Conn, error) { return nil, platform.ErrNotSupported }
