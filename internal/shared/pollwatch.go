package shared

import (
	"context"
	"time"
)

// PollWatcher observa algo por amostragem: a cada Interval chama Probe, que
// devolve uma impressão digital (hash do conteúdo, listagem de pasta...).
// Quando a impressão muda e fica estável por Quiet, envia um aviso em C.
// Amostragem evita depender de ReadDirectoryChangesW e roda igual no Linux.
type PollWatcher struct {
	Clock    Clock
	Interval time.Duration
	Quiet    time.Duration
	Probe    func() string

	c chan struct{}
}

// NewPollWatcher prepara o observador; chame Run numa goroutine.
func NewPollWatcher(clock Clock, interval, quiet time.Duration, probe func() string) *PollWatcher {
	return &PollWatcher{Clock: clock, Interval: interval, Quiet: quiet, Probe: probe, c: make(chan struct{}, 1)}
}

// C entrega um aviso por rajada de mudanças (avisos pendentes se agregam).
func (w *PollWatcher) C() <-chan struct{} { return w.c }

// Run amostra até ctx terminar. A primeira amostra é a linha de base.
func (w *PollWatcher) Run(ctx context.Context) {
	last := w.Probe()
	var changedAt time.Time
	pending := false
	t := w.Clock.NewTimer(w.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
		}
		now := w.Clock.Now()
		if fp := w.Probe(); fp != last {
			last, changedAt, pending = fp, now, true
		} else if pending && now.Sub(changedAt) >= w.Quiet {
			pending = false
			select {
			case w.c <- struct{}{}:
			default:
			}
		}
		t.Reset(w.Interval)
	}
}
