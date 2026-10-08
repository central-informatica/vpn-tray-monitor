package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

// session é uma conexão aberta: hello trocado, inscrita em eventos, com um
// leitor que entrega respostas aos pedidos pendentes e eventos ao emit.
type session struct {
	conn  net.Conn
	codec *ipc.Codec
	emit  func(Event) bool
	cnt   *counters
	// writeTimeout limita cada escrita (servidor travado não prende o menu).
	writeTimeout time.Duration

	mu       sync.Mutex
	writeMu  sync.Mutex
	next     int
	pending  map[string]chan ipc.Message
	finished chan struct{}
	// readerDone fecha quando o leitor termina (nada mais é emitido).
	readerDone chan struct{}
	isStop     bool
	once       sync.Once
}

// openSession troca o hello (prazo timeout), chama ready (quem chama passa
// a aceitar pedidos por esta sessão), emite Conn{Connected}, liga o leitor e
// se inscreve nos eventos. cnt recebe as contagens da decodificação tolerante. Erros do serviço chegam como *ipc.Error
// (incompatible, busy…); a conexão é fechada em qualquer erro.
func openSession(conn net.Conn, appVersion string, timeout time.Duration, emit func(Event) bool, ready func(*session), cnt *counters) (*session, error) {
	s := &session{conn: conn, codec: ipc.NewCodec(conn), emit: emit, cnt: cnt, writeTimeout: timeout,
		pending: map[string]chan ipc.Message{}, finished: make(chan struct{}), readerDone: make(chan struct{})}
	serverApp, err := s.hello(appVersion, timeout)
	if err != nil {
		conn.Close()
		return nil, err
	}
	ready(s)
	if !emit(Event{Kind: EvConn, Conn: Conn{State: Connected, ServerVersion: serverApp}}) {
		s.close() // a sessão já foi publicada por ready: done() precisa fechar
		return nil, context.Canceled
	}
	go func() { defer close(s.readerDone); s.reader() }()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := s.call(ctx, ipc.TypeSubscribe, nil, nil); err != nil {
		s.close()
		s.wait() // o leitor não emite depois que openSession devolve erro
		return nil, fmt.Errorf("inscrição nos eventos: %w", err)
	}
	return s, nil
}

// hello é síncrono (antes do leitor). A resposta é lida de forma tolerante:
// um serviço mais novo pode mandar campos que este cliente não conhece, e o
// que importa é o protocolo.
func (s *session) hello(appVersion string, timeout time.Duration) (string, error) {
	_ = s.conn.SetDeadline(time.Now().Add(timeout))
	defer s.conn.SetDeadline(time.Time{})
	if err := s.codec.Write(ipc.MustMessage("0", ipc.TypeHello, ipc.Hello{Protocol: ipc.ProtocolVersion, AppVersion: appVersion})); err != nil {
		return "", err
	}
	m, err := s.codec.Read()
	if err != nil {
		return "", err
	}
	switch m.Type {
	case ipc.TypeError:
		return "", decodeError(m)
	case ipc.TypeHello:
	default:
		return "", fmt.Errorf("resposta inesperada ao hello: %q", m.Type)
	}
	var h ipc.Hello
	if err := s.decode(m.Payload, &h); err != nil {
		return "", fmt.Errorf("hello ilegível: %w", err)
	}
	if h.Protocol != ipc.ProtocolVersion {
		return "", &ipc.Error{Code: ipc.CodeIncompatible, Message: fmt.Sprintf(
			"o serviço (versão %s) fala o protocolo %d e esta bandeja o %d; atualize o VPN Monitor",
			h.AppVersion, h.Protocol, ipc.ProtocolVersion)}
	}
	return h.AppVersion, nil
}

func decodeError(m ipc.Message) error {
	var e ipc.Error
	if err := json.Unmarshal(m.Payload, &e); err != nil {
		return fmt.Errorf("resposta de erro ilegível: %w", err)
	}
	return &e
}

// reader entrega respostas e eventos até a conexão cair.
func (s *session) reader() {
	defer s.close()
	for {
		m, err := s.codec.Read()
		if err != nil {
			return
		}
		if m.ID != "" {
			s.mu.Lock()
			ch := s.pending[m.ID]
			delete(s.pending, m.ID)
			s.mu.Unlock()
			if ch != nil {
				ch <- m // buffer 1
			}
			continue
		}
		ev, ok := s.event(m)
		if !ok {
			continue // tipo desconhecido (serviço mais novo): ignora
		}
		if !s.emit(ev) {
			return
		}
	}
}

func (s *session) event(m ipc.Message) (Event, bool) {
	var ev Event
	var target any
	switch m.Type {
	case ipc.TypeSnapshot:
		ev.Kind, target = EvSnapshot, &ev.Snapshot
	case ipc.TypeVPNState:
		ev.Kind, target = EvVPNState, &ev.VPN
	case ipc.TypeNotice:
		ev.Kind, target = EvNotice, &ev.Notice
	case ipc.TypeConfigStatus:
		ev.Kind, target = EvConfigStatus, &ev.ConfigStatus
	case ipc.TypeServiceStopping:
		s.mu.Lock()
		s.isStop = true
		s.mu.Unlock()
		return Event{Kind: EvConn, Conn: Conn{State: Stopping, Message: "o serviço VPN Monitor está parando"}}, true
	default:
		s.cnt.events.Add(1)
		return Event{}, false
	}
	if s.decode(m.Payload, target) != nil {
		s.cnt.events.Add(1)
		return Event{}, false
	}
	return ev, true
}

// decode é tolerante só quanto ao PAYLOAD: o envelope (ipc.Decode, no codec)
// é estrito, e uma linha inválida derruba a sessão (a bandeja reconecta).
// Tenta o estrito do protocolo e, se só sobrarem campos
// desconhecidos (serviço mais novo), aproveita a mensagem sem eles e conta.
// Erro só se nem a leitura tolerante der certo.
func (s *session) decode(raw json.RawMessage, out any) error {
	if len(raw) == 0 || ipc.DecodePayload(raw, out) == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	s.cnt.fields.Add(1)
	return nil
}

// call envia um pedido e espera a resposta com o mesmo id.
func (s *session) call(ctx context.Context, typ string, payload any, out any) error {
	s.mu.Lock()
	select {
	case <-s.finished:
		s.mu.Unlock()
		return ErrNotConnected
	default:
	}
	s.next++
	id := strconv.Itoa(s.next)
	ch := make(chan ipc.Message, 1)
	s.pending[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()
	m, err := ipc.NewMessage(id, typ, payload)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
	err = s.codec.Write(m)
	s.writeMu.Unlock()
	if err != nil {
		s.close()
		return ErrNotConnected
	}
	var r ipc.Message
	select {
	case r = <-ch:
	case <-s.finished:
		// A resposta pode ter chegado junto com a queda.
		select {
		case r = <-ch:
		default:
			return ErrNotConnected
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	switch r.Type {
	case ipc.TypeError:
		return decodeError(r)
	case ipc.TypeOK:
		if out != nil && len(r.Payload) > 0 {
			return json.Unmarshal(r.Payload, out)
		}
		return nil
	}
	return fmt.Errorf("resposta inesperada %q", r.Type)
}

func (s *session) close() {
	s.once.Do(func() {
		s.mu.Lock()
		close(s.finished)
		s.mu.Unlock()
		s.conn.Close()
	})
}

func (s *session) done() <-chan struct{} { return s.finished }

// stopping diz se o serviço avisou a parada antes de a conexão cair.
func (s *session) stopping() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isStop
}

// wait espera o leitor terminar; depois disso a sessão não emite mais.
func (s *session) wait() { <-s.readerDone }
