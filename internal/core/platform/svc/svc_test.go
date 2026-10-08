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
	if c := Loop(h, reqs, rec.report); c != 1 {
		t.Fatalf("Run travado deve dar código 1, veio %d", c)
	}
}

func TestLoopRunFailsAlone(t *testing.T) {
	var rec recorder
	h := Hooks{Run: func(context.Context) error { return errors.New("pipe ocupado") }}
	if c := Loop(h, make(chan Request), rec.report); c != 1 {
		t.Fatalf("código %d", c)
	}
}
