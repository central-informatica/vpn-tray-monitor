package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// Events, se não nil, recebe o aviso de Accept falhando há ~1 min e a
	// volta ao normal (o serviço liga ao Event Log).
	Events EventReporter
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

// Recusa por excesso de conexões (busy): no máximo refuseMax recusas em
// andamento, cada uma esperando até refuseLinger o cliente fechar.
const (
	refuseMax    = 8
	refuseLinger = time.Second
)

// replyAndClose grava a última mensagem de uma conexão e só fecha depois de
// ler e descartar o que o cliente mandou, até ele fechar (EOF) ou o prazo.
// Fechar um socket TCP com dados recebidos e não lidos (o hello do cliente)
// manda RST, e no Windows o RST descarta no cliente a resposta que ele ainda
// não leu ("forcibly closed"). No named pipe o CloseHandle não perde o que
// está no buffer, mas o Server aceita qualquer net.Listener. A leitura começa
// antes da escrita: num transporte sem buffer (net.Pipe) o cliente só passa a
// ler depois de terminar de escrever o hello.
func replyAndClose(c net.Conn, m Message, linger time.Duration) {
	_ = c.SetDeadline(time.Now().Add(linger))
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(io.Discard, io.LimitReader(c, MaxMessage))
	}()
	_ = NewCodec(c).Write(m)
	<-drained
	c.Close()
}

// Espera entre tentativas de Accept após falha transitória (dobra até o teto).
const (
	acceptRetryMin = 5 * time.Millisecond
	acceptRetryMax = time.Second
)

// Serve aceita conexões até ctx terminar; então fecha o listener e todas
// as conexões e espera os atendimentos voltarem. Falhas transitórias de
// Accept são repetidas com espera crescente, sem fechar o listener. Volta com
// erro só se o listener for fechado sem parada pedida (net.ErrClosed).
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.defaults()
	var (
		mu       sync.Mutex
		conns    = map[net.Conn]struct{}{}
		refusing = map[net.Conn]struct{}{} // recusas (busy) em andamento
		wg       sync.WaitGroup
	)
	closeAll := func() {
		ln.Close()
		mu.Lock()
		for c := range conns {
			c.Close()
		}
		for c := range refusing {
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
	var delay time.Duration
	trouble := acceptTrouble{log: s.Log, events: s.Events}
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				// Falha transitória (no go-winio, ConnectNamedPipe/CreateNamedPipe
				// de uma instância): o listener e a primeira instância seguem
				// vivos. Fechar aqui liberaria o nome do pipe para outro processo
				// criá-lo (squatting); então espera e repete, sem derrubar as
				// conexões existentes.
				delay = min(max(2*delay, acceptRetryMin), acceptRetryMax)
				trouble.failed(time.Now(), err) // 1ª falha e depois 1 linha/min; nunca desiste
				select {
				case <-ctx.Done():
				case <-time.After(delay):
				}
				continue
			}
			closeAll()
			wg.Wait()
			if ctx.Err() != nil {
				return nil
			}
			return err // listener fechado de fato (go-winio: ErrPipeListenerClosed == net.ErrClosed)
		}
		delay = 0
		trouble.recovered(time.Now())
		mu.Lock()
		// Conferido sob o mutex: uma conexão aceita junto com a parada não
		// escapa do fechamento em massa.
		stopping := ctx.Err() != nil
		full := len(conns) >= s.MaxConns
		refuse := full && !stopping && len(refusing) < refuseMax
		switch {
		case refuse:
			refusing[c] = struct{}{}
		case !full && !stopping:
			conns[c] = struct{}{}
		}
		mu.Unlock()
		if stopping || (full && !refuse) {
			c.Close() // parando, ou recusas demais em andamento: fecha sem resposta
			continue
		}
		if full {
			wg.Add(1)
			go func() {
				defer wg.Done()
				replyAndClose(c, ErrorMessage("", &Error{Code: CodeBusy, Message: "conexões demais"}), refuseLinger)
				mu.Lock()
				delete(refusing, c)
				mu.Unlock()
			}()
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
		// Primeira linha que nem é envelope: responde antes de fechar, para o
		// cliente saber o motivo. Outros erros = cliente saiu ou prazo.
		var de *DecodeError
		if errors.Is(err, ErrTooLarge) || errors.As(err, &de) {
			cn.send(ErrorMessage("", &Error{Code: CodeBadRequest, Message: "esperava hello: " + err.Error()}))
			cn.flush()
		}
		return
	}
	if herr := checkHello(m); herr != nil {
		cn.send(ErrorMessage(m.ID, herr))
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

// checkHello valida o hello. Um cliente de protocolo futuro pode mandar
// campos que este servidor não conhece: se a decodificação estrita falhar, só
// o campo protocol é lido, de forma tolerante, e uma versão diferente vira
// incompatible (o cliente diz "atualize") em vez de bad_request.
func checkHello(m Message) *Error {
	if m.Type != TypeHello {
		return &Error{Code: CodeBadRequest, Message: "esperava hello"}
	}
	var h Hello
	if derr := DecodePayload(m.Payload, &h); derr != nil {
		var loose struct {
			Protocol *int `json:"protocol"`
		}
		if json.Unmarshal(m.Payload, &loose) != nil || loose.Protocol == nil || *loose.Protocol == ProtocolVersion {
			msg := derr.Error()
			var e *Error
			if errors.As(derr, &e) {
				msg = e.Message
			}
			return &Error{Code: CodeBadRequest, Message: "hello inválido: " + msg}
		}
		h.Protocol = *loose.Protocol
	}
	if h.Protocol != ProtocolVersion {
		return &Error{Code: CodeIncompatible,
			Message: fmt.Sprintf("protocolo %d incompatível com o serviço (%d); atualize o VPN Monitor", h.Protocol, ProtocolVersion)}
	}
	return nil
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
