package svc

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu     sync.Mutex
	states []State
}

func (r *recorder) report(s State) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, s)
}

func (r *recorder) get() []State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]State(nil), r.states...)
}

func TestLoopStopCancelsRun(t *testing.T) {
	var rec recorder
	reqs := make(chan Request)
	resumed := make(chan struct{}, 1)
	h := Hooks{
		Run:      func(ctx context.Context) error { <-ctx.Done(); return nil },
		OnResume: func() { resumed <- struct{}{} },
	}
	code := make(chan uint32)
	go func() { code <- Loop(h, reqs, rec.report) }()

	reqs <- Request{Cmd: CmdPowerEvent, EventType: 0x4} // suspensão: ignora
	reqs <- Request{Cmd: CmdPowerEvent, EventType: PBT_APMRESUMEAUTOMATIC}
	select {
	case <-resumed:
	case <-time.After(time.Second):
		t.Fatal("OnResume não chamado")
	}
	reqs <- Request{Cmd: CmdStop}
	if c := <-code; c != 0 {
		t.Fatalf("código %d", c)
	}
	want := []State{StateStartPending, StateRunning, StateStopPending}
	if got := rec.get(); !reflect.DeepEqual(got, want) {
		t.Fatalf("estados %v, quer %v", got, want)
	}
}

func TestLoopPreShutdownAndTimeout(t *testing.T) {
	var rec recorder
	reqs := make(chan Request, 1)
	h := Hooks{
		Run:         func(ctx context.Context) error { select {} },
		StopTimeout: 20 * time.Millisecond,
	}
	reqs <- Request{Cmd: CmdPreShutdown}
	if c := Loop(h, reqs, rec.report); c != 0 {
		t.Fatalf("parada pedida que estoura o prazo deve dar código 0, veio %d", c)
	}
}

func TestLoopSuspendDoesNotCallOnResume(t *testing.T) {
	var rec recorder
	reqs := make(chan Request)
	called := make(chan struct{}, 2)
	h := Hooks{
		Run:      func(ctx context.Context) error { <-ctx.Done(); return nil },
		OnResume: func() { called <- struct{}{} },
	}
	code := make(chan uint32)
	go func() { code <- Loop(h, reqs, rec.report) }()
	reqs <- Request{Cmd: CmdPowerEvent, EventType: 0x4}
	reqs <- Request{Cmd: CmdStop}
	<-code
	select {
	case <-called:
		t.Fatal("evento 0x4 (suspensão) não deve chamar OnResume")
	default:
	}
}

func TestLoopBlockingOnResumeDoesNotBlockStop(t *testing.T) {
	var rec recorder
	reqs := make(chan Request)
	h := Hooks{
		Run:         func(ctx context.Context) error { <-ctx.Done(); return nil },
		OnResume:    func() { select {} },
		StopTimeout: time.Second,
	}
	code := make(chan uint32)
	go func() { code <- Loop(h, reqs, rec.report) }()
	reqs <- Request{Cmd: CmdPowerEvent, EventType: PBT_APMRESUMEAUTOMATIC}
	reqs <- Request{Cmd: CmdStop}
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("código %d", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OnResume bloqueado travou o laço")
	}
}

func TestLoopRunFailsAlone(t *testing.T) {
	var rec recorder
	h := Hooks{Run: func(context.Context) error { return errors.New("pipe ocupado") }}
	if c := Loop(h, make(chan Request), rec.report); c != 1 {
		t.Fatalf("código %d", c)
	}
}

// A política do SCM é contrato com o spec (§9) e com o roteiro e2e, que a
// confere no registro depois de instalar o MSI.
func TestPolicyValues(t *testing.T) {
	want := []time.Duration{5 * time.Second, 30 * time.Second, 60 * time.Second}
	if got := RecoveryDelays(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RecoveryDelays %v", got)
	}
	if RecoveryReset != 24*time.Hour || PreshutdownTimeout != 15*time.Second {
		t.Fatalf("reset %s, preshutdown %s", RecoveryReset, PreshutdownTimeout)
	}
	if got := Dependencies(); !reflect.DeepEqual(got, []string{"RasMan"}) {
		t.Fatalf("Dependencies %v", got)
	}
	// Quem chama não altera a política de todos.
	RecoveryDelays()[0] = 0
	Dependencies()[0] = "x"
	if RecoveryDelays()[0] != 5*time.Second || Dependencies()[0] != "RasMan" {
		t.Fatal("política mutável por quem chama")
	}
}

func TestDiffPolicy(t *testing.T) {
	want := WantedPolicy()
	if len(want.Actions) != 3 || !want.Actions[0].Restart || want.Actions[2].Delay != 60*time.Second ||
		want.Reset != 24*time.Hour || !want.NonCrash || want.Preshutdown != 15*time.Second {
		t.Fatalf("WantedPolicy %+v", want)
	}
	mod := func(f func(*Policy)) Policy {
		p := WantedPolicy()
		f(&p)
		return p
	}
	cases := []struct {
		name string
		cur  Policy
		want PolicyChanges
	}{
		{"igual: nada a gravar (não zera a contagem de falhas)", WantedPolicy(), PolicyChanges{}},
		{"serviço recém-registrado (MSI)", Policy{}, PolicyChanges{true, true, true}},
		{"espera diferente", mod(func(p *Policy) { p.Actions[1].Delay = 10 * time.Second }), PolicyChanges{Recovery: true}},
		{"ação que não reinicia", mod(func(p *Policy) { p.Actions[2].Restart = false }), PolicyChanges{Recovery: true}},
		{"ação a mais", mod(func(p *Policy) { p.Actions = append(p.Actions, RecoveryStep{true, time.Minute}) }), PolicyChanges{Recovery: true}},
		{"reset diferente", mod(func(p *Policy) { p.Reset = 0 }), PolicyChanges{Recovery: true}},
		{"sem falha sem crash", mod(func(p *Policy) { p.NonCrash = false }), PolicyChanges{NonCrash: true}},
		{"preshutdown padrão (3 min)", mod(func(p *Policy) { p.Preshutdown = 3 * time.Minute }), PolicyChanges{Preshutdown: true}},
	}
	for _, c := range cases {
		got := DiffPolicy(c.cur, WantedPolicy())
		if got != c.want || got.Any() != (c.want != PolicyChanges{}) {
			t.Errorf("%s: %+v, quer %+v", c.name, got, c.want)
		}
	}
}
