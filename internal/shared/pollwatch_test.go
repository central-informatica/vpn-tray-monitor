package shared

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestPollWatcherDebounces(t *testing.T) {
	clk := NewFakeClock(t0)
	var value atomic.Value
	value.Store("a")
	w := NewPollWatcher(clk, 250*time.Millisecond, time.Second, func() string { return value.Load().(string) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	// step avança um período e espera o observador processar a amostra
	// (ele rearma o temporizador ao terminar), para o teste não correr com ele.
	armed := func() {
		t.Helper()
		if !clk.WaitForTimers(1, time.Second) {
			t.Fatal("observador não armou o temporizador")
		}
	}
	step := func() {
		t.Helper()
		clk.Advance(250 * time.Millisecond)
		armed()
	}
	armed()
	expectNone := func() {
		t.Helper()
		select {
		case <-w.C():
			t.Fatal("aviso antes da hora")
		case <-time.After(20 * time.Millisecond):
		}
	}

	step() // sem mudança
	expectNone()
	value.Store("b")
	step() // mudança vista
	value.Store("c")
	step() // nova mudança reinicia a espera
	for i := 0; i < 3; i++ {
		step()
		expectNone()
	}
	step() // 1s estável
	select {
	case <-w.C():
	case <-time.After(time.Second):
		t.Fatal("aviso não chegou")
	}
	step()
	expectNone()
}
