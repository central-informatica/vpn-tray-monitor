package client

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

func openTest(t *testing.T, addr string) (*session, chan Event) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 16)
	s, err := openSession(conn, "2.1.0-tray", time.Second, func(ev Event) bool { events <- ev; return true }, func(*session) {}, &counters{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	return s, events
}

func TestSessionConnectedThenSnapshotThenEvents(t *testing.T) {
	b := newBackend()
	srv := startServer(t, b)
	s, events := openTest(t, srv.addr)
	if ev := next(t, events); ev.Kind != EvConn || ev.Conn.State != Connected || ev.Conn.ServerVersion != "2.1.0-svc" {
		t.Fatalf("1º evento: %+v", ev)
	}
	if ev := next(t, events); ev.Kind != EvSnapshot || len(ev.Snapshot.VPNs) != 1 || !ev.Snapshot.Notifications {
		t.Fatalf("2º evento: %+v", ev)
	}
	b.events <- ipc.MustMessage("", ipc.TypeVPNState, ipc.VPNView{Name: "Matriz", State: "Reconectando", Attempt: 2})
	b.events <- ipc.MustMessage("", ipc.TypeNotice, ipc.NoticeEvent{VPN: "Matriz", Kind: "down", Text: "VPN Matriz caiu"})
	b.events <- ipc.MustMessage("", ipc.TypeConfigStatus, ipc.ConfigStatus{OK: false, Message: "config.json inválido"})
	b.events <- ipc.MustMessage("", "eventoDoFuturo", nil)
	// Serviço mais novo: campo que esta bandeja não conhece não derruba nada.
	b.events <- ipc.MustMessage("", ipc.TypeVPNState, map[string]any{"name": "Filial", "state": "Conectada", "campoNovo": 1})
	if ev := next(t, events); ev.Kind != EvVPNState || ev.VPN.Attempt != 2 {
		t.Fatalf("vpnState: %+v", ev)
	}
	if ev := next(t, events); ev.Kind != EvNotice || ev.Notice.Text != "VPN Matriz caiu" {
		t.Fatalf("notice: %+v", ev)
	}
	if ev := next(t, events); ev.Kind != EvConfigStatus || ev.ConfigStatus.OK {
		t.Fatalf("configStatus: %+v", ev)
	}
	if ev := next(t, events); ev.Kind != EvVPNState || ev.VPN.Name != "Filial" || ev.VPN.State != "Conectada" {
		t.Fatalf("vpnState com campo novo: %+v", ev)
	}
	// Nada vai a log em disco: as contagens aparecem em "Sobre".
	if got := s.cnt.stats(); got != (Stats{DroppedEvents: 1, UnknownFields: 1}) {
		t.Fatalf("contagens: %+v", got)
	}
}

func TestSessionCall(t *testing.T) {
	b := newBackend()
	srv := startServer(t, b)
	s, _ := openTest(t, srv.addr)
	ctx := context.Background()
	var snap ipc.Snapshot
	if err := s.call(ctx, ipc.TypeStatus, nil, &snap); err != nil || snap.VPNs[0].Name != "Matriz" {
		t.Fatalf("status: %+v %v", snap, err)
	}
	if err := s.call(ctx, ipc.TypeCheckNow, ipc.VPNRef{VPN: "Matriz"}, nil); err != nil {
		t.Fatal(err)
	}
	var e *ipc.Error
	if err := s.call(ctx, ipc.TypeReconnect, ipc.VPNRef{VPN: "Matriz"}, nil); !errors.As(err, &e) || e.Code != ipc.CodePaused {
		t.Fatalf("erro do serviço: %v", err)
	}
	var tail ipc.LogTail
	if err := s.call(ctx, ipc.TypeLogTail, ipc.LogTailRequest{MaxBytes: 1000}, &tail); err != nil || tail.Text != "fim do log\n" {
		t.Fatalf("logTail: %+v %v", tail, err)
	}
	// Chamadas concorrentes não se misturam.
	errs := make(chan error, 8)
	for range 8 {
		go func() {
			var sn ipc.Snapshot
			errs <- s.call(ctx, ipc.TypeStatus, nil, &sn)
		}()
	}
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestSessionCallEndsWhenConnectionDrops(t *testing.T) {
	b := newBackend()
	srv := startServer(t, b)
	s, _ := openTest(t, srv.addr)
	srv.stop()
	select {
	case <-s.done():
	case <-time.After(2 * time.Second):
		t.Fatal("sessão não percebeu a queda")
	}
	if err := s.call(context.Background(), ipc.TypeStatus, nil, nil); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("chamada após a queda: %v", err)
	}
}

func TestSessionCallHonoursContext(t *testing.T) {
	// Servidor que responde ao hello e ao subscribe e depois se cala.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		codec := ipc.NewCodec(c)
		for i := 0; ; i++ {
			m, err := codec.Read()
			if err != nil {
				return
			}
			switch m.Type {
			case ipc.TypeHello:
				_ = codec.Write(ipc.MustMessage(m.ID, ipc.TypeHello, ipc.Hello{Protocol: ipc.ProtocolVersion, AppVersion: "x"}))
			case ipc.TypeSubscribe:
				_ = codec.Write(ipc.MustMessage(m.ID, ipc.TypeOK, nil))
			}
		}
	}()
	s, _ := openTest(t, ln.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.call(ctx, ipc.TypeStatus, nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sem resposta: %v", err)
	}
}

// fakeHelloServer responde ao hello com reply e fecha.
func fakeHelloServer(t *testing.T, reply func(id string) ipc.Message) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			codec := ipc.NewCodec(c)
			if m, err := codec.Read(); err == nil {
				_ = codec.Write(reply(m.ID))
			}
			c.Close()
		}
	}()
	return ln.Addr().String()
}

func TestSessionIncompatible(t *testing.T) {
	cases := map[string]func(id string) ipc.Message{
		// Serviço mais novo recusa este cliente.
		"recusa do serviço": func(id string) ipc.Message {
			return ipc.ErrorMessage(id, &ipc.Error{Code: ipc.CodeIncompatible, Message: "protocolo 1 incompatível com o serviço (2); atualize o VPN Monitor"})
		},
		// Serviço de outra versão aceita, mas fala outro protocolo; o hello
		// dele pode trazer campos que este cliente não conhece.
		"hello de outra versão": func(id string) ipc.Message {
			m := ipc.MustMessage(id, ipc.TypeHello, map[string]any{"protocol": 2, "appVersion": "3.0.0", "features": []string{"x"}})
			return m
		},
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			conn, err := net.Dial("tcp", fakeHelloServer(t, reply))
			if err != nil {
				t.Fatal(err)
			}
			_, err = openSession(conn, "2.1.0-tray", time.Second, func(Event) bool { return true }, func(*session) {}, &counters{})
			var e *ipc.Error
			if !errors.As(err, &e) || e.Code != ipc.CodeIncompatible || !strings.Contains(e.Message, "atualize") {
				t.Fatalf("esperava incompatible: %v", err)
			}
		})
	}
}

func TestSessionBusy(t *testing.T) {
	addr := fakeHelloServer(t, func(string) ipc.Message {
		return ipc.ErrorMessage("", &ipc.Error{Code: ipc.CodeBusy, Message: "conexões demais"})
	})
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_, err = openSession(conn, "x", time.Second, func(Event) bool { return true }, func(*session) {}, &counters{})
	var e *ipc.Error
	if !errors.As(err, &e) || e.Code != ipc.CodeBusy {
		t.Fatalf("esperava busy: %v", err)
	}
}
