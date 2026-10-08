package client

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// Options configura o cliente. Só Dial é obrigatório.
type Options struct {
	// Dial abre a conexão; no Windows, ipc.Dial (confere o PID do servidor e
	// devolve ipc.ErrNotService se divergir).
	Dial       func(context.Context) (net.Conn, error)
	AppVersion string
	// Timeout vale para o dial, o hello, a inscrição e cada escrita. Padrão 5 s.
	Timeout time.Duration
	// Backoff entre tentativas; padrão 500 ms dobrando até 10 s, ±20 % (§7).
	Backoff shared.Backoff
	// After e Rand são injetáveis para os testes; padrão time.After e rand.Float64.
	After func(time.Duration) <-chan time.Time
	Rand  func() float64
	// EventBuffer é o tamanho do canal de eventos; padrão 64.
	EventBuffer int
}

// Client mantém a conexão com o serviço e reconecta sozinho.
type Client struct {
	o      Options
	events chan Event
	cnt    counters
	mu     sync.Mutex
	cur    *session
}

// New cria o cliente; a conexão só começa em Run.
func New(o Options) *Client {
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Backoff == (shared.Backoff{}) {
		o.Backoff = shared.Backoff{Base: 500 * time.Millisecond, Max: 10 * time.Second, Jitter: 0.2}
	}
	if o.After == nil {
		o.After = time.After
	}
	if o.Rand == nil {
		o.Rand = rand.Float64
	}
	if o.EventBuffer == 0 {
		o.EventBuffer = 64
	}
	return &Client{o: o, events: make(chan Event, o.EventBuffer)}
}

// Events entrega mudanças de conexão e eventos do serviço, em ordem. Depois
// de Conn{Connected} vem sempre um snapshot completo. O canal fecha quando
// Run volta. Quem consome precisa acompanhar: o leitor espera por ele, e um
// serviço que não consegue entregar desconecta a bandeja (que reconecta).
func (c *Client) Events() <-chan Event { return c.events }

// Run conecta, se inscreve e reconecta com backoff até ctx terminar.
func (c *Client) Run(ctx context.Context) {
	defer close(c.events)
	emit := func(ev Event) bool { return c.send(ctx, ev, nil) }
	attempt := 0
	for ctx.Err() == nil {
		cn, lasted := c.connectOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if lasted >= c.o.Backoff.Max {
			attempt = 0 // sessão longa: a queda não é um laço de falhas
		}
		if !emit(Event{Kind: EvConn, Conn: cn}) {
			return
		}
		attempt++
		select {
		case <-ctx.Done():
			return
		case <-c.o.After(c.o.Backoff.Delay(attempt, c.o.Rand())):
		}
	}
}

// connectOnce faz uma tentativa e, se conectar, fica até a sessão cair.
// Devolve o estado a informar e quanto tempo a sessão durou.
func (c *Client) connectOnce(ctx context.Context) (Conn, time.Duration) {
	dctx, cancel := context.WithTimeout(ctx, c.o.Timeout)
	conn, err := c.o.Dial(dctx)
	cancel()
	if err != nil {
		if errors.Is(err, ipc.ErrNotService) {
			return Conn{State: NotService, Message: err.Error()}, 0
		}
		// ipc.Dial já explica ("serviço VPN Monitor inacessível: …").
		return Conn{State: Unavailable, Message: err.Error()}, 0
	}
	// Cancelar Run destrava hello e inscrição (que só têm prazo próprio).
	defer context.AfterFunc(ctx, func() { conn.Close() })()
	// O emit da sessão desiste com ctx (que já destrava sozinho) e também
	// quando a sessão fecha por outro motivo, como um close vindo de call
	// com falha de escrita: sem o done o leitor ficaria preso atrás de um
	// evento e esse close não o destravaria.
	var sess atomic.Pointer[session]
	ready := func(s *session) { sess.Store(s); c.setCurrent(s) }
	emit := func(ev Event) bool {
		var done <-chan struct{}
		if s := sess.Load(); s != nil {
			done = s.done()
		}
		return c.send(ctx, ev, done)
	}
	s, err := openSession(conn, c.o.AppVersion, c.o.Timeout, emit, ready, &c.cnt)
	if err != nil {
		c.setCurrent(nil)
		var e *ipc.Error
		if errors.As(err, &e) && e.Code == ipc.CodeIncompatible {
			return Conn{State: Incompatible, Message: e.Message}, 0
		}
		return Conn{State: Unavailable, Message: "serviço VPN Monitor não respondeu: " + err.Error()}, 0
	}
	start := time.Now()
	select {
	case <-s.done():
	case <-ctx.Done():
		s.close()
	}
	c.setCurrent(nil)
	s.wait() // o leitor não pode emitir depois de Run fechar Events
	if s.stopping() {
		return Conn{State: Unavailable, Message: "o serviço VPN Monitor parou"}, time.Since(start)
	}
	return Conn{State: Unavailable, Message: "conexão com o serviço VPN Monitor perdida"}, time.Since(start)
}

// send entrega ev sem bloquear para sempre: desiste (false) se ctx terminar
// ou se done (opcional, o fim da sessão) fechar.
func (c *Client) send(ctx context.Context, ev Event, done <-chan struct{}) bool {
	select {
	case c.events <- ev:
		return true
	case <-ctx.Done():
		return false
	case <-done:
		return false
	}
}

// setCurrent troca a sessão que atende Call (nil = sem conexão).
func (c *Client) setCurrent(s *session) {
	c.mu.Lock()
	c.cur = s
	c.mu.Unlock()
}

// Call envia um pedido pela conexão atual. Sem conexão: ErrNotConnected.
// Erros do serviço chegam como *ipc.Error.
func (c *Client) Call(ctx context.Context, typ string, payload any, out any) error {
	c.mu.Lock()
	s := c.cur
	c.mu.Unlock()
	if s == nil {
		return ErrNotConnected
	}
	return s.call(ctx, typ, payload, out)
}

// Stats devolve as contagens da decodificação tolerante desde o início.
func (c *Client) Stats() Stats { return c.cnt.stats() }
