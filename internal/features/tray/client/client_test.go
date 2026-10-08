package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// fastOptions reconecta em milissegundos.
func fastOptions(dial func(context.Context) (net.Conn, error)) Options {
	return Options{Dial: dial, AppVersion: "2.1.0-tray", Timeout: time.Second,
		Backoff: shared.Backoff{Base: 5 * time.Millisecond, Max: 20 * time.Millisecond, Jitter: 0.2}}
}

func runClient(t *testing.T, o Options) *Client {
	t.Helper()
	c := New(o)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		for range c.Events() { // drena até Run fechar o canal
		}
		<-done
	})
	return c
}

// nextConn pula eventos até um de conexão.
func nextConn(t *testing.T, ch <-chan Event) Conn {
	t.Helper()
	for {
		if ev := next(t, ch); ev.Kind == EvConn {
			return ev.Conn
		}
	}
}

// waitConn espera um evento de conexão com o estado dado.
func waitConn(t *testing.T, ch <-chan Event, want ConnState) Conn {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cn := nextConn(t, ch); cn.State == want {
			return cn
		}
	}
	t.Fatalf("estado %v não chegou", want)
	return Conn{}
}

func TestClientConnectsAndCalls(t *testing.T) {
	srv := startServer(t, newBackend())
	c := New(fastOptions(dialTCP(srv.addr)))
	if err := c.Call(context.Background(), ipc.TypeStatus, nil, nil); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("antes de conectar: %v", err)
	}
	c = runClient(t, fastOptions(dialTCP(srv.addr)))
	if cn := nextConn(t, c.Events()); cn.State != Connected || cn.ServerVersion != "2.1.0-svc" {
		t.Fatalf("conexão: %+v", cn)
	}
	if ev := next(t, c.Events()); ev.Kind != EvSnapshot {
		t.Fatalf("depois de conectar vem o snapshot: %+v", ev)
	}
	var snap ipc.Snapshot
	if err := c.Call(context.Background(), ipc.TypeStatus, nil, &snap); err != nil || snap.VPNs[0].Name != "Matriz" {
		t.Fatalf("status: %+v %v", snap, err)
	}
	if got := c.Stats(); got != (Stats{}) {
		t.Fatalf("nada descartado com serviço da mesma versão: %+v", got)
	}
}

// addr muda de servidor durante o teste (serviço reiniciado).
type addr struct{ v atomic.Value }

func (a *addr) set(s string) { a.v.Store(s) }
func (a *addr) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", a.v.Load().(string))
}

func TestClientReconnectsAfterServiceRestart(t *testing.T) {
	b1 := newBackend()
	srv1 := startServer(t, b1)
	var a addr
	a.set(srv1.addr)
	c := runClient(t, fastOptions(a.dial))
	waitConn(t, c.Events(), Connected)

	b1.events <- ipc.MustMessage("", ipc.TypeServiceStopping, struct{}{})
	if cn := waitConn(t, c.Events(), Stopping); !strings.Contains(cn.Message, "parando") {
		t.Fatalf("parando: %+v", cn)
	}
	srv1.stop()
	if cn := waitConn(t, c.Events(), Unavailable); !strings.Contains(cn.Message, "parou") {
		t.Fatalf("depois da parada: %+v", cn)
	}
	if err := c.Call(context.Background(), ipc.TypeStatus, nil, nil); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("sem serviço: %v", err)
	}

	srv2 := startServer(t, newBackend())
	a.set(srv2.addr)
	waitConn(t, c.Events(), Connected)
	if ev := next(t, c.Events()); ev.Kind != EvSnapshot {
		t.Fatalf("reconexão traz snapshot novo: %+v", ev)
	}
	if err := c.Call(context.Background(), ipc.TypeCheckNow, ipc.VPNRef{VPN: "Matriz"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestClientConnectionLostWithoutNotice(t *testing.T) {
	srv := startServer(t, newBackend())
	c := runClient(t, fastOptions(dialTCP(srv.addr)))
	waitConn(t, c.Events(), Connected)
	if ev := next(t, c.Events()); ev.Kind != EvSnapshot { // inscrição concluída
		t.Fatalf("snapshot: %+v", ev)
	}
	srv.stop()
	if cn := waitConn(t, c.Events(), Unavailable); !strings.Contains(cn.Message, "perdida") {
		t.Fatalf("queda sem aviso: %+v", cn)
	}
}

func TestClientNotService(t *testing.T) {
	var dials atomic.Int32
	dial := func(context.Context) (net.Conn, error) {
		dials.Add(1)
		return nil, fmt.Errorf("%w: servido pelo PID 4242", ipc.ErrNotService)
	}
	c := runClient(t, fastOptions(dial))
	if cn := waitConn(t, c.Events(), NotService); !strings.Contains(cn.Message, "4242") {
		t.Fatalf("PID divergente: %+v", cn)
	}
	waitConn(t, c.Events(), NotService) // continua tentando
	if dials.Load() < 2 {
		t.Fatalf("tentativas: %d", dials.Load())
	}
}

func TestClientIncompatibleKeepsTrying(t *testing.T) {
	var a addr
	a.set(fakeHelloServer(t, func(id string) ipc.Message {
		return ipc.ErrorMessage(id, &ipc.Error{Code: ipc.CodeIncompatible, Message: "atualize o VPN Monitor"})
	}))
	c := runClient(t, fastOptions(a.dial))
	if cn := waitConn(t, c.Events(), Incompatible); !strings.Contains(cn.Message, "atualize") {
		t.Fatalf("incompatível: %+v", cn)
	}
	// Serviço atualizado: a bandeja volta sozinha.
	srv := startServer(t, newBackend())
	a.set(srv.addr)
	waitConn(t, c.Events(), Connected)
}

func TestClientServiceAbsent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().String()
	ln.Close()
	c := runClient(t, fastOptions(dialTCP(dead)))
	// O motivo é o erro do Dial como veio (o ipc.Dial já diz "serviço VPN
	// Monitor inacessível: …"); a bandeja não repete o prefixo.
	if cn := waitConn(t, c.Events(), Unavailable); !strings.Contains(cn.Message, dead) ||
		strings.Contains(cn.Message, "inacessível") {
		t.Fatalf("motivo: %+v", cn)
	}
}

func TestClientBackoffUpTo10s(t *testing.T) {
	var mu sync.Mutex
	var delays []time.Duration
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fired := make(chan time.Time)
	close(fired)
	o := Options{
		Dial:       func(context.Context) (net.Conn, error) { return nil, errors.New("pipe inexistente") },
		AppVersion: "x",
		Rand:       func() float64 { return 0.5 }, // sem jitter
		After: func(d time.Duration) <-chan time.Time {
			mu.Lock()
			defer mu.Unlock()
			delays = append(delays, d)
			if len(delays) == 8 {
				cancel()
			}
			return fired
		},
	}
	c := New(o)
	go func() {
		for range c.Events() {
		}
	}()
	c.Run(ctx)
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second,
		8 * time.Second, 10 * time.Second, 10 * time.Second, 10 * time.Second}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(delays) != fmt.Sprint(want) {
		t.Fatalf("esperas %v, esperava %v", delays, want)
	}
}

func TestClientRunClosesEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := New(fastOptions(func(context.Context) (net.Conn, error) { return nil, errors.New("x") }))
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	cancel()
	for range c.Events() {
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run não voltou após o cancelamento")
	}
}

// holdServer atende hello e inscrição, depois lê pedidos sem responder.
// drop() derruba a conexão do lado do serviço.
func holdServer(t *testing.T) (addr string, drop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	conns := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		codec := ipc.NewCodec(c)
		if m, err := codec.Read(); err == nil {
			_ = codec.Write(ipc.MustMessage(m.ID, ipc.TypeHello, ipc.Hello{Protocol: ipc.ProtocolVersion, AppVersion: "2.1.0-svc"}))
		}
		if m, err := codec.Read(); err == nil {
			_ = codec.Write(ipc.MustMessage(m.ID, ipc.TypeOK, struct{}{}))
		}
		conns <- c
		for {
			if _, err := codec.Read(); err != nil {
				return
			}
		}
	}()
	return ln.Addr().String(), func() {
		select {
		case c := <-conns:
			c.Close()
		case <-time.After(3 * time.Second):
			t.Error("holdServer: nenhuma conexão para derrubar em 3 s")
		}
	}
}

// Pedido em voo quando a conexão cai sai com ErrNotConnected.
func TestClientInFlightCallEndsWhenConnectionDrops(t *testing.T) {
	addr, drop := holdServer(t)
	c := runClient(t, fastOptions(dialTCP(addr)))
	waitConn(t, c.Events(), Connected)
	cur := waitSession(t, c)
	res := make(chan error, 1)
	go func() { res <- c.Call(context.Background(), ipc.TypeStatus, nil, nil) }()
	deadline := time.Now().Add(3 * time.Second)
	for { // espera o pedido ficar pendente
		cur.mu.Lock()
		n := len(cur.pending)
		cur.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("o pedido não ficou pendente em 3 s")
		}
		time.Sleep(time.Millisecond)
	}
	drop()
	select {
	case err := <-res:
		if !errors.Is(err, ErrNotConnected) {
			t.Fatalf("pedido em voo: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pedido em voo não terminou com a queda")
	}
}

// waitSession espera a sessão atual ficar publicada (com prazo).
func waitSession(t *testing.T, c *Client) *session {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		s := c.cur
		c.mu.Unlock()
		if s != nil {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("nenhuma sessão em 3 s")
	return nil
}

// Leitor preso em emit (buffer cheio, ctx vivo) é solto quando a sessão
// fecha, e Run segue: emite Unavailable assim que o consumidor drena.
func TestClientReaderNotStuckOnFullEvents(t *testing.T) {
	srv := startServer(t, newBackend())
	o := fastOptions(dialTCP(srv.addr))
	o.EventBuffer = 1
	c := New(o)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	defer func() {
		cancel()
		for range c.Events() {
		}
		<-done
	}()
	// Ninguém consome: Connected enche o buffer e o snapshot prende o leitor.
	s := waitSession(t, c)
	s.close()
	select {
	case <-s.readerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("o leitor continuou preso em emit depois do close")
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.Events():
			if ev.Kind == EvConn && ev.Conn.State == Unavailable {
				return
			}
		case <-deadline:
			t.Fatal("Unavailable não chegou depois do close")
		}
	}
}

// Cancelar Run não espera o prazo do hello com serviço travado.
func TestClientCancelDuringHello(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		close(accepted)
		buf := make([]byte, 1024)
		for {
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	}()
	o := fastOptions(dialTCP(ln.Addr().String()))
	o.Timeout = 10 * time.Second
	c := New(o)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	go func() {
		for range c.Events() {
		}
	}()
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("servidor não recebeu a conexão")
	}
	start := time.Now()
	cancel()
	select {
	case <-done:
		if d := time.Since(start); d > 200*time.Millisecond {
			t.Fatalf("Run levou %v para voltar", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run não voltou durante o hello")
	}
}
