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

	// Teto do backoff menor: a próxima discagem não espera mais que ele.
	p = params()
	p.Interval, p.MaxBackoff = 5*time.Second, 30*time.Second
	s = Reconfigure(lived(Reconectando), params(), p, t0, config.Pause{})
	if !s.NextAttempt.Equal(t0.Add(30*time.Second)) || s.State != Reconectando || s.BlockedFP != "fp-velha" {
		t.Fatalf("NextAttempt deveria cair para o teto novo: %+v", s)
	}
	// Teto maior: o prazo já calculado (menor) fica.
	p.MaxBackoff = 10 * time.Minute
	if s = Reconfigure(lived(Reconectando), params(), p, t0, config.Pause{}); !s.NextAttempt.Equal(t0.Add(2 * time.Minute)) {
		t.Fatalf("teto maior não deveria adiar: %+v", s)
	}
	// Sem prazo pendente, continua sem prazo.
	none := lived(Reconectando)
	none.NextAttempt = time.Time{}
	if s = Reconfigure(none, params(), p, t0, config.Pause{}); !s.NextAttempt.IsZero() {
		t.Fatalf("NextAttempt zero deveria ficar zero: %+v", s)
	}
	p = params()
	p.Interval = time.Minute

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

func TestReconfigureNewEntry(t *testing.T) {
	p := params()
	p.Entry = "VPN Nova"
	// Credencial rejeitada: o cofre é por nome de VPN, o bloqueio fica.
	b := lived(CredencialInvalida)
	b.Op = OpNone
	b.Blocked = CredencialInvalida
	s := Reconfigure(b, params(), p, t0, config.Pause{})
	if s.State != CredencialInvalida || s.BlockedFP != "fp-velha" || !s.RejectedAt.Equal(b.RejectedAt) ||
		!s.LastManualTry.Equal(b.LastManualTry) || !s.NextTick.IsZero() || s.LastErr != b.LastErr {
		t.Fatalf("memória de credencial perdida: %+v", s)
	}
	// Erro de configuração era da entrada antiga: sai do bloqueio e verifica já.
	e := lived(ErroConfig)
	e.Blocked = ErroConfig
	s = Reconfigure(e, params(), p, t0, config.Pause{})
	if s.State != Desconhecido || s.Blocked != "" || s.LastErr != nil || !s.NextTick.Equal(t0) {
		t.Fatalf("erro de configuração da entrada antiga continuou: %+v", s)
	}
	// Enlace e alcance da entrada antiga não valem; backoff zera.
	c := lived(Conectada)
	c.LastRTT = time.Millisecond
	s = Reconfigure(c, params(), p, t0, config.Pause{})
	if s.State != Desconhecido || s.Failures != 0 || s.LastRTT != 0 || s.Attempt != 0 ||
		!s.NextAttempt.IsZero() || !s.LastCheck.IsZero() || s.BlockedFP != "fp-velha" || len(s.Reconnects) != 1 {
		t.Fatalf("estado de enlace da entrada antiga: %+v", s)
	}
}

// Desativar guarda a memória de credencial rejeitada (não a de ErroConfig,
// que sai com a config alterada); o resto zera.
func TestRestartDisabledKeepsCredentialMemory(t *testing.T) {
	off := params()
	off.Enabled = false
	last := lived(CredencialInvalida)
	last.Blocked = CredencialInvalida
	s := Restart(last, off, t0, config.Pause{})
	if s.State != Desativada || s.Blocked != CredencialInvalida || s.BlockedFP != "fp-velha" ||
		!s.RejectedAt.Equal(last.RejectedAt) || !s.LastManualTry.Equal(last.LastManualTry) ||
		s.LastErr != nil || !s.NextTick.IsZero() || s.Attempt != 0 {
		t.Fatalf("Desativada sem a memória de credencial: %+v", s)
	}
	e := lived(ErroConfig)
	e.Blocked = ErroConfig
	if s := Restart(e, off, t0, config.Pause{}); s.Blocked != "" || s.State != Desativada {
		t.Fatalf("ErroConfig não deveria ficar guardado: %+v", s)
	}
	// Memória guardada numa pausa também passa para Desativada.
	pz := lived(Pausada)
	pz.Blocked = CredencialInvalida
	if s := Restart(pz, off, t0, config.Pause{}); s.Blocked != CredencialInvalida || s.BlockedFP != "fp-velha" {
		t.Fatalf("memória da pausa perdida: %+v", s)
	}
}

func TestCredMemoryOf(t *testing.T) {
	if _, ok := CredMemoryOf(lived(Conectada)); ok {
		t.Fatal("sem bloqueio não há memória")
	}
	last := lived(Reconectando) // reconnect manual de um bloqueado em curso
	last.Blocked = CredencialInvalida
	m, ok := CredMemoryOf(last)
	if !ok || m.FP != "fp-velha" || !m.RejectedAt.Equal(last.RejectedAt) || !m.LastManualTry.Equal(last.LastManualTry) {
		t.Fatalf("%+v %v", m, ok)
	}
	if m.Ref() != last.LastManualTry {
		t.Fatalf("referência = a mais recente entre rejeição e tentativa manual: %v", m.Ref())
	}
}
