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

// ToView converte o estado de uma VPN para o protocolo.
func ToView(v config.VPN, s domain.Status, now time.Time) ipc.VPNView {
	view := ipc.VPNView{
		Name: v.Name, Entry: v.RasEntry, Enabled: v.Enabled, CheckKind: string(v.Check.Kind),
		State: string(s.State), SinceUnix: unix(s.Since), LastCheckUnix: unix(s.LastCheck),
		LatencyMs: s.LastRTT.Milliseconds(), Failures: s.Failures, Attempt: s.Attempt,
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
