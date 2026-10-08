package client

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

// fakeBackend é o serviço visto pelo servidor ipc real dos testes.
type fakeBackend struct {
	mu     sync.Mutex
	events chan ipc.Message
	calls  []string
}

func newBackend() *fakeBackend {
	b := &fakeBackend{events: make(chan ipc.Message, 16)}
	b.events <- ipc.MustMessage("", ipc.TypeSnapshot, ipc.Snapshot{
		VPNs: []ipc.VPNView{{Name: "Matriz", State: "Conectada"}}, Notifications: true})
	return b
}

func (b *fakeBackend) rec(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, s)
}

func (b *fakeBackend) Status() ipc.Snapshot {
	return ipc.Snapshot{VPNs: []ipc.VPNView{{Name: "Matriz", State: "Conectada"}}}
}
func (b *fakeBackend) CheckNow(v string) error { b.rec("checkNow " + v); return nil }
func (b *fakeBackend) Reconnect(string) error {
	return &ipc.Error{Code: ipc.CodePaused, Message: "VPN pausada"}
}
func (b *fakeBackend) Pause(string, *time.Time) error          { return nil }
func (b *fakeBackend) Resume(string) error                     { return nil }
func (b *fakeBackend) SetEnabled(string, bool) error           { return nil }
func (b *fakeBackend) AddVPN(config.RawVPN) error              { return nil }
func (b *fakeBackend) UpdateVPN(string, config.RawVPN) error   { return nil }
func (b *fakeBackend) RemoveVPN(string) error                  { return nil }
func (b *fakeBackend) ListRasEntries() ([]ipc.RasEntry, error) { return nil, nil }
func (b *fakeBackend) GetConfig() config.Config                { return config.Empty() }
func (b *fakeBackend) SetGlobal(ipc.SetGlobalRequest) error    { return nil }
func (b *fakeBackend) LogTail(int) (string, error)             { return "fim do log\n", nil }
func (b *fakeBackend) Subscribe() (<-chan ipc.Message, func()) { return b.events, func() {} }

// server é um ipc.Server real em TCP local.
type server struct {
	addr   string
	cancel context.CancelFunc
	done   chan error
}

func startServer(t *testing.T, b ipc.Backend) *server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &ipc.Server{Backend: b, AppVersion: "2.1.0-svc", HandshakeTimeout: time.Second, WriteTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	srv := &server{addr: ln.Addr().String(), cancel: cancel, done: make(chan error, 1)}
	go func() { srv.done <- s.Serve(ctx, ln) }()
	t.Cleanup(srv.stop)
	return srv
}

func (s *server) stop() {
	s.cancel()
	<-s.done
	s.done <- nil // stop idempotente
}

func dialTCP(addr string) func(context.Context) (net.Conn, error) {
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
}

// next lê o próximo evento ou falha após 2 s.
func next(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("canal de eventos fechado")
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("nenhum evento em 2 s")
	}
	return Event{}
}
