package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/adapters"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

var t0 = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// stubWorld é o "mundo" visto pelo supervisor: enlace, alvo e discador.
type stubWorld struct {
	mu        sync.Mutex
	up        bool
	network   bool
	reachable bool
	probes    int
	gate      chan struct{} // se não nil, Probe espera um valor
	handle    ras.Handle    // devolvido pela sonda (com up=false: handle preso)
	dials     []adapters.DialJob
	outcomes  []adapters.DialOutcome
	block     bool          // Dial espera o ctx (cancelamento)
	hang      chan struct{} // Dial trava ignorando o ctx até fechar
	cancelled int
	panicOn   string
	probeErr  bool // Probe devolve erro (RAS inconsultável)
	// probePanics: as próximas N sondas entram em pânico.
	probePanics int
	fp          string // impressão digital da credencial ("" = "fp1")
}

func (w *stubWorld) Probe(ctx context.Context, _ string) (adapters.LinkResult, error) {
	w.mu.Lock()
	w.probes++
	gate, p, perr := w.gate, w.panicOn, w.probeErr
	w.panicOn = "" // só uma vez
	if w.probePanics > 0 {
		w.probePanics--
		p = "probe"
	}
	w.mu.Unlock()
	if p == "probe" {
		panic("bug na sonda")
	}
	if perr {
		return adapters.LinkResult{}, errors.New("rasman parado")
	}
	if gate != nil {
		<-gate
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return adapters.LinkResult{Up: w.up, Network: w.network, Handle: w.handle}, nil
}

func (w *stubWorld) Check(context.Context) adapters.ReachResult {
	w.mu.Lock()
	defer w.mu.Unlock()
	return adapters.ReachResult{OK: w.reachable, RTT: 10 * time.Millisecond}
}

func (w *stubWorld) Dial(ctx context.Context, job adapters.DialJob) adapters.DialOutcome {
	w.mu.Lock()
	w.dials = append(w.dials, job)
	block, hang := w.block, w.hang
	var out adapters.DialOutcome
	if len(w.outcomes) > 0 {
		out, w.outcomes = w.outcomes[0], w.outcomes[1:]
	}
	w.mu.Unlock()
	if hang != nil {
		<-hang
		return adapters.DialOutcome{Cancelled: true}
	}
	if block {
		<-ctx.Done()
		w.mu.Lock()
		w.cancelled++
		w.mu.Unlock()
		return adapters.DialOutcome{Cancelled: true}
	}
	if out.Err == nil { // discagem do stub bem-sucedida: enlace e alvo de pé
		w.mu.Lock()
		w.up, w.reachable = true, true
		w.mu.Unlock()
	}
	return out
}

func (w *stubWorld) Fingerprint(context.Context, string, string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fp == "" {
		return "fp1"
	}
	return w.fp
}

func (w *stubWorld) set(f func(w *stubWorld)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	f(w)
}

func (w *stubWorld) get() stubWorld {
	w.mu.Lock()
	defer w.mu.Unlock()
	return stubWorld{probes: w.probes, dials: append([]adapters.DialJob(nil), w.dials...), cancelled: w.cancelled}
}

type harness struct {
	t       *testing.T
	clk     *shared.FakeClock
	w       *stubWorld
	sup     *Supervisor
	updates chan Update
	cancel  context.CancelFunc
	done    chan any // valor do panic, ou nil
}

func vpnConfig() config.VPN {
	return config.RawVPN{Name: "Matriz", RasEntry: "VPN Matriz",
		Check: &config.RawCheck{Kind: config.CheckPing, Host: "10.0.0.1"}}.Normalize()
}

func start(t *testing.T, w *stubWorld, initial *domain.Status) *harness {
	t.Helper()
	return startWith(t, w, initial, nil)
}

// startWith permite ajustar as Deps (ex.: StopWait curto).
func startWith(t *testing.T, w *stubWorld, initial *domain.Status, tweak func(*Deps)) *harness {
	t.Helper()
	clk := shared.NewFakeClock(t0)
	h := &harness{t: t, clk: clk, w: w, updates: make(chan Update, 1000), done: make(chan any, 1)}
	v := vpnConfig()
	st := domain.Initial(domain.ParamsFrom(v), t0, config.Pause{})
	if initial != nil {
		st = *initial
	}
	deps := Deps{
		Clock: clk, Link: w, Checker: w, Dialer: w, Queue: &DialQueue{}, Creds: w,
		OnUpdate: func(u Update) { h.updates <- u },
	}
	if tweak != nil {
		tweak(&deps)
	}
	h.sup = NewSupervisor(v, st, deps)
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() {
		defer func() { h.done <- recover() }()
		h.sup.Run(ctx)
	}()
	t.Cleanup(func() { cancel(); <-h.done })
	return h
}

// waitState lê atualizações até o estado pedido (e devolve os avisos vistos).
func (h *harness) waitState(want domain.State) (domain.Status, []domain.Notice) {
	h.t.Helper()
	var notices []domain.Notice
	timeout := time.After(2 * time.Second)
	for {
		select {
		case u := <-h.updates:
			notices = append(notices, u.Notices...)
			if u.Status.State == want && u.Status.Op == domain.OpNone {
				return u.Status, notices
			}
		case <-timeout:
			h.t.Fatalf("estado %s não chegou", want)
		}
	}
}

func TestAdoptsExistingConnectionWithoutDialing(t *testing.T) {
	w := &stubWorld{up: true, network: true, reachable: true}
	h := start(t, w, nil)
	s, _ := h.waitState(domain.Conectada)
	if s.LastRTT != 10*time.Millisecond || len(w.get().dials) != 0 {
		t.Fatalf("adoção não deve discar: %+v %v", s, w.get().dials)
	}
}

func TestDialsWhenDownAndReportsConnected(t *testing.T) {
	w := &stubWorld{network: true, reachable: true}
	h := start(t, w, nil)
	h.waitState(domain.Conectada)
	if d := w.get().dials; len(d) != 1 || d[0].HangupFirst || d[0].Timeout != time.Minute {
		t.Fatalf("discagens %+v", d)
	}
}

func TestZombieTunnelHangsUpAndRedials(t *testing.T) {
	w := &stubWorld{up: true, network: true, reachable: true}
	h := start(t, w, nil)
	h.waitState(domain.Conectada)
	w.set(func(w *stubWorld) { w.reachable = false })
	for i := 1; i <= 2; i++ {
		h.clk.Advance(30 * time.Second)
		if s, _ := h.waitState(domain.Degradada); s.Failures != i {
			t.Fatalf("falhas = %d, quer %d", s.Failures, i)
		}
	}
	// 3ª falha: túnel zumbi. O discador do stub "conecta" e o alcance volta.
	w.set(func(w *stubWorld) { w.outcomes = []adapters.DialOutcome{{}} })
	h.clk.Advance(30 * time.Second)
	s, notices := h.waitState(domain.Conectada)
	d := w.get().dials
	if len(d) != 1 || !d[0].HangupFirst {
		t.Fatalf("zumbi deve desligar e discar: %+v", d)
	}
	// Discar não prova tráfego (check ping): o "voltou" espera o 1º alcance OK.
	if len(notices) != 1 || s.LastRTT != 0 {
		t.Fatalf("após discar, antes do alcance: avisos %+v, %+v", notices, s)
	}
	h.clk.Advance(30 * time.Second)
	_, more := h.waitState(domain.Conectada)
	notices = append(notices, more...)
	if len(notices) != 2 || notices[0].Kind != domain.NoticeDown || notices[1].Kind != domain.NoticeUp {
		t.Fatalf("avisos %+v", notices)
	}
}

func TestRejectedCredentialStopsDialing(t *testing.T) {
	w := &stubWorld{network: true, outcomes: []adapters.DialOutcome{{
		Err: &domain.DialError{Class: ras.ClassCredencial, Code: 691}, Fingerprint: "fp1"}}}
	h := start(t, w, nil)
	_, notices := h.waitState(domain.CredencialInvalida)
	r, _ := h.sup.Reconnect(context.Background())
	if r.Code != domain.ReplyCredentialRejected {
		t.Fatalf("clique imediato com a mesma credencial deve ser recusado: %+v", r)
	}
	for i := 0; i < 10; i++ {
		h.clk.Advance(5 * time.Minute)
	}
	time.Sleep(20 * time.Millisecond)
	if n := len(w.get().dials); n != 1 {
		t.Fatalf("691 deve parar as tentativas; discagens = %d", n)
	}
	if len(notices) != 1 || !strings.Contains(notices[0].Text, "credencial rejeitada") {
		t.Fatalf("avisos %+v", notices)
	}
}

func TestPauseWinsOverDialInProgress(t *testing.T) {
	w := &stubWorld{network: true, block: true}
	h := start(t, w, nil)
	deadline := time.Now().Add(2 * time.Second)
	for len(w.get().dials) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	r, err := h.sup.Pause(context.Background(), t0.Add(15*time.Minute))
	if err != nil || r.Code != domain.ReplyOK {
		t.Fatal(r, err)
	}
	h.waitState(domain.Pausada)
	for w.get().cancelled == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if w.get().cancelled != 1 {
		t.Fatal("a discagem em curso deve ser cancelada (hangup)")
	}
	r, _ = h.sup.Reconnect(context.Background())
	if r.Code != domain.ReplyPaused {
		t.Fatalf("reconnect em pausa: %+v", r)
	}
}

func TestStopDuringDialCancelsIt(t *testing.T) {
	w := &stubWorld{network: true, block: true}
	h := start(t, w, nil)
	deadline := time.Now().Add(2 * time.Second)
	for len(w.get().dials) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	h.cancel()
	select {
	case <-h.done:
		h.done <- nil // para o Cleanup
	case <-time.After(2 * time.Second):
		t.Fatal("Run não voltou")
	}
	if w.get().cancelled != 1 {
		t.Fatal("parar o serviço durante a discagem deve desligá-la")
	}
	if _, err := h.sup.CheckNow(context.Background()); err != ErrStopped {
		t.Fatalf("comando após parada: %v", err)
	}
}

func TestWakesAggregateWhileProbing(t *testing.T) {
	gate := make(chan struct{})
	w := &stubWorld{up: true, network: true, reachable: true, gate: gate}
	h := start(t, w, nil)
	deadline := time.Now().Add(2 * time.Second)
	for w.get().probes == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 5; i++ {
		h.sup.Wake()
	}
	close(gate)
	h.waitState(domain.Conectada)
	time.Sleep(20 * time.Millisecond)
	if p := w.get().probes; p > 2 {
		t.Fatalf("despertares deveriam se agregar: %d sondas", p)
	}
}

func TestNoNetworkDoesNotDial(t *testing.T) {
	w := &stubWorld{network: false}
	h := start(t, w, nil)
	h.waitState(domain.SemRede)
	h.clk.Advance(30 * time.Second)
	h.waitState(domain.SemRede)
	if n := len(w.get().dials); n != 0 {
		t.Fatalf("sem rede não disca: %d", n)
	}
	w.set(func(w *stubWorld) { w.network = true; w.reachable = true })
	h.sup.Wake()
	h.waitState(domain.Conectada)
}

func TestPanicInOperationPropagatesToRun(t *testing.T) {
	w := &stubWorld{network: true, panicOn: "probe"}
	h := start(t, w, nil)
	select {
	case v := <-h.done:
		if v == nil || !strings.Contains(v.(string), "bug na sonda") {
			t.Fatalf("panic = %v", v)
		}
		h.done <- nil
	case <-time.After(2 * time.Second):
		t.Fatal("panic não chegou ao Run")
	}
}

func TestLingeringHandleHangsUpBeforeDialing(t *testing.T) {
	// A entrada aparece entre as ativas, mas não conectada: discar por cima
	// esbarraria no handle preso, então a discagem desliga antes.
	w := &stubWorld{network: true, reachable: true, handle: 7}
	h := start(t, w, nil)
	h.waitState(domain.Conectada)
	if d := w.get().dials; len(d) != 1 || !d[0].HangupFirst {
		t.Fatalf("handle preso deve sair com HangupFirst: %+v", d)
	}
}

func TestWakeDuringOperationIsNotLost(t *testing.T) {
	gate := make(chan struct{})
	w := &stubWorld{up: true, network: true, reachable: true, gate: gate}
	h := start(t, w, nil)
	deadline := time.Now().Add(2 * time.Second)
	for w.get().probes == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	h.sup.Wake() // ex.: desconexão RAS vista depois de a sonda ler o enlace
	close(gate)
	for w.get().probes < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p := w.get().probes; p != 2 {
		t.Fatalf("o despertar durante a sonda deve virar um novo ciclo: %d sondas", p)
	}
}

func TestStaleResultAfterReconnectIsDiscarded(t *testing.T) {
	gate := make(chan struct{})
	w := &stubWorld{up: true, network: true, reachable: true, gate: gate}
	h := start(t, w, nil)
	deadline := time.Now().Add(2 * time.Second)
	for w.get().probes == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	// Reconexão manual com a sonda presa (ela ignora o ctx): a discagem
	// nova conecta antes de a sonda velha voltar.
	if r, err := h.sup.Reconnect(context.Background()); err != nil || r.Code != domain.ReplyOK {
		t.Fatal(r, err)
	}
	h.waitState(domain.Conectada)
	// A sonda velha volta dizendo "sem rede": é de outra geração, descartada.
	w.set(func(w *stubWorld) { w.network = false })
	close(gate)
	time.Sleep(20 * time.Millisecond)
	for {
		select {
		case u := <-h.updates:
			if u.Status.State != domain.Conectada {
				t.Fatalf("resultado de operação cancelada mudou o estado: %+v", u.Status)
			}
			continue
		default:
		}
		break
	}
	if d := w.get().dials; len(d) != 1 || !d[0].HangupFirst {
		t.Fatalf("discagens %+v", d)
	}
}

func TestProbeErrorWhileUpDoesNotReportDown(t *testing.T) {
	w := &stubWorld{up: true, network: true, reachable: true}
	h := start(t, w, nil)
	h.waitState(domain.Conectada)
	// RAS inconsultável com a VPN de pé: o alcance decide, sem "caiu" nem discagem.
	w.set(func(w *stubWorld) { w.probeErr = true })
	h.clk.Advance(30 * time.Second)
	s, notices := h.waitState(domain.Conectada)
	if len(notices) != 0 || len(w.get().dials) != 0 || s.LastCheck != t0.Add(30*time.Second) {
		t.Fatalf("erro de sonda em Conectada: avisos %+v, discagens %+v, %+v", notices, w.get().dials, s)
	}
	// Alvo mudo também: vira Degradada pelo alcance, não queda.
	w.set(func(w *stubWorld) { w.reachable = false })
	h.clk.Advance(30 * time.Second)
	if _, notices := h.waitState(domain.Degradada); len(notices) != 0 || len(w.get().dials) != 0 {
		t.Fatalf("avisos %+v, discagens %+v", notices, w.get().dials)
	}
}

func TestProbeErrorWhileDownDials(t *testing.T) {
	w := &stubWorld{network: true, reachable: true, probeErr: true}
	h := start(t, w, nil)
	h.waitState(domain.Conectada)
	if d := w.get().dials; len(d) != 1 {
		t.Fatalf("erro de sonda com a VPN caída deve discar: %+v", d)
	}
}

func TestStopDoesNotWaitForeverOnHungOperation(t *testing.T) {
	hang := make(chan struct{})
	defer close(hang)
	w := &stubWorld{network: true, hang: hang} // discador preso ignorando o ctx
	h := startWith(t, w, nil, func(d *Deps) { d.StopWait = 50 * time.Millisecond })
	deadline := time.Now().Add(2 * time.Second)
	for len(w.get().dials) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	h.cancel()
	select {
	case <-h.done:
		h.done <- nil // para o Cleanup
	case <-time.After(time.Second):
		t.Fatal("Run deve abandonar a operação presa após StopWait")
	}
}
