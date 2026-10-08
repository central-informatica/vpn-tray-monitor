package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
)

// Backend é o que o serviço oferece pelo pipe. Erros *Error vão como estão;
// outros viram "internal".
type Backend interface {
	Status() Snapshot
	CheckNow(vpn string) error
	Reconnect(vpn string) error
	Pause(vpn string, until *time.Time) error
	Resume(vpn string) error
	SetEnabled(vpn string, enabled bool) error
	AddVPN(raw config.RawVPN) error
	UpdateVPN(name string, raw config.RawVPN) error
	RemoveVPN(name string) error
	ListRasEntries() ([]RasEntry, error)
	GetConfig() config.Config
	SetGlobal(req SetGlobalRequest) error
	LogTail(maxBytes int) (string, error)
	// Subscribe devolve eventos já como mensagens (o primeiro é o snapshot).
	// O canal é fechado se o assinante ficar para trás.
	Subscribe() (<-chan Message, func())
}

// Server atende conexões do pipe.
type Server struct {
	Backend          Backend
	AppVersion       string
	Log              *slog.Logger
	MaxConns         int
	HandshakeTimeout time.Duration
	WriteTimeout     time.Duration
	// IdleTimeout fecha conexões sem pedido nesse prazo, exceto as inscritas
	// em eventos (a bandeja fica ociosa por horas). Padrão 2 min.
	IdleTimeout time.Duration
	OutQueue    int
}

func (s *Server) defaults() {
	if s.MaxConns == 0 {
		s.MaxConns = MaxConns
	}
	if s.HandshakeTimeout == 0 {
		s.HandshakeTimeout = 5 * time.Second
	}
	if s.WriteTimeout == 0 {
		s.WriteTimeout = 5 * time.Second
	}
	if s.OutQueue == 0 {
		s.OutQueue = 64
	}
	if s.IdleTimeout == 0 {
		s.IdleTimeout = 2 * time.Minute
	}
	if s.Log == nil {
		s.Log = slog.New(slog.DiscardHandler)
	}
}

// Serve aceita conexões até ctx terminar; então fecha o listener e todas
// as conexões e espera os atendimentos voltarem.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.defaults()
	var (
		mu    sync.Mutex
		conns = map[net.Conn]struct{}{}
		wg    sync.WaitGroup
	)
	closeAll := func() {
		ln.Close()
		mu.Lock()
		for c := range conns {
			c.Close()
		}
		mu.Unlock()
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			closeAll()
		case <-stop:
		}
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			closeAll() // vale também para Accept que falhou sem parada pedida
			wg.Wait()
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		mu.Lock()
		// Conferido sob o mutex: uma conexão aceita junto com a parada não
		// escapa do fechamento em massa.
		stopping := ctx.Err() != nil
		full := len(conns) >= s.MaxConns
		if !full && !stopping {
			conns[c] = struct{}{}
		}
		mu.Unlock()
		if stopping {
			c.Close()
			continue
		}
		if full {
			_ = c.SetWriteDeadline(time.Now().Add(time.Second))
			_ = NewCodec(c).Write(ErrorMessage("", &Error{Code: CodeBusy, Message: "conexões demais"}))
			c.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				mu.Lock()
				delete(conns, c)
				mu.Unlock()
				c.Close()
			}()
			s.handle(ctx, c)
		}()
	}
}

type conn struct {
	s      *Server
	c      net.Conn
	codec  *Codec
	out    chan Message
	closed chan struct{}
	once   sync.Once
	// pending conta mensagens enfileiradas ainda não escritas (para o flush).
	pending atomic.Int64
}

func (cn *conn) close() { cn.once.Do(func() { close(cn.closed); cn.c.Close() }) }

// send enfileira; fila cheia = cliente que não consome: desconecta.
func (cn *conn) send(m Message) {
	cn.pending.Add(1)
	select {
	case cn.out <- m:
		return
	default:
	}
	cn.pending.Add(-1)
	select {
	case <-cn.closed:
	default:
		cn.s.Log.Warn("cliente do pipe lento; desconectando")
		cn.close()
	}
}

func (cn *conn) writer() {
	for {
		select {
		case <-cn.closed:
			return
		case m := <-cn.out:
			_ = cn.c.SetWriteDeadline(time.Now().Add(cn.s.WriteTimeout))
			err := cn.codec.Write(m)
			cn.pending.Add(-1)
			if err != nil {
				cn.close()
				return
			}
		}
	}
}

func (s *Server) handle(ctx context.Context, c net.Conn) {
	cn := &conn{s: s, c: c, codec: NewCodec(c), out: make(chan Message, s.OutQueue), closed: make(chan struct{})}
	defer cn.close()
	defer func() {
		if r := recover(); r != nil {
			s.Log.Error("panic numa conexão do pipe", "panic", fmt.Sprint(r), "pilha", string(debug.Stack()))
		}
	}()
	go cn.writer()

	_ = c.SetReadDeadline(time.Now().Add(s.HandshakeTimeout))
	m, err := cn.codec.Read()
	if err != nil {
		return
	}
	var h Hello
	if m.Type != TypeHello || DecodePayload(m.Payload, &h) != nil {
		cn.send(ErrorMessage(m.ID, &Error{Code: CodeBadRequest, Message: "esperava hello"}))
		cn.flush()
		return
	}
	if h.Protocol != ProtocolVersion {
		cn.send(ErrorMessage(m.ID, &Error{Code: CodeIncompatible,
			Message: fmt.Sprintf("protocolo %d incompatível com o serviço (%d); atualize o VPN Monitor", h.Protocol, ProtocolVersion)}))
		cn.flush()
		return
	}
	cn.send(MustMessage(m.ID, TypeHello, Hello{Protocol: ProtocolVersion, AppVersion: s.AppVersion}))

	var unsubscribe func()
	defer func() {
		if unsubscribe != nil {
			unsubscribe()
		}
	}()
	for {
		if unsubscribe == nil {
			_ = c.SetReadDeadline(time.Now().Add(s.IdleTimeout))
		} else {
			_ = c.SetReadDeadline(time.Time{}) // inscrito: pode ficar ocioso
		}
		m, err := cn.codec.Read()
		if err != nil {
			// Linha grande demais ou inválida: responde e encerra, porque o
			// fluxo perdeu o sincronismo. Outros erros = cliente saiu.
			var de *DecodeError
			if errors.Is(err, ErrTooLarge) || errors.As(err, &de) {
				cn.send(ErrorMessage("", &Error{Code: CodeBadRequest, Message: err.Error()}))
				cn.flush()
			}
			return
		}
		if m.Type == TypeSubscribe {
			if unsubscribe != nil {
				cn.send(MustMessage(m.ID, TypeOK, nil))
				continue
			}
			events, cancel := s.Backend.Subscribe()
			unsubscribe = cancel
			cn.send(MustMessage(m.ID, TypeOK, nil))
			go func() {
				for {
					select {
					case ev, ok := <-events:
						if !ok {
							cn.close() // ficou para trás no barramento
							return
						}
						cn.send(ev)
					case <-cn.closed:
						return
					case <-ctx.Done():
						return
					}
				}
			}()
			continue
		}
		cn.send(s.dispatch(m))
	}
}

// flush espera o escritor gravar tudo o que foi enfileirado (até
// WriteTimeout) antes de a conexão ser fechada.
func (cn *conn) flush() {
	deadline := time.After(cn.s.WriteTimeout)
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for cn.pending.Load() > 0 {
		select {
		case <-deadline:
			return
		case <-cn.closed:
			return
		case <-tick.C:
		}
	}
}

func (s *Server) dispatch(m Message) (resp Message) {
	defer func() {
		if r := recover(); r != nil {
			s.Log.Error("panic atendendo pedido", "tipo", m.Type, "panic", fmt.Sprint(r), "pilha", string(debug.Stack()))
			resp = ErrorMessage(m.ID, &Error{Code: CodeInternal, Message: "erro interno"})
		}
	}()
	result, err := s.call(m)
	if err != nil {
		var e *Error
		if !errors.As(err, &e) {
			e = &Error{Code: CodeInternal, Message: err.Error()}
		}
		return ErrorMessage(m.ID, e)
	}
	out, merr := NewMessage(m.ID, TypeOK, result)
	if merr != nil {
		return ErrorMessage(m.ID, &Error{Code: CodeInternal, Message: merr.Error()})
	}
	if encodedLen(out) > MaxMessage {
		return ErrorMessage(m.ID, &Error{Code: CodeInternal, Message: "resposta acima de 64 KB"})
	}
	return out
}

func encodedLen(m Message) int {
	b, err := json.Marshal(m)
	if err != nil {
		return 0
	}
	return len(b)
}

// fitLogTail corta o começo do texto até a resposta "ok" com ele caber em
// MaxMessage. O JSON pode expandir o texto ('<', controles e bytes
// inválidos viram \uXXXX), então o corte soma, do fim para o começo, o
// tamanho codificado de cada caractere (estimado por cima).
func fitLogTail(id, text string) string {
	budget := MaxMessage - encodedLen(MustMessage(id, TypeOK, LogTail{}))
	start, used := len(text), 0
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:start])
		n := jsonRuneLen(r, size)
		if used+n > budget {
			break
		}
		used += n
		start -= size
	}
	return text[start:]
}

// jsonRuneLen é um limite superior do tamanho de r no encoding/json.
func jsonRuneLen(r rune, size int) int {
	switch {
	case r == '\n', r == '\r', r == '\t', r == '"', r == '\\':
		return 2
	case r == utf8.RuneError && size == 1, r < 0x20, r == '<', r == '>', r == '&', r == 0x2028, r == 0x2029:
		return 6
	}
	return size
}

func (s *Server) call(m Message) (any, error) {
	b := s.Backend
	switch m.Type {
	case TypeStatus:
		return b.Status(), nil
	case TypeCheckNow, TypeReconnect, TypeResume:
		var r VPNRef
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		switch m.Type {
		case TypeCheckNow:
			return nil, b.CheckNow(r.VPN)
		case TypeReconnect:
			return nil, b.Reconnect(r.VPN)
		}
		return nil, b.Resume(r.VPN)
	case TypePause:
		var r PauseRequest
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		var until *time.Time
		if r.UntilUnix != nil {
			t := time.Unix(*r.UntilUnix, 0)
			until = &t
		}
		return nil, b.Pause(r.VPN, until)
	case TypeSetEnabled:
		var r SetEnabledRequest
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		return nil, b.SetEnabled(r.VPN, r.Enabled)
	case TypeAddVPN:
		var r AddVPNRequest
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		return nil, b.AddVPN(r.Config)
	case TypeUpdateVPN:
		var r UpdateVPNRequest
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		return nil, b.UpdateVPN(r.Name, r.Config)
	case TypeRemoveVPN:
		var r RemoveVPNRequest
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		return nil, b.RemoveVPN(r.Name)
	case TypeListRasEntries:
		e, err := b.ListRasEntries()
		return RasEntries{Entries: e}, err
	case TypeGetConfig:
		return b.GetConfig(), nil
	case TypeSetGlobal:
		var r SetGlobalRequest
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		return nil, b.SetGlobal(r)
	case TypeLogTail:
		var r LogTailRequest
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		if r.MaxBytes <= 0 || r.MaxBytes > MaxMessage-1024 {
			r.MaxBytes = MaxMessage - 1024
		}
		t, err := b.LogTail(r.MaxBytes)
		if err != nil {
			return nil, err
		}
		return LogTail{Text: fitLogTail(m.ID, t)}, nil
	}
	return nil, &Error{Code: CodeUnknownType, Message: fmt.Sprintf("tipo %q desconhecido", m.Type)}
}
