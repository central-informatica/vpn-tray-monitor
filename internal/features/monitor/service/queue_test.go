package service

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestDialQueueOrderManualFirst(t *testing.T) {
	var q DialQueue
	release, err := q.Acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup
	start := func(name string, manual bool) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := q.Acquire(context.Background(), manual)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			r()
		}()
		time.Sleep(10 * time.Millisecond) // garante a ordem de chegada
	}
	start("auto1", false)
	start("auto2", false)
	start("manual", true)
	release()
	release() // idempotente
	wg.Wait()
	want := []string{"manual", "auto1", "auto2"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("ordem %v, quer %v", order, want)
		}
	}
}

func TestDialQueueCancelLeavesQueue(t *testing.T) {
	var q DialQueue
	release, _ := q.Acquire(context.Background(), false)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error)
	go func() { _, err := q.Acquire(ctx, false); errc <- err }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-errc; err == nil {
		t.Fatal("esperava erro de cancelamento")
	}
	release()
	r, err := q.Acquire(context.Background(), false) // fila livre de novo
	if err != nil {
		t.Fatal(err)
	}
	r()
}
