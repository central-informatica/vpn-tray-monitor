package ipc

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
)

type fakeBackend struct {
	mu      sync.Mutex
	calls   []string
	events  chan Message
	cancels int
}

func (f *fakeBackend) rec(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeBackend) Status() Snapshot {
	return Snapshot{VPNs: []VPNView{{Name: "Matriz", State: "Conectada"}}, Notifications: true}
}
func (f *fakeBackend) CheckNow(v string) error { f.rec("checkNow " + v); return nil }
func (f *fakeBackend) Reconnect(v string) error {
	return &Error{Code: CodePaused, Message: "VPN pausada"}
}
func (f *fakeBackend) Pause(v string, until *time.Time) error {
	if until == nil {
		f.rec("pause " + v + " indefinida")
	} else {
		f.rec("pause " + v + " " + until.UTC().Format(time.RFC3339))
	}
	return nil
}
func (f *fakeBackend) Resume(string) error                   { return errors.New("disco cheio") }
func (f *fakeBackend) SetEnabled(string, bool) error         { panic("bug") }
func (f *fakeBackend) AddVPN(config.RawVPN) error            { return nil }
func (f *fakeBackend) UpdateVPN(string, config.RawVPN) error { return nil }
func (f *fakeBackend) RemoveVPN(string) error                { return nil }
func (f *fakeBackend) ListRasEntries() ([]RasEntry, error) {
	return []RasEntry{{Name: "VPN Matriz", Monitored: true}}, nil
}
func (f *fakeBackend) GetConfig() config.Config         { return config.Empty() }
func (f *fakeBackend) SetGlobal(SetGlobalRequest) error { return nil }
func (f *fakeBackend) LogTail(n int) (string, error)    { return "linha\n", nil }
func (f *fakeBackend) Subscribe() (<-chan Message, func()) {
	return f.events, func() { f.mu.Lock(); f.cancels++; f.mu.Unlock() }
}

func startServer(t *testing.T, b Backend, tweak func(*Server)) (string, context.CancelFunc, chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Backend: b, AppVersion: "2.0.0-teste", HandshakeTimeout: time.Second, WriteTimeout: time.Second}
	if tweak != nil {
		tweak(s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	t.Cleanup(func() { cancel(); <-done })
	return ln.Addr().String(), cancel, done
}

func dial(t *testing.T, addr string) *Client {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	cl, err := Handshake(c, "teste")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	return cl
}

func TestServerRequests(t *testing.T) {
	b := &fakeBackend{}
	addr, _, _ := startServer(t, b, nil)
	cl := dial(t, addr)
	if cl.ServerApp != "2.0.0-teste" {
		t.Fatalf("hello: %q", cl.ServerApp)
	}
	var snap Snapshot
	if err := cl.Call(TypeStatus, nil, &snap); err != nil || snap.VPNs[0].State != "Conectada" {
		t.Fatalf("status: %+v %v", snap, err)
	}
	if err := cl.Call(TypeCheckNow, VPNRef{VPN: "Matriz"}, nil); err != nil {
		t.Fatal(err)
	}
	until := time.Date(2026, 10, 7, 15, 30, 0, 0, time.UTC).Unix()
	_ = cl.Call(TypePause, PauseRequest{VPN: "Matriz", UntilUnix: &until}, nil)
	_ = cl.Call(TypePause, PauseRequest{VPN: "Filial"}, nil)

	var e *Error
	if err := cl.Call(TypeReconnect, VPNRef{VPN: "Matriz"}, nil); !errors.As(err, &e) || e.Code != CodePaused {
		t.Fatalf("erro do backend passa como está: %v", err)
	}
	if err := cl.Call(TypeResume, VPNRef{VPN: "Matriz"}, nil); !errors.As(err, &e) || e.Code != CodeInternal {
		t.Fatalf("erro comum vira internal: %v", err)
	}
	if err := cl.Call(TypeSetEnabled, SetEnabledRequest{VPN: "x"}, nil); !errors.As(err, &e) || e.Code != CodeInternal {
		t.Fatalf("panic vira internal: %v", err)
	}
	if err := cl.Call("formatarDisco", nil, nil); !errors.As(err, &e) || e.Code != CodeUnknownType {
		t.Fatalf("tipo desconhecido: %v", err)
	}
	if err := cl.Call(TypeCheckNow, map[string]any{"vpn": "x", "extra": 1}, nil); !errors.As(err, &e) || e.Code != CodeBadRequest {
		t.Fatalf("payload estrito: %v", err)
	}
	var entries RasEntries
	if err := cl.Call(TypeListRasEntries, nil, &entries); err != nil || len(entries.Entries) != 1 {
		t.Fatal(entries, err)
	}
	// A conexão sobreviveu ao panic e aos erros.
	if err := cl.Call(TypeStatus, nil, nil); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	want := []string{"checkNow Matriz", "pause Matriz 2026-10-07T15:30:00Z", "pause Filial indefinida"}
	if strings.Join(b.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("chamadas %v", b.calls)
	}
}

func rawConn(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	return c, bufio.NewReader(c)
}

func expectErrorAndClose(t *testing.T, r *bufio.Reader, code string) {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil || !strings.Contains(line, `"code":"`+code+`"`) {
		t.Fatalf("esperava erro %s, veio %q %v", code, line, err)
	}
	if _, err := r.ReadString('\n'); err == nil {
		t.Fatal("conexão deveria ter sido fechada")
	}
}

func TestServerHandshakeRules(t *testing.T) {
	addr, _, _ := startServer(t, &fakeBackend{}, nil)

	c, r := rawConn(t, addr)
	c.Write([]byte(`{"v":1,"id":"h7","type":"hello","payload":{"protocol":99,"appVersion":"x"}}` + "\n"))
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	m, err := Decode([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	var e Error
	if m.Type != TypeError || m.ID != "h7" || DecodePayload(m.Payload, &e) != nil || e.Code != CodeIncompatible {
		t.Fatalf("hello incompatível deve ter resposta error{incompatible} com o id do hello: %q", line)
	}
	if _, err := r.ReadString('\n'); err == nil {
		t.Fatal("conexão deveria ter sido fechada após incompatible")
	}

	c, r = rawConn(t, addr)
	c.Write([]byte(`{"v":1,"id":"1","type":"status"}` + "\n"))
	expectErrorAndClose(t, r, CodeBadRequest)

	c, r = rawConn(t, addr) // não manda nada: prazo do handshake
	if _, err := r.ReadString('\n'); err == nil {
		t.Fatal("sem hello a conexão deve cair")
	}
}

func TestServerMalformedAndOversized(t *testing.T) {
	addr, _, _ := startServer(t, &fakeBackend{}, nil)
	hello := `{"v":1,"id":"1","type":"hello","payload":{"protocol":1,"appVersion":"x"}}` + "\n"

	c, r := rawConn(t, addr)
	c.Write([]byte(hello + "{isso não é json\n"))
	r.ReadString('\n') // hello
	expectErrorAndClose(t, r, CodeBadRequest)

	c, r = rawConn(t, addr)
	c.Write([]byte(hello))
	r.ReadString('\n')
	go c.Write([]byte(strings.Repeat("a", MaxMessage+10) + "\n"))
	expectErrorAndClose(t, r, CodeBadRequest)
}

func TestServerMaxConnections(t *testing.T) {
	addr, _, _ := startServer(t, &fakeBackend{}, func(s *Server) { s.MaxConns = 2 })
	dial(t, addr)
	dial(t, addr)
	_, r := rawConn(t, addr)
	expectErrorAndClose(t, r, CodeBusy)
}

func TestServerSlowSubscriberIsDisconnected(t *testing.T) {
	b := &fakeBackend{events: make(chan Message, 1000)}
	addr, _, _ := startServer(t, b, func(s *Server) { s.OutQueue = 4 })
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cl, err := Handshake(c, "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.Call(TypeSubscribe, nil, nil); err != nil {
		t.Fatal(err)
	}
	big := MustMessage("", TypeLogTail, LogTail{Text: strings.Repeat("x", 60000)})
	for i := 0; i < 1000; i++ { // cliente não lê: a fila de saída enche
		b.events <- big
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		n := b.cancels
		b.mu.Unlock()
		if n == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("assinante lento deveria ter sido desconectado (e a assinatura cancelada)")
}

func TestServerStopsOnContextCancel(t *testing.T) {
	addr, cancel, done := startServer(t, &fakeBackend{}, nil)
	cl := dial(t, addr)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		done <- nil
	case <-time.After(2 * time.Second):
		t.Fatal("Serve não voltou")
	}
	if err := cl.Call(TypeStatus, nil, nil); err == nil {
		t.Fatal("conexões devem ser fechadas na parada")
	}
}

func TestServerIdleTimeoutSparesSubscribers(t *testing.T) {
	b := &fakeBackend{events: make(chan Message, 10)}
	addr, _, _ := startServer(t, b, func(s *Server) { s.IdleTimeout = 100 * time.Millisecond })
	idle := dial(t, addr)
	sub := dial(t, addr)
	if err := sub.Call(TypeSubscribe, nil, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := idle.Call(TypeStatus, nil, nil); err == nil {
		t.Fatal("conexão ociosa sem inscrição deveria ter sido fechada")
	}
	if err := sub.Call(TypeStatus, nil, nil); err != nil {
		t.Fatalf("conexão inscrita deve sobreviver à ociosidade: %v", err)
	}
}

type bigLogBackend struct {
	fakeBackend
	asked chan int
}

// Texto que o JSON expande: '<' vira < (6 bytes) e controles também.
func (b *bigLogBackend) LogTail(n int) (string, error) {
	b.asked <- n
	return strings.Repeat("<\x01", n/2), nil
}

func TestServerLogTailFitsMaxMessage(t *testing.T) {
	b := &bigLogBackend{asked: make(chan int, 4)}
	addr, _, _ := startServer(t, b, nil)
	cl := dial(t, addr)
	for _, req := range []int{0, -5, 10 * MaxMessage} {
		var lt LogTail
		if err := cl.Call(TypeLogTail, LogTailRequest{MaxBytes: req}, &lt); err != nil {
			t.Fatalf("maxBytes %d: %v", req, err)
		}
		if n := <-b.asked; n <= 0 || n > MaxMessage {
			t.Fatalf("maxBytes %d repassado como %d", req, n)
		}
		if lt.Text == "" || !strings.HasSuffix(lt.Text, "<\x01") {
			t.Fatalf("deve vir o final do log, cortado pelo começo: %d bytes", len(lt.Text))
		}
	}
	var lt LogTail
	if err := cl.Call(TypeLogTail, LogTailRequest{MaxBytes: 100}, &lt); err != nil || len(lt.Text) != 100 {
		t.Fatalf("pedido pequeno passa inteiro: %d %v", len(lt.Text), err)
	}
	<-b.asked
}

func TestClientSeesBusy(t *testing.T) {
	addr, _, _ := startServer(t, &fakeBackend{}, func(s *Server) { s.MaxConns = 1 })
	dial(t, addr)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	var e *Error
	if _, err := Handshake(c, "x"); !errors.As(err, &e) || e.Code != CodeBusy {
		t.Fatalf("cliente deve receber busy: %v", err)
	}
}

func TestFitLogTail(t *testing.T) {
	pieces := []string{"a", "ç", " ", "<", "\"", "\\", "\n", "\x01", "\xff", "😀"}
	var sb strings.Builder
	for i := 0; sb.Len() < 3*MaxMessage; i++ {
		sb.WriteString(pieces[i%len(pieces)])
	}
	text := sb.String()
	got := fitLogTail("123", text)
	if !strings.HasSuffix(text, got) || len(got) == 0 {
		t.Fatal("deve ser um sufixo não vazio")
	}
	n := encodedLen(MustMessage("123", TypeOK, LogTail{Text: got}))
	if n > MaxMessage || n < MaxMessage-64 {
		t.Fatalf("codificado com %d bytes; esperado logo abaixo de %d", n, MaxMessage)
	}
	if small := "linha\n"; fitLogTail("1", small) != small {
		t.Fatal("texto pequeno passa inteiro")
	}
}

// glitchListener devolve erros transitórios no Accept quando armado, sem
// fechar o listener de verdade (no go-winio o firstHandle segue vivo).
type glitchListener struct {
	net.Listener
	mu      sync.Mutex
	glitch  int
	glitchN int
}

func (g *glitchListener) arm(n int) { g.mu.Lock(); g.glitch = n; g.mu.Unlock() }

func (g *glitchListener) Accept() (net.Conn, error) {
	g.mu.Lock()
	if g.glitch > 0 {
		g.glitch--
		g.glitchN++
		g.mu.Unlock()
		return nil, errors.New("falha transitória do Accept")
	}
	g.mu.Unlock()
	return g.Listener.Accept()
}

// Erro de Accept que não é fechamento nem parada: Serve espera e repete,
// sem fechar o listener (o nome do pipe ficaria livre para outro processo)
// e sem derrubar as conexões existentes.
func TestServerRetriesTransientAcceptErrors(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := &glitchListener{Listener: ln}
	s := &Server{Backend: &fakeBackend{}, AppVersion: "2.0.0-teste", HandshakeTimeout: time.Second, WriteTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, g) }()
	t.Cleanup(func() { cancel(); <-done })

	existing := dial(t, ln.Addr().String())
	if err := existing.Call(TypeStatus, nil, nil); err != nil {
		t.Fatal(err)
	}
	g.arm(5)
	// O Accept pendente já pegou o listener real; a próxima conexão o libera
	// e as seguintes passam pelas falhas.
	first := dial(t, ln.Addr().String())
	if err := first.Call(TypeStatus, nil, nil); err != nil {
		t.Fatal(err)
	}
	after := dial(t, ln.Addr().String())
	if err := after.Call(TypeStatus, nil, nil); err != nil {
		t.Fatalf("Serve deveria seguir atendendo após falhas transitórias: %v", err)
	}
	g.mu.Lock()
	n := g.glitchN
	g.mu.Unlock()
	if n != 5 {
		t.Fatalf("falhas consumidas: %d", n)
	}
	select {
	case err := <-done:
		t.Fatalf("Serve voltou com %v", err)
	default:
	}
	if err := existing.Call(TypeStatus, nil, nil); err != nil {
		t.Fatalf("conexão existente foi derrubada: %v", err)
	}
}

// Listener fechado de fato: Serve volta com erro (a montagem decide).
func TestServerReturnsWhenListenerCloses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Backend: &fakeBackend{}, AppVersion: "2.0.0-teste"}
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), ln) }()
	ln.Close()
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("erro: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve não voltou com o listener fechado")
	}
}

// O cliente confere a versão de protocolo devolvida no hello: um servidor
// que responda outra versão é recusado com incompatible.
func TestClientChecksServerProtocol(t *testing.T) {
	cli, srv := net.Pipe()
	go func() {
		codec := NewCodec(srv)
		m, err := codec.Read()
		if err != nil {
			return
		}
		_ = codec.Write(MustMessage(m.ID, TypeHello, Hello{Protocol: ProtocolVersion + 1, AppVersion: "9.9"}))
	}()
	var e *Error
	cl, err := Handshake(cli, "x")
	if !errors.As(err, &e) || e.Code != CodeIncompatible || !strings.Contains(e.Message, "protocolo") {
		t.Fatalf("protocolo divergente deve ser recusado: %v %v", cl, err)
	}
	// A conexão foi fechada pelo cliente.
	if _, err := cli.Write([]byte("x")); err == nil {
		t.Fatal("conexão deveria estar fechada")
	}
}

// expectErrorID lê a resposta de erro, confere código e id, e o fechamento.
func expectErrorID(t *testing.T, r *bufio.Reader, code, id string) {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("sem resposta: %v", err)
	}
	m, err := Decode([]byte(line))
	var e Error
	if err != nil || m.Type != TypeError || m.ID != id || DecodePayload(m.Payload, &e) != nil || e.Code != code {
		t.Fatalf("esperava error{%s} com id %q, veio %q", code, id, line)
	}
	if _, err := r.ReadString('\n'); err == nil {
		t.Fatal("conexão deveria ter sido fechada")
	}
}

// Um cliente de protocolo futuro pode mandar no hello campos que este
// servidor não conhece: a versão é lida de forma tolerante e a resposta é
// incompatible (não bad_request), para a bandeja dizer "atualize".
func TestServerHelloTolerantProtocolRead(t *testing.T) {
	addr, _, _ := startServer(t, &fakeBackend{}, nil)
	cases := []struct {
		name, line, code, id string
	}{
		{"campo novo e versão futura", `{"v":1,"id":"h1","type":"hello","payload":{"protocol":2,"appVersion":"3.0","features":["x"]}}`, CodeIncompatible, "h1"},
		{"campo novo com a versão atual", `{"v":1,"id":"h2","type":"hello","payload":{"protocol":1,"appVersion":"x","extra":true}}`, CodeBadRequest, "h2"},
		{"protocolo com tipo errado", `{"v":1,"id":"h3","type":"hello","payload":{"protocol":"dois"}}`, CodeBadRequest, "h3"},
		{"payload não é objeto", `{"v":1,"id":"h4","type":"hello","payload":[1]}`, CodeBadRequest, "h4"},
		{"primeira linha não é JSON", `isso não é json`, CodeBadRequest, ""},
		{"envelope com campo desconhecido", `{"v":1,"id":"h5","type":"hello","novo":1}`, CodeBadRequest, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, r := rawConn(t, addr)
			c.Write([]byte(tc.line + "\n"))
			expectErrorID(t, r, tc.code, tc.id)
		})
	}
	t.Run("primeira linha grande demais", func(t *testing.T) {
		c, r := rawConn(t, addr)
		go c.Write([]byte(strings.Repeat("a", MaxMessage+10) + "\n"))
		expectErrorID(t, r, CodeBadRequest, "")
	})
}

// pipeListener entrega conexões net.Pipe: síncronas, então o hello do
// cliente só "chega" se o servidor o ler. Reproduz de forma determinística o
// que no Windows acontece com TCP: fechar com dados do cliente não lidos (RST)
// faz o cliente perder a resposta já enviada.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *pipeListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *pipeListener) Addr() net.Addr { return &net.UnixAddr{Name: "pipe", Net: "pipe"} }

func (l *pipeListener) dial(t *testing.T) net.Conn {
	t.Helper()
	cli, srv := net.Pipe()
	select {
	case l.conns <- srv:
	case <-time.After(2 * time.Second):
		t.Fatal("servidor não aceitou")
	}
	t.Cleanup(func() { cli.Close() })
	return cli
}

func TestBusyReplyReadsClientHelloBeforeClosing(t *testing.T) {
	ln := newPipeListener()
	s := &Server{Backend: &fakeBackend{}, MaxConns: 1, HandshakeTimeout: time.Second, WriteTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	t.Cleanup(func() { cancel(); <-done })

	if _, err := Handshake(ln.dial(t), "x"); err != nil {
		t.Fatal(err)
	}
	var e *Error
	if _, err := Handshake(ln.dial(t), "x"); !errors.As(err, &e) || e.Code != CodeBusy {
		t.Fatalf("cliente deve receber busy mesmo mandando hello antes de ler: %v", err)
	}
}
