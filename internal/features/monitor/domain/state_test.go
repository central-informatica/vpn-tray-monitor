package domain

import (
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

var t0 = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func params() Params {
	return ParamsFrom(config.RawVPN{Name: "Matriz", RasEntry: "VPN Matriz",
		Check: &config.RawCheck{Kind: config.CheckPing, Host: "10.0.0.1"}}.Normalize())
}

func TestParamsFrom(t *testing.T) {
	p := params()
	if p.Interval != 30*time.Second || p.Failures != 3 || p.Grace != 15*time.Second ||
		p.ConnectTimeout != time.Minute || p.MaxBackoff != 5*time.Minute || !p.Enabled || p.CheckKind != config.CheckPing {
		t.Fatalf("%+v", p)
	}
}

func TestInitial(t *testing.T) {
	p := params()
	if s := Initial(p, t0, config.Pause{}); s.State != Desconhecido || !s.NextTick.Equal(t0) {
		t.Fatalf("normal: %+v", s)
	}
	p.Enabled = false
	if s := Initial(p, t0, config.Pause{}); s.State != Desativada || !s.NextTick.IsZero() {
		t.Fatalf("desativada: %+v", s)
	}
	p.Enabled = true
	if s := Initial(p, t0, config.Pause{Indefinite: true}); s.State != Pausada || !s.PausedIndefinite {
		t.Fatalf("pausa indefinida: %+v", s)
	}
	until := t0.Add(time.Hour)
	s := Initial(p, t0, config.Pause{UntilUnix: until.Unix()})
	if s.State != Pausada || !s.PausedUntil.Equal(until) || !s.NextTick.Equal(until) {
		t.Fatalf("pausa temporária: %+v", s)
	}
	if s.PauseRecord() != (config.Pause{UntilUnix: until.Unix()}) {
		t.Fatalf("PauseRecord: %+v", s.PauseRecord())
	}
	if s := Initial(p, t0, config.Pause{UntilUnix: t0.Add(-time.Minute).Unix()}); s.State != Desconhecido {
		t.Fatalf("pausa vencida não vale: %+v", s)
	}
}

func TestFormatOutage(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second: "45 s", 4 * time.Minute: "4 min", 2 * time.Hour: "2 h",
		2*time.Hour + 5*time.Minute: "2 h 5 min",
	}
	for d, want := range cases {
		if got := FormatOutage(d); got != want {
			t.Errorf("%v → %q, quer %q", d, got, want)
		}
	}
}

func TestConfigErrorText(t *testing.T) {
	got := ConfigErrorText(&DialError{Code: ras.ERROR_CANNOT_FIND_PHONEBOOK_ENTRY}, "VPN Matriz")
	if want := `a entrada RAS "VPN Matriz" não existe no catálogo de todos os usuários; recrie-a com Add-VpnConnection -AllUserConnection`; got != want {
		t.Fatal(got)
	}
	if got := ConfigErrorText(&DialError{Code: 720, Message: "sem protocolos"}, "x"); got != "erro 720: sem protocolos" {
		t.Fatal(got)
	}
}

func TestReconnects24h(t *testing.T) {
	s := Status{Reconnects: []time.Time{t0.Add(-25 * time.Hour), t0.Add(-time.Hour), t0}}
	if n := s.Reconnects24h(t0); n != 2 {
		t.Fatal(n)
	}
}
