//go:build !windows

package icmp

import (
	"context"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform"
)

type stubPinger struct{}

// New fora do Windows devolve um Pinger que sempre falha.
func New() Pinger { return stubPinger{} }

func (stubPinger) Ping(context.Context, string, time.Duration) (Result, error) {
	return Result{}, platform.ErrNotSupported
}
