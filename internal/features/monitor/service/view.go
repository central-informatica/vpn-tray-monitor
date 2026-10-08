package service

import (
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
)

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// ceilMs arredonda para cima: um RTT medido > 0 nunca vira 0 ms ("sem dado").
func ceilMs(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64((d + time.Millisecond - 1) / time.Millisecond)
}

// ToView converte o estado de uma VPN para o protocolo.
func ToView(v config.VPN, s domain.Status, now time.Time) ipc.VPNView {
	view := ipc.VPNView{
		Name: v.Name, Entry: v.RasEntry, Enabled: v.Enabled, CheckKind: string(v.Check.Kind),
		State: string(s.State), SinceUnix: unix(s.Since), LastCheckUnix: unix(s.LastCheck),
		LatencyMs: ceilMs(s.LastRTT), Failures: s.Failures, Attempt: s.Attempt,
		Reconnects24h: s.Reconnects24h(now), PausedUntilUnix: unix(s.PausedUntil),
		PausedIndefinite: s.PausedIndefinite,
	}
	if s.NextAttempt.After(now) {
		view.NextAttemptUnix = s.NextAttempt.Unix()
	}
	if s.State == domain.CredencialInvalida && s.BlockedUntil.After(now) {
		view.BlockedUntilUnix = s.BlockedUntil.Unix()
	}
	if e := s.LastErr; e != nil {
		view.LastError = &ipc.ErrorInfo{Class: e.Class.String(), Code: e.Code, Message: e.Message}
	}
	return view
}
