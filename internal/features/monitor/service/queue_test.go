package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// esperaFila espera, de forma determinística, a fila ter o nº de esperando.
func esperaFila(t *testing.T, q *DialQueue, auto, manual int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		ok := len(q.auto) == auto && len(q.manual) == manual
		q.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("fila não chegou a auto=%d manual=%d", auto, manual)
}

func ctxTeste(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestDialQueueOrderManualFirst(t *testing.T) {
	var q DialQueue
	release, err := q.Acquire(ctxTeste(t), false)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup
	nAuto, nManual := 0, 0
	start := func(name string, manual bool) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := q.Acquire(ctxTeste(t), manual)
			if err != nil {
				t.Errorf("Acquire %s: %v", name, err)
				return
			}
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			r()
		}()
		if manual {
			nManual++
		} else {
			nAuto++
		}
		esperaFila(t, &q, nAuto, nManual)
	}
	start("auto1", false)
	start("auto2", false)
	start("manual", true)
	release()
	release() // idempotente
	wg.Wait()
	want := []string{"manual", "auto1", "auto2"}
	if len(order) != len(want) {
		t.Fatalf("ordem %v, quer %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("ordem %v, quer %v", order, want)
		}
	}
}

func TestDialQueueCancelLeavesQueue(t *testing.T) {
	var q DialQueue
	release, err := q.Acquire(ctxTeste(t), false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := q.Acquire(ctx, false); errc <- err }()
	esperaFila(t, &q, 1, 0)
	cancel()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("esperava erro de cancelamento")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire cancelado não retornou")
	}
	release()
	r, err := q.Acquire(ctxTeste(t), false) // fila livre de novo
	if err != nil {
		t.Fatal(err)
	}
	r()
}

func TestDialQueueCtxJaCanceladoComFilaLivre(t *testing.T) {
	var q DialQueue
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r, err := q.Acquire(ctx, false); err == nil {
		r()
		t.Fatal("esperava ctx.Err() com ctx já cancelado")
	}
	r, err := q.Acquire(ctxTeste(t), false) // não pode ter ficado ocupada
	if err != nil {
		t.Fatal(err)
	}
	r()
}

func TestDialQueueEstresse(t *testing.T) {
	var q DialQueue
	var holders atomic.Int32
	var wg sync.WaitGroup
	const n = 2000
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			if i%2 == 0 {
				c, cancel := context.WithTimeout(ctx, time.Duration(i%7)*time.Microsecond)
				defer cancel()
				ctx = c
			} else {
				c, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				ctx = c
			}
			r, err := q.Acquire(ctx, i%3 == 0)
			if err != nil {
				return
			}
			if holders.Add(1) > 1 {
				t.Error("mais de um detentor ao mesmo tempo")
			}
			holders.Add(-1)
			r()
			r()
		}()
	}
	wg.Wait()
	r, err := q.Acquire(ctxTeste(t), false)
	if err != nil {
		t.Fatalf("fila não ficou livre: %v", err)
	}
	r()
}
