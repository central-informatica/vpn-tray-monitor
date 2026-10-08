// Package service tem o supervisor (ator por VPN), a fila global de
// discagem e o orquestrador.
package service

import (
	"context"
	"sync"
)

// DialQueue garante uma discagem por vez entre todas as VPNs (§4.4).
// Pedidos manuais passam na frente dos automáticos; dentro de cada classe,
// ordem de chegada. Manuais contínuos podem atrasar indefinidamente os
// automáticos (aceito: pedido manual é raro).
type DialQueue struct {
	mu     sync.Mutex
	busy   bool
	manual []*queueWaiter
	auto   []*queueWaiter
}

type queueWaiter struct {
	ch      chan struct{}
	granted bool
}

// Acquire espera a vez. Devolve a função que libera a fila (idempotente).
func (q *DialQueue) Acquire(ctx context.Context, manual bool) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q.mu.Lock()
	if !q.busy {
		q.busy = true
		q.mu.Unlock()
		return q.releaser(), nil
	}
	w := &queueWaiter{ch: make(chan struct{})}
	if manual {
		q.manual = append(q.manual, w)
	} else {
		q.auto = append(q.auto, w)
	}
	q.mu.Unlock()

	select {
	case <-w.ch:
		return q.releaser(), nil
	case <-ctx.Done():
		q.mu.Lock()
		if w.granted { // ganhou a vez no mesmo instante: repassa
			q.mu.Unlock()
			q.releaser()()
			return nil, ctx.Err()
		}
		q.manual = remove(q.manual, w)
		q.auto = remove(q.auto, w)
		q.mu.Unlock()
		return nil, ctx.Err()
	}
}

func remove(list []*queueWaiter, w *queueWaiter) []*queueWaiter {
	for i, x := range list {
		if x == w {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}

func (q *DialQueue) releaser() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			q.mu.Lock()
			defer q.mu.Unlock()
			var next *queueWaiter
			switch {
			case len(q.manual) > 0:
				next, q.manual = q.manual[0], q.manual[1:]
			case len(q.auto) > 0:
				next, q.auto = q.auto[0], q.auto[1:]
			}
			if next == nil {
				q.busy = false
				return
			}
			next.granted = true
			close(next.ch)
		})
	}
}
