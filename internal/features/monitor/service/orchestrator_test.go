package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/fake"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/adapters"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

type orchHarness struct {
	t      *testing.T
	o      *Orchestrator
	w      *stubWorld
	clk    *shared.FakeClock
	events *logging.RecordingSink
	paths  Paths
	ras    *fake.RAS
	pinger *fake.Pinger
	cancel context.CancelFunc
}

func vpnNamed(name string) config.VPN {
	return config.RawVPN{Name: name, RasEntry: "VPN " + name,
		Check: &config.RawCheck{Kind: config.CheckPing, Host: "10.0.0.1"}}.Normalize()
}

func cfgWith(vpns ...config.VPN) config.Config {
	c := config.Empty()
	c.VPNs = vpns
	return c
}

func newOrch(t *testing.T, w *stubWorld, cfg config.Config, st config.State) *orchHarness {
	t.Helper()
	return newOrchWith(t, w, cfg, st, nil)
}

func newOrchWith(t *testing.T, w *stubWorld, cfg config.Config, st config.State, tweak func(*Options)) *orchHarness {
	t.Helper()
	dir := t.TempDir()
	h := &orchHarness{t: t, w: w, clk: shared.NewFakeClock(t0), events: &logging.RecordingSink{},
		paths: Paths{ConfigFile: filepath.Join(dir, "config.json"), StateFile: filepath.Join(dir, "state.json"),
			LogFile: filepath.Join(dir, "vpnmon.log")},
		ras: fake.NewRAS("VPN Matriz", "VPN Filial", "VPN Backup")}
	if _, err := config.Save(h.paths.ConfigFile, cfg); err != nil {
		t.Fatal(err)
	}
	h.pinger = fake.NewPinger()
	h.pinger.SetReachable("10.0.0.1", true)
	opts := Options{
		Paths: h.paths, Clock: h.clk, RAS: h.ras, Pinger: h.pinger, Link: w, Dialer: w, Creds: w,
		Events: h.events,
	}
	if tweak != nil {
		tweak(&opts)
	}
	h.o = New(opts, cfg, st)
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.o.Start(ctx)
	t.Cleanup(func() { cancel(); h.o.Stop() })
	return h
}

func (h *orchHarness) waitView(name string, state domain.State) ipc.VPNView {
	h.t.Helper()
	return h.waitViewWhere(name, func(v ipc.VPNView) bool { return v.State == string(state) })
}

// waitViewWhere espera a VPN satisfazer cond (estado e campos juntos, para
// não pegar um retrato intermediário).
func (h *orchHarness) waitViewWhere(name string, cond func(ipc.VPNView) bool) ipc.VPNView {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, v := range h.o.Status().VPNs {
			if v.Name == name && cond(v) {
				return v
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.t.Fatalf("%s não chegou à condição esperada: %+v", name, h.o.Status())
	return ipc.VPNView{}
}

func (h *orchHarness) supOf(name string) *Supervisor {
	h.o.mu.Lock()
	defer h.o.mu.Unlock()
	return h.o.sups[config.NameKey(name)].sup
}

func TestOrchestratorStartsOnePerVPN(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz"), vpnNamed("Filial")), config.State{})
	h.waitView("Matriz", domain.Conectada)
	v := h.waitView("Filial", domain.Conectada)
	if v.Entry != "VPN Filial" || v.LatencyMs != 12 || v.CheckKind != "ping" {
		t.Fatalf("view %+v", v)
	}
}

func TestOrchestratorPartialReload(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz"), vpnNamed("Filial")), config.State{})
	h.waitView("Matriz", domain.Conectada)
	h.waitView("Filial", domain.Conectada)
	matriz, filial := h.supOf("Matriz"), h.supOf("Filial")

	changed := vpnNamed("Filial")
	changed.IntervalSeconds = 60
	h.o.applyMu.Lock()
	h.o.apply(cfgWith(vpnNamed("Matriz"), changed, vpnNamed("Backup")))
	h.o.applyMu.Unlock()
	if h.supOf("Matriz") != matriz {
		t.Fatal("VPN sem mudança não pode ser reiniciada")
	}
	if h.supOf("Filial") == filial {
		t.Fatal("VPN alterada deve ser reiniciada")
	}
	h.waitView("Backup", domain.Conectada)

	h.o.applyMu.Lock()
	h.o.apply(cfgWith(vpnNamed("Matriz")))
	h.o.applyMu.Unlock()
	if n := len(h.o.Status().VPNs); n != 1 {
		t.Fatalf("removidas continuam: %d", n)
	}
}

func TestOrchestratorPausePersistsAcrossRestart(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitView("Matriz", domain.Conectada)
	if err := h.o.Pause("matriz", nil); err != nil {
		t.Fatal(err)
	}
	h.waitView("Matriz", domain.Pausada)
	st, err := config.LoadState(h.paths.StateFile, t0)
	if err != nil || !st.Pauses["matriz"].Indefinite {
		t.Fatalf("state.json: %+v %v", st, err)
	}
	var e *ipc.Error
	if err := h.o.Reconnect("Matriz"); !asIPC(err, &e) || e.Code != ipc.CodePaused {
		t.Fatalf("reconnect em pausa: %v", err)
	}
	if err := h.o.CheckNow("Nenhuma"); !asIPC(err, &e) || e.Code != ipc.CodeNotFound {
		t.Fatalf("VPN inexistente: %v", err)
	}
	past := t0.Add(-time.Minute)
	if err := h.o.Pause("Matriz", &past); !asIPC(err, &e) || e.Code != ipc.CodeBadRequest {
		t.Fatalf("pausa no passado: %v", err)
	}

	h2 := newOrch(t, w, cfgWith(vpnNamed("Matriz")), st)
	h2.waitView("Matriz", domain.Pausada)
}

func TestOrchestratorRecoversPanic(t *testing.T) {
	w := &stubWorld{up: true, network: true, panicOn: "probe"}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	deadline := time.Now().Add(2 * time.Second)
	for len(h.events.Snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	ev := h.events.Snapshot()
	if len(ev) != 1 || ev[0].Level != "error" || !strings.Contains(ev[0].Msg, "pânico") {
		t.Fatalf("Event Log: %+v", ev)
	}
	if !h.clk.WaitForDeadline(5*time.Second, time.Second) {
		t.Fatal("recriação não agendada para 5 s")
	}
	h.clk.Advance(5 * time.Second)
	h.waitView("Matriz", domain.Conectada)
}

func TestOrchestratorSubscribeAndStopping(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	events, cancel := h.o.Subscribe()
	defer cancel()
	first := <-events
	if first.Type != ipc.TypeSnapshot {
		t.Fatalf("primeiro evento %s", first.Type)
	}
	h.waitView("Matriz", domain.Conectada)
	// A VPN pode ter conectado antes da inscrição (aí já veio no snapshot);
	// uma verificação garante um vpnState depois dela.
	if err := h.o.CheckNow("Matriz"); err != nil {
		t.Fatal(err)
	}
	h.waitView("Matriz", domain.Conectada)
	h.o.Stop()
	sawState, sawStopping := false, false
	for m := range drain(events) {
		sawState = sawState || m.Type == ipc.TypeVPNState
		sawStopping = sawStopping || m.Type == ipc.TypeServiceStopping
	}
	if !sawState || !sawStopping {
		t.Fatalf("vpnState=%v serviceStopping=%v", sawState, sawStopping)
	}
}

func TestOrchestratorClockJumpResetsBackoff(t *testing.T) {
	w := &stubWorld{network: true, outcomes: []adapters.DialOutcome{{Err: &domain.DialError{Code: 809}}}}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	v := h.waitViewWhere("Matriz", func(v ipc.VPNView) bool { return v.NextAttemptUnix != 0 })
	if v.State != string(domain.Reconectando) || v.NextAttemptUnix != t0.Add(30*time.Second).Unix() {
		t.Fatalf("primeiro backoff: %+v", v)
	}
	h.clk.Suspend(2 * time.Hour)   // máquina dormiu
	h.clk.Advance(5 * time.Second) // tique do detector: salto de relógio
	if !h.clk.WaitForDeadline(5*time.Second, time.Second) {
		t.Fatal("retomada deveria agendar verificação em 5 s")
	}
	h.clk.Advance(5 * time.Second)
	h.waitView("Matriz", domain.Conectada)
	if n := len(w.get().dials); n != 2 {
		t.Fatalf("discagens = %d", n)
	}
}

func drain(ch <-chan ipc.Message) <-chan ipc.Message {
	out := make(chan ipc.Message, 100)
	go func() {
		defer close(out)
		for {
			select {
			case m, ok := <-ch:
				if !ok {
					return
				}
				out <- m
			case <-time.After(50 * time.Millisecond):
				return
			}
		}
	}()
	return out
}

func TestStopWithHungEchoIsFast(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.pinger.SetHang(true) // IcmpSendEcho2 preso, ignorando ctx
	defer h.pinger.SetHang(false)
	deadline := time.Now().Add(2 * time.Second)
	for h.pinger.Calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	h.o.Stop()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("parada com eco preso levou %s", d)
	}
	if _, err := os.Stat(h.paths.StateFile); err != nil {
		t.Fatal("state.json deveria ser gravado")
	}
}

func TestStopDeadlineWithHungSupervisor(t *testing.T) {
	hang := make(chan struct{})
	defer close(hang)
	w := &stubWorld{network: true, hang: hang} // discador preso ignorando ctx
	h := newOrchWith(t, w, cfgWith(vpnNamed("Matriz")), config.State{}, func(o *Options) { o.StopTimeout = 200 * time.Millisecond })
	deadline := time.Now().Add(2 * time.Second)
	for len(w.get().dials) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	h.o.Stop()
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Stop deve respeitar o prazo global, levou %s", d)
	}
	if _, err := os.Stat(h.paths.StateFile); err != nil {
		t.Fatal("state.json deveria ser gravado mesmo com supervisor preso")
	}
}

func TestRemovedVPNPauseLeavesStateFile(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz"), vpnNamed("Filial")), config.State{})
	h.waitView("Filial", domain.Conectada)
	if err := h.o.Pause("Filial", nil); err != nil {
		t.Fatal(err)
	}
	h.waitView("Filial", domain.Pausada)
	h.o.applyMu.Lock()
	h.o.apply(cfgWith(vpnNamed("Matriz")))
	h.o.applyMu.Unlock()
	st, err := config.LoadState(h.paths.StateFile, t0)
	if err != nil || len(st.Pauses) != 0 {
		t.Fatalf("pausa da VPN removida continua em state.json: %+v %v", st, err)
	}
}

// Vários supervisores presos: o prazo global vale para todos juntos (um
// temporizador que dispara uma vez só deixaria o segundo esperando).
func TestStopDeadlineWithSeveralHungSupervisors(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	w := &stubWorld{network: true, gate: gate} // sondas presas ignorando ctx
	h := newOrchWith(t, w, cfgWith(vpnNamed("Matriz"), vpnNamed("Filial"), vpnNamed("Backup")), config.State{},
		func(o *Options) { o.StopTimeout = 200 * time.Millisecond })
	deadline := time.Now().Add(2 * time.Second)
	for w.get().probes < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	h.o.Stop()
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Stop deve respeitar o prazo global com vários presos, levou %s", d)
	}
	if _, err := os.Stat(h.paths.StateFile); err != nil {
		t.Fatal("state.json deveria ser gravado")
	}
}

func (h *orchHarness) waitEvents(n int) []logging.RecordedEvent {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ev := h.events.Snapshot(); len(ev) >= n {
			return ev
		}
		time.Sleep(time.Millisecond)
	}
	h.t.Fatalf("esperava %d eventos: %+v", n, h.events.Snapshot())
	return nil
}

func rejected() []adapters.DialOutcome {
	return []adapters.DialOutcome{{Err: &domain.DialError{Class: ras.ClassCredencial, Code: 691, Message: "senha"}, Fingerprint: "fp1"}}
}

// panicInCredentialBlock leva a VPN a CredencialInvalida, provoca um panic
// e espera a recriação ser agendada (com o estado de "reiniciando" publicado).
func panicInCredentialBlock(t *testing.T, w *stubWorld) *orchHarness {
	t.Helper()
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitView("Matriz", domain.CredencialInvalida)
	w.set(func(w *stubWorld) { w.panicOn = "probe" })
	if err := h.o.CheckNow("Matriz"); err != nil { // bloqueada só sonda o enlace
		t.Fatal(err)
	}
	h.waitViewWhere("Matriz", func(v ipc.VPNView) bool {
		return v.LastError != nil && v.LastError.Message == restartingText
	})
	var e *ipc.Error
	if err := h.o.CheckNow("Matriz"); !asIPC(err, &e) || e.Code != ipc.CodeInternal {
		t.Fatalf("comando durante a recriação: %v", err)
	}
	return h
}

func TestPanicInCredentialBlockDoesNotRedial(t *testing.T) {
	w := &stubWorld{network: true, outcomes: rejected()}
	h := panicInCredentialBlock(t, w)
	h.clk.Advance(5 * time.Second)
	h.waitViewWhere("Matriz", func(v ipc.VPNView) bool {
		return v.State == string(domain.CredencialInvalida) && v.LastError != nil && v.LastError.Code == 691
	})
	for range 30 {
		h.clk.Advance(time.Minute)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(w.get().dials); n != 1 {
		t.Fatalf("recriação não pode discar de novo com a credencial rejeitada: %d discagens", n)
	}
}

func TestRestartInCredentialBlockSeesNewCredential(t *testing.T) {
	w := &stubWorld{network: true, outcomes: rejected()}
	h := panicInCredentialBlock(t, w)
	w.set(func(w *stubWorld) { w.fp = "fp2" }) // cofre mudou durante a espera
	h.clk.Advance(5 * time.Second)
	h.waitView("Matriz", domain.Conectada)
	if n := len(w.get().dials); n != 2 {
		t.Fatalf("discagens = %d", n)
	}
}

func TestRepeatedPanicsTripBreaker(t *testing.T) {
	w := &stubWorld{up: true, network: true, probePanics: 3}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitEvents(1)
	if !h.clk.WaitForDeadline(5*time.Second, time.Second) {
		t.Fatal("1ª recriação em 5 s")
	}
	h.clk.Advance(5 * time.Second)
	h.waitEvents(2)
	if !h.clk.WaitForDeadline(10*time.Second, time.Second) {
		t.Fatal("2ª recriação em 10 s")
	}
	h.clk.Advance(10 * time.Second)
	ev := h.waitEvents(4)
	if len(ev) != 4 || ev[3].Level != "error" || !strings.Contains(ev[3].Msg, "suspenso") {
		t.Fatalf("Event Log: %+v", ev)
	}
	h.waitViewWhere("Matriz", func(v ipc.VPNView) bool {
		return v.State == string(domain.ErroConfig) && v.LastError != nil && v.LastError.Message == trippedText
	})
	probes := w.get().probes
	h.clk.Advance(2 * time.Minute)
	time.Sleep(50 * time.Millisecond)
	if p := w.get().probes; p != probes {
		t.Fatalf("supervisor recriado após o disjuntor: sondas %d → %d", probes, p)
	}
	if err := h.o.Reconnect("Matriz"); err != nil {
		t.Fatalf("reconnect manual deveria recriar: %v", err)
	}
	h.waitView("Matriz", domain.Conectada)
}

func TestStopDuringReloadWithHungSupervisor(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	w := &stubWorld{network: true, gate: gate} // sondas presas ignorando ctx
	// Matriz muda na recarga (a recarga espera por ela); Filial fica e é a
	// Stop que espera por ela: os prazos não podem se somar.
	h := newOrchWith(t, w, cfgWith(vpnNamed("Matriz"), vpnNamed("Filial")), config.State{},
		func(o *Options) { o.StopTimeout = time.Second })
	deadline := time.Now().Add(2 * time.Second)
	for w.get().probes < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	changed := vpnNamed("Matriz")
	changed.IntervalSeconds = 60
	applied := make(chan struct{})
	go func() {
		defer close(applied)
		h.o.applyMu.Lock()
		defer h.o.applyMu.Unlock()
		_ = h.o.apply(cfgWith(changed, vpnNamed("Filial"), vpnNamed("Backup")))
	}()
	for time.Now().Before(deadline) { // a recarga está esperando o supervisor preso
		h.o.mu.Lock()
		n := len(h.o.draining)
		h.o.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	h.o.Stop()
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("Stop com recarga concorrente levou %s (prazo 1 s)", d)
	}
	<-applied
	h.o.applyMu.Lock()
	err := h.o.apply(cfgWith(vpnNamed("Backup")))
	h.o.applyMu.Unlock()
	if err == nil {
		t.Fatal("apply após Stop deveria falhar")
	}
	h.o.mu.Lock()
	n := len(h.o.sups)
	h.o.mu.Unlock()
	if n != 0 || len(h.o.Status().VPNs) != 0 {
		t.Fatalf("supervisores relançados após Stop: %d", n)
	}
	var e *ipc.Error
	if err := h.o.CheckNow("Matriz"); !asIPC(err, &e) || e.Code != ipc.CodeInternal {
		t.Fatalf("comando após Stop: %v", err)
	}
}
