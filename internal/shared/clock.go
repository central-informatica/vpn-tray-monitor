// Package shared reúne utilitários sem dependência de SO usados por todas as
// camadas: relógio, backoff, segredo, gravação atômica e observação de arquivos.
package shared

import (
	"sort"
	"sync"
	"time"
)

// Clock abstrai o tempo para que a lógica de espera seja testável sem dormir.
type Clock interface {
	// Now devolve o relógio de parede.
	Now() time.Time
	// NewTimer cria um temporizador que dispara uma vez após d.
	NewTimer(d time.Duration) Timer
}

// Timer é o subconjunto de *time.Timer que o código usa.
type Timer interface {
	C() <-chan time.Time
	// Stop desarma; devolve false se já tinha disparado ou parado.
	Stop() bool
	// Reset rearma para disparar após d.
	Reset(d time.Duration)
}

// RealClock usa o pacote time.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time   { return r.t.C }
func (r realTimer) Stop() bool            { return r.t.Stop() }
func (r realTimer) Reset(d time.Duration) { r.t.Reset(d) }

// FakeClock é um relógio manual para testes. Ele separa o relógio de parede
// do tempo que os temporizadores enxergam, para simular suspensão: Advance
// move os dois; Suspend move só o de parede. Now devolve tempos sem leitura
// monotônica (só parede), que é o que ResumeDetector compara.
type FakeClock struct {
	mu      sync.Mutex
	wall    time.Time
	mono    time.Duration
	timers  []*fakeTimer
	changed chan struct{}
}

// NewFakeClock cria um relógio parado em start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{wall: start, changed: make(chan struct{})}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wall
}

func (c *FakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, ch: make(chan time.Time, 1), deadline: c.mono + d, active: true}
	c.timers = append(c.timers, t)
	if d <= 0 {
		c.fireLocked()
	}
	c.notifyLocked()
	return t
}

// Advance avança parede e monotônico e dispara, em ordem, os temporizadores vencidos.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mono += d
	c.wall = c.wall.Add(d)
	c.fireLocked()
	c.notifyLocked()
}

// Suspend avança só o relógio de parede, como numa suspensão da máquina.
func (c *FakeClock) Suspend(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
	c.notifyLocked()
}

// ActiveTimers conta os temporizadores armados e ainda não disparados.
func (c *FakeClock) ActiveTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if t.active {
			n++
		}
	}
	return n
}

// WaitForTimers bloqueia até haver pelo menos n temporizadores armados ou o
// prazo real esgotar. Serve para sincronizar o teste com uma goroutine que
// ainda vai armar o próximo temporizador.
func (c *FakeClock) WaitForTimers(n int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		c.mu.Lock()
		count := 0
		for _, t := range c.timers {
			if t.active {
				count++
			}
		}
		ch := c.changed
		c.mu.Unlock()
		if count >= n {
			return true
		}
		select {
		case <-ch:
		case <-deadline:
			return false
		}
	}
}

// WaitForDeadline bloqueia até existir um temporizador armado que dispare
// exatamente daqui a rel (no relógio dos temporizadores), ou o prazo real
// esgotar. Sincroniza o teste com um Reset feito por outra goroutine.
func (c *FakeClock) WaitForDeadline(rel time.Duration, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		c.mu.Lock()
		found := false
		for _, t := range c.timers {
			if t.active && t.deadline-c.mono == rel {
				found = true
			}
		}
		ch := c.changed
		c.mu.Unlock()
		if found {
			return true
		}
		select {
		case <-ch:
		case <-deadline:
			return false
		}
	}
}

func (c *FakeClock) fireLocked() {
	sort.SliceStable(c.timers, func(i, j int) bool { return c.timers[i].deadline < c.timers[j].deadline })
	keep := c.timers[:0]
	for _, t := range c.timers {
		if t.active && t.deadline <= c.mono {
			t.active = false
			select {
			case t.ch <- c.wall:
			default:
			}
			continue
		}
		if t.active {
			keep = append(keep, t)
		}
	}
	c.timers = keep
}

func (c *FakeClock) notifyLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

type fakeTimer struct {
	clock    *FakeClock
	ch       chan time.Time
	deadline time.Duration
	active   bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	was := t.active
	t.active = false
	c.notifyLocked()
	return was
}

func (t *fakeTimer) Reset(d time.Duration) {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	select { // descarta disparo antigo não lido
	case <-t.ch:
	default:
	}
	t.deadline = c.mono + d
	if !t.active {
		t.active = true
		c.timers = append(c.timers, t)
	}
	if d <= 0 {
		c.fireLocked()
	}
	c.notifyLocked()
}

// ResumeDetector percebe que a máquina ficou suspensa: o tique periódico
// (medido pelo relógio monotônico dos temporizadores) chega e o relógio de
// parede avançou bem mais que o período. Cobre o modern standby, que nem
// sempre avisa o serviço com PBT_APMRESUMEAUTOMATIC.
//
// A comparação é só de parede: time.Now() carrega também a leitura
// monotônica, e t.Sub(u) usaria ela — que não anda durante a suspensão —
// escondendo justamente o salto. Por isso Observe descarta a leitura
// monotônica com Round(0).
type ResumeDetector struct {
	Period    time.Duration // período do tique
	Threshold time.Duration // folga além do período que caracteriza suspensão
	last      time.Time
}

// Observe registra um tique no instante de parede now e diz se houve salto.
func (d *ResumeDetector) Observe(now time.Time) bool {
	now = now.Round(0) // só relógio de parede
	if d.last.IsZero() {
		d.last = now
		return false
	}
	elapsed := now.Sub(d.last)
	d.last = now
	return elapsed > d.Period+d.Threshold
}
