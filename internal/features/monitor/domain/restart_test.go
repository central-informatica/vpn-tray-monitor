package domain

import (
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

// last é um estado "vivido": bloqueio, histórico e backoff preenchidos.
func lived(st State) Status {
	return Status{
		State: st, Since: t0.Add(-time.Hour), Op: OpDial, Failures: 2,
		Attempt: 3, NextAttempt: t0.Add(2 * time.Minute), NextTick: t0.Add(time.Minute),
		Blocked: "", BlockedFP: "fp-velha", RejectedAt: t0.Add(-10 * time.Minute),
		LastManualTry: t0.Add(-5 * time.Minute),
		LastErr:       &DialError{Class: ras.ClassTransitorio, Code: 809, Message: "x"},
		DownSince:     t0.Add(-20 * time.Minute), WasUp: true,
		Reconnects: []time.Time{t0.Add(-3 * time.Hour)},
	}
}

func assertMemory(t *testing.T, got, last Status) {
	t.Helper()
	if got.BlockedFP != last.BlockedFP || !got.RejectedAt.Equal(last.RejectedAt) ||
		!got.LastManualTry.Equal(last.LastManualTry) || got.LastErr != last.LastErr ||
		got.Attempt != last.Attempt || !got.NextAttempt.Equal(last.NextAttempt) ||
		got.WasUp != last.WasUp || !got.DownSince.Equal(last.DownSince) ||
		len(got.Reconnects) != len(last.Reconnects) {
		t.Fatalf("memória perdida:\n got %+v\nlast %+v", got, last)
	}
	if got.Op != OpNone {
		t.Fatalf("Op deveria zerar: %v", got.Op)
	}
}

func TestRestartKeepsCredentialBlock(t *testing.T) {
	last := lived(CredencialInvalida)
	last.Blocked = CredencialInvalida
	last.Op = OpNone
	s := Restart(last, params(), t0, config.Pause{})
	assertMemory(t, s, last)
	if s.State != CredencialInvalida || s.Blocked != CredencialInvalida || !s.NextTick.IsZero() {
		t.Fatalf("deveria recriar bloqueada, sem tique: %+v", s)
	}
}

func TestRestartDuringManualRetryOfBlockedReturnsToBlock(t *testing.T) {
	last := lived(Reconectando) // reconnect manual em curso, memória do bloqueio
	last.Blocked = CredencialInvalida
	s := Restart(last, params(), t0, config.Pause{})
	assertMemory(t, s, last)
	if s.State != CredencialInvalida || !s.NextTick.IsZero() || !s.Since.Equal(t0) {
		t.Fatalf("%+v", s)
	}
}

func TestRestartKeepsBackoff(t *testing.T) {
	last := lived(Reconectando)
	s := Restart(last, params(), t0, config.Pause{})
	assertMemory(t, s, last)
	if s.State != Reconectando || !s.Since.Equal(last.Since) || !s.NextTick.Equal(t0) {
		t.Fatalf("%+v", s)
	}
	// A sonda logo em seguida respeita o backoff: enlace caído antes de
	// NextAttempt não disca.
	d := Decide(s, Input{Kind: InTick}, params(), Env{Now: t0})
	d = Decide(d.Next, Input{Kind: InLinkResult, Network: true}, params(), Env{Now: t0})
	if d.Action != OpNone || !d.Next.NextTick.Equal(last.NextAttempt) {
		t.Fatalf("discou antes do backoff: %+v %+v", d.Action, d.Next)
	}
}

func TestRestartRespectsPause(t *testing.T) {
	last := lived(Pausada)
	last.Blocked = CredencialInvalida
	until := t0.Add(time.Hour)
	s := Restart(last, params(), t0, config.Pause{UntilUnix: until.Unix()})
	assertMemory(t, s, last)
	if s.State != Pausada || !s.PausedUntil.Equal(until) || !s.NextTick.Equal(until) || s.Blocked != CredencialInvalida {
		t.Fatalf("%+v", s)
	}
	s = Restart(lived(Conectada), params(), t0, config.Pause{Indefinite: true})
	if s.State != Pausada || !s.PausedIndefinite || !s.NextTick.IsZero() {
		t.Fatalf("indefinida: %+v", s)
	}
	// Pausa vencida na janela de recriação: sai dela e verifica já.
	s = Restart(lived(Pausada), params(), t0, config.Pause{})
	if s.State != Desconhecido || !s.NextTick.Equal(t0) || s.PausedIndefinite || !s.PausedUntil.IsZero() {
		t.Fatalf("pausa vencida: %+v", s)
	}
}

func TestRestartDisabled(t *testing.T) {
	p := params()
	p.Enabled = false
	if s := Restart(lived(Conectada), p, t0, config.Pause{}); s.State != Desativada {
		t.Fatalf("%+v", s)
	}
}

func TestReconfigureSameEntryKeepsOnlyBlockMemory(t *testing.T) {
	last := lived(Degradada)
	last.Op = OpNone
	last.Blocked = ""
	last.LastRTT = 40 * time.Millisecond
	p := params()
	p.Failures = 5 // só a verificação mudou: backoff intacto
	s := Reconfigure(last, params(), p, t0, config.Pause{})
	if s.State != Desconhecido || !s.Since.Equal(t0) || s.Failures != 0 || s.LastRTT != 0 || !s.NextTick.Equal(t0) {
		t.Fatalf("Degradada deveria virar Desconhecido sem falhas: %+v", s)
	}
	assertMemory(t, s, last) // bloqueio, reconexões, WasUp/DownSince e backoff ficam

	// Backoff alterado: a próxima discagem não espera o prazo calculado
	// com os limites antigos.
	p = params()
	p.MaxBackoff = 10 * time.Minute
	s = Reconfigure(lived(Reconectando), params(), p, t0, config.Pause{})
	if !s.NextAttempt.IsZero() || s.State != Reconectando || s.BlockedFP != "fp-velha" {
		t.Fatalf("backoff novo deveria limpar NextAttempt: %+v", s)
	}
	p = params()
	p.Interval = time.Minute
	if s = Reconfigure(lived(Reconectando), params(), p, t0, config.Pause{}); !s.NextAttempt.IsZero() {
		t.Fatalf("intervalo novo deveria limpar NextAttempt: %+v", s)
	}

	// Bloqueio por credencial continua bloqueio, sem tique.
	b := lived(CredencialInvalida)
	b.Blocked = CredencialInvalida
	s = Reconfigure(b, params(), p, t0, config.Pause{})
	if s.State != CredencialInvalida || s.Blocked != CredencialInvalida || !s.NextTick.IsZero() || s.LastErr != b.LastErr {
		t.Fatalf("bloqueio perdido: %+v", s)
	}
}

func TestOpString(t *testing.T) {
	for op, want := range map[Op]string{OpNone: "nenhuma", OpProbeLink: "sonda de enlace",
		OpProbeReach: "verificação de alcance", OpDial: "discagem", OpHangupDial: "desligar e discar", Op(99): "Op(99)"} {
		if got := op.String(); got != want {
			t.Errorf("%d: %q", int(op), got)
		}
	}
}
