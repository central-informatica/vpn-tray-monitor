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
