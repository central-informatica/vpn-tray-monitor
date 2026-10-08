package shared

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func TestFakeClockFiresInOrderOnAdvance(t *testing.T) {
	c := NewFakeClock(t0)
	a := c.NewTimer(10 * time.Second)
	b := c.NewTimer(5 * time.Second)

	c.Advance(5 * time.Second)
	select {
	case got := <-b.C():
		if !got.Equal(t0.Add(5 * time.Second)) {
			t.Fatalf("b disparou em %v", got)
		}
	default:
		t.Fatal("b deveria ter disparado")
	}
	select {
	case <-a.C():
		t.Fatal("a não deveria ter disparado")
	default:
	}
	c.Advance(5 * time.Second)
	select {
	case <-a.C():
	default:
		t.Fatal("a deveria ter disparado")
	}
	if n := c.ActiveTimers(); n != 0 {
		t.Fatalf("ActiveTimers = %d, quer 0", n)
	}
}

func TestFakeClockStopAndReset(t *testing.T) {
	c := NewFakeClock(t0)
	tm := c.NewTimer(time.Second)
	if !tm.Stop() {
		t.Fatal("Stop de temporizador armado deve devolver true")
	}
	c.Advance(2 * time.Second)
	select {
	case <-tm.C():
		t.Fatal("temporizador parado disparou")
	default:
	}
	tm.Reset(3 * time.Second)
	c.Advance(2 * time.Second)
	select {
	case <-tm.C():
		t.Fatal("disparou antes do prazo")
	default:
	}
	c.Advance(time.Second)
	select {
	case <-tm.C():
	default:
		t.Fatal("Reset não rearmou")
	}
}

func TestFakeClockSuspendMovesOnlyWall(t *testing.T) {
	c := NewFakeClock(t0)
	tm := c.NewTimer(time.Minute)
	c.Suspend(time.Hour)
	if got := c.Now(); !got.Equal(t0.Add(time.Hour)) {
		t.Fatalf("Now = %v", got)
	}
	select {
	case <-tm.C():
		t.Fatal("suspensão não deve disparar temporizadores")
	default:
	}
}

func TestFakeClockWaitForTimers(t *testing.T) {
	c := NewFakeClock(t0)
	go func() {
		time.Sleep(10 * time.Millisecond)
		c.NewTimer(time.Second)
	}()
	if !c.WaitForTimers(1, time.Second) {
		t.Fatal("WaitForTimers não viu o temporizador")
	}
	if c.WaitForTimers(2, 20*time.Millisecond) {
		t.Fatal("WaitForTimers deveria esgotar o prazo")
	}
}

func TestFakeClockWaitForDeadline(t *testing.T) {
	c := NewFakeClock(t0)
	tm := c.NewTimer(5 * time.Second)
	c.Advance(time.Second)
	if !c.WaitForDeadline(4*time.Second, 10*time.Millisecond) {
		t.Fatal("deveria ver o prazo restante de 4s")
	}
	go tm.Reset(10 * time.Second)
	if !c.WaitForDeadline(10*time.Second, time.Second) {
		t.Fatal("deveria ver o Reset feito por outra goroutine")
	}
}

func TestResumeDetectorIgnoresMonotonicReading(t *testing.T) {
	d := ResumeDetector{Period: 5 * time.Second, Threshold: time.Minute}
	now := time.Now() // com leitura monotônica, como o RealClock devolve
	d.Observe(now)
	if strings.Contains(d.last.String(), "m=") {
		t.Fatalf("o detector deve guardar só a parede, guardou %s", d.last)
	}
	// Após uma suspensão real, a parede pulou e a leitura monotônica não; um
	// tempo só-parede no futuro reproduz isso para a comparação feita.
	if !d.Observe(now.Round(0).Add(2 * time.Hour)) {
		t.Fatal("salto de parede de 2 h deveria ser detectado")
	}
	if d.Observe(time.Now().Add(2*time.Hour + 5*time.Second)) {
		t.Fatal("tique normal (com leitura monotônica) após o salto não é salto")
	}
}

func TestResumeDetector(t *testing.T) {
	d := ResumeDetector{Period: 5 * time.Second, Threshold: 60 * time.Second}
	if d.Observe(t0) {
		t.Fatal("primeira observação nunca é salto")
	}
	if d.Observe(t0.Add(5 * time.Second)) {
		t.Fatal("tique normal não é salto")
	}
	if d.Observe(t0.Add(70 * time.Second)) {
		t.Fatal("65s com limite de 5+60 não é salto")
	}
	if !d.Observe(t0.Add(70*time.Second + 66*time.Second)) {
		t.Fatal("66s depois deveria ser salto")
	}
	// Relógio de parede ajustado para trás (NTP, ajuste manual): não é retomada.
	if d.Observe(t0.Add(-time.Hour)) {
		t.Fatal("relógio voltando não é salto de suspensão")
	}
	if d.Observe(t0.Add(-time.Hour + 5*time.Second)) {
		t.Fatal("tique normal após o ajuste não é salto")
	}
}
