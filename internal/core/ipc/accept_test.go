package ipc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
)

// syncBuffer é um bytes.Buffer seguro para o handler do slog e o teste.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s.b.String()), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Falhas de Accept seguidas: a 1ª vai ao log, depois no máximo uma linha
// por minuto com a contagem; ~1 min de falhas contínuas avisa o Event Log
// uma vez; voltar a aceitar registra Info (e Info no Event Log, se avisou).
func TestAcceptTroubleRateLimitsLog(t *testing.T) {
	var buf syncBuffer
	ev := &logging.RecordingSink{}
	at := acceptTrouble{log: slog.New(slog.NewTextHandler(&buf, nil)), events: ev}
	t0 := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	boom := errors.New("ERROR_NO_SYSTEM_RESOURCES")

	at.failed(t0, boom)
	if n := len(buf.lines()); n != 1 {
		t.Fatalf("1ª falha: %d linhas", n)
	}
	for s := 1; s < 60; s++ {
		at.failed(t0.Add(time.Duration(s)*time.Second), boom)
	}
	if n := len(buf.lines()); n != 1 || len(ev.Snapshot()) != 0 {
		t.Fatalf("falhas dentro do 1º minuto não geram linhas nem evento: %d linhas %+v", n, ev.Snapshot())
	}
	at.failed(t0.Add(time.Minute), boom)
	l := buf.lines()
	if len(l) != 2 || !strings.Contains(l[1], "falhas=60") {
		t.Fatalf("resumo do minuto com a contagem: %q", l)
	}
	if e := ev.Snapshot(); len(e) != 1 || e[0].Level != "warning" || !strings.Contains(e[0].Msg, "pipe") {
		t.Fatalf("Event Log após ~1 min: %+v", e)
	}
	for s := 61; s <= 120; s++ {
		at.failed(t0.Add(time.Duration(s)*time.Second), boom)
	}
	if l := buf.lines(); len(l) != 3 || !strings.Contains(l[2], "falhas=60") || len(ev.Snapshot()) != 1 {
		t.Fatalf("2º minuto: %q %+v", l, ev.Snapshot())
	}
	at.recovered(t0.Add(125 * time.Second))
	l = buf.lines()
	if len(l) != 4 || !strings.Contains(l[3], "level=INFO") || !strings.Contains(l[3], "falhas=121") {
		t.Fatalf("recuperação: %q", l)
	}
	if e := ev.Snapshot(); len(e) != 2 || e[1].Level != "info" {
		t.Fatalf("Event Log da recuperação: %+v", e)
	}
	at.recovered(t0.Add(126 * time.Second)) // sem falha pendente: nada
	if n := len(buf.lines()); n != 4 {
		t.Fatalf("recuperação sem falha registrou: %d", n)
	}
	// Falha curta depois: log de novo, sem Event Log.
	at.failed(t0.Add(130*time.Second), boom)
	at.recovered(t0.Add(131 * time.Second))
	if n := len(buf.lines()); n != 6 || len(ev.Snapshot()) != 2 {
		t.Fatalf("falha curta: %d linhas %+v", n, ev.Snapshot())
	}
}

// Integração: rajada curta de falhas no Serve gera uma linha de falha e uma
// de recuperação, não uma por falha.
func TestServerAcceptErrorsLogOnce(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var buf syncBuffer
	g := &glitchListener{Listener: ln}
	s := &Server{Backend: &fakeBackend{}, AppVersion: "2.0.0-teste", HandshakeTimeout: time.Second, WriteTimeout: time.Second,
		Log: slog.New(slog.NewTextHandler(&buf, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, g) }()
	t.Cleanup(func() { cancel(); <-done })
	g.arm(5)
	first := dial(t, ln.Addr().String()) // libera o Accept pendente
	if err := first.Call(TypeStatus, nil, nil); err != nil {
		t.Fatal(err)
	}
	after := dial(t, ln.Addr().String())
	if err := after.Call(TypeStatus, nil, nil); err != nil {
		t.Fatal(err)
	}
	var fails, back int
	for _, l := range buf.lines() {
		switch {
		case strings.Contains(l, "falha ao aceitar"):
			fails++
		case strings.Contains(l, "voltou a aceitar"):
			back++
		}
	}
	if fails != 1 || back != 1 {
		t.Fatalf("linhas de falha=%d recuperação=%d: %q", fails, back, buf.lines())
	}
}
