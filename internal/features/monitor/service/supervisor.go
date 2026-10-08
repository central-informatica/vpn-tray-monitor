package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"runtime/debug"
	"sync"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/adapters"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// ErrStopped é devolvido a comandos enviados a um supervisor encerrado.
var ErrStopped = errors.New("supervisor encerrado")

// LinkProber, Dialer e Fingerprinter são o que o supervisor usa de fora.
type LinkProber interface {
	Probe(ctx context.Context, entry string) (adapters.LinkResult, error)
}

type Dialer interface {
	Dial(ctx context.Context, job adapters.DialJob) adapters.DialOutcome
}

type Fingerprinter interface {
	Fingerprint(ctx context.Context, name, entry string) string
}

// Update é publicado a cada mudança do estado de uma VPN.
type Update struct {
	Name    string
	Status  domain.Status
	Notices []domain.Notice
	// PauseChanged indica que a pausa mudou e precisa ir para state.json.
	PauseChanged bool
}

// DefaultStopWait é quanto Run espera as operações canceladas voltarem.
const DefaultStopWait = 5 * time.Second

// Deps são as dependências de um supervisor.
type Deps struct {
	Clock   shared.Clock
	Link    LinkProber
	Checker adapters.Checker
	Dialer  Dialer
	Queue   *DialQueue
	Creds   Fingerprinter
	Rand    func() float64
	Log     *slog.Logger
	// OnUpdate roda dentro do ator: não pode bloquear nem chamar métodos do
	// supervisor de forma síncrona (deadlock). Entregue a outra goroutine
	// ou a um canal com folga.
	OnUpdate func(Update)
	// StopWait limita a espera, em tempo real, pelas operações canceladas
	// ao sair do Run (parada ou panic); zero = DefaultStopWait. Operação que
	// não volta a tempo é abandonada e registrada no log.
	StopWait time.Duration
}

type message struct {
	in    domain.Input
	reply chan domain.Reply
	// Resultados de operação levam a geração; os de gerações antigas
	// (canceladas) são descartados.
	result bool
	gen    uint64
	panic  string
	// lingering: a sonda achou a entrada entre as conexões ativas, mas não
	// conectada (handle preso). A discagem seguinte desliga antes.
	lingering bool
	// probeErr: a sonda de enlace falhou (resultado inconclusivo).
	probeErr bool
}

// Supervisor é o ator de uma VPN: uma goroutine dona exclusiva do estado.
type Supervisor struct {
	vpn     config.VPN
	params  domain.Params
	deps    Deps
	initial domain.Status

	in      chan message
	wake    chan struct{}
	stopped chan struct{}
}

// NewSupervisor cria o ator; chame Run numa goroutine.
func NewSupervisor(v config.VPN, initial domain.Status, deps Deps) *Supervisor {
	if deps.Rand == nil {
		deps.Rand = func() float64 { return 0.5 }
	}
	if deps.Log == nil {
		deps.Log = slog.New(slog.DiscardHandler)
	}
	if deps.OnUpdate == nil {
		deps.OnUpdate = func(Update) {}
	}
	if deps.StopWait <= 0 {
		deps.StopWait = DefaultStopWait
	}
	return &Supervisor{
		vpn: v, params: domain.ParamsFrom(v), deps: deps, initial: initial,
		in: make(chan message), wake: make(chan struct{}, 1), stopped: make(chan struct{}),
	}
}

// Wake pede uma reavaliação (desconexão RAS, mudança de rede). Vários
// pedidos pendentes viram um só ciclo.
func (s *Supervisor) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Send entrega uma entrada (comando ou evento) e espera a resposta. Como
// todos os métodos de comando (CheckNow, Reconnect, Pause, Resume,
// CredentialChanged, PowerResume), bloqueia até o ator atender: o chamador
// deve passar um ctx com prazo.
func (s *Supervisor) Send(ctx context.Context, in domain.Input) (domain.Reply, error) {
	m := message{in: in, reply: make(chan domain.Reply, 1)}
	select {
	case s.in <- m:
	case <-s.stopped:
		return domain.Reply{}, ErrStopped
	case <-ctx.Done():
		return domain.Reply{}, ctx.Err()
	}
	select {
	case r := <-m.reply:
		return r, nil
	case <-s.stopped:
		return domain.Reply{}, ErrStopped
	case <-ctx.Done():
		return domain.Reply{}, ctx.Err()
	}
}

// CheckNow pede uma verificação imediata.
func (s *Supervisor) CheckNow(ctx context.Context) (domain.Reply, error) {
	return s.Send(ctx, domain.Input{Kind: domain.InCheckNow})
}

// Reconnect pede reconexão manual, com a impressão digital atual da
// credencial (o domínio recusa repetir uma credencial já rejeitada).
func (s *Supervisor) Reconnect(ctx context.Context) (domain.Reply, error) {
	return s.Send(ctx, domain.Input{Kind: domain.InReconnect, Fingerprint: s.fingerprint(ctx)})
}

// Pause pausa até until (zero = indefinida).
func (s *Supervisor) Pause(ctx context.Context, until time.Time) (domain.Reply, error) {
	return s.Send(ctx, domain.Input{Kind: domain.InPause, PauseUntil: until})
}

// Resume retoma.
func (s *Supervisor) Resume(ctx context.Context) (domain.Reply, error) {
	return s.Send(ctx, domain.Input{Kind: domain.InResume, Fingerprint: s.fingerprint(ctx)})
}

// CredentialChanged avisa que o cofre mudou (o domínio ignora se a
// impressão digital desta VPN não mudou).
func (s *Supervisor) CredentialChanged(ctx context.Context) {
	_, _ = s.Send(ctx, domain.Input{Kind: domain.InCredentialChanged, Fingerprint: s.fingerprint(ctx)})
}

// PowerResume avisa retomada de energia ou salto de relógio.
func (s *Supervisor) PowerResume(ctx context.Context) {
	_, _ = s.Send(ctx, domain.Input{Kind: domain.InPowerResume})
}

func (s *Supervisor) fingerprint(ctx context.Context) string {
	if s.deps.Creds == nil {
		return ""
	}
	return s.deps.Creds.Fingerprint(ctx, s.vpn.Name, s.vpn.RasEntry)
}

// Run executa o ator até ctx terminar. Ao sair (ou num panic), cancela as
// operações em andamento (a discagem desliga com RasHangUp) e espera elas
// voltarem por no máximo Deps.StopWait; depois disso as abandona.
func (s *Supervisor) Run(ctx context.Context) {
	defer close(s.stopped)
	ctx, stopOps := context.WithCancel(ctx)
	var (
		status   = s.initial
		gen      uint64
		cancelOp context.CancelFunc
		ops      sync.WaitGroup
		// lingering vem da última sonda de enlace: há handle preso da entrada.
		lingering bool
		// pendingWake guarda um despertar chegado durante uma operação: ele
		// vira um só ciclo quando ela terminar, em vez de se perder.
		pendingWake bool
	)
	defer func() {
		// Cancela todas as operações, inclusive as de gerações antigas, e
		// libera as que estejam tentando entregar resultado.
		stopOps()
		done := make(chan struct{})
		go func() { ops.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(s.deps.StopWait):
			s.deps.Log.Error("operação não voltou após o cancelamento; abandonada",
				"vpn", s.vpn.Name, "prazo", s.deps.StopWait)
		}
	}()

	timer := s.deps.Clock.NewTimer(time.Hour)
	stopTimer := func() {
		if !timer.Stop() {
			select { // Stop do FakeClock não drena: descarta disparo velho
			case <-timer.C():
			default:
			}
		}
	}
	stopTimer()
	defer timer.Stop()
	arm := func() {
		stopTimer()
		if status.NextTick.IsZero() {
			return
		}
		d := status.NextTick.Sub(s.deps.Clock.Now())
		if d < 0 {
			d = 0
		}
		timer.Reset(d)
	}

	apply := func(in domain.Input) *domain.Reply {
		dec := domain.Decide(status, in, s.params, domain.Env{Now: s.deps.Clock.Now(), Rand: s.deps.Rand()})
		// Cancelar pedido, ou uma operação nova substituindo a anterior:
		// a geração muda e o resultado da antiga será descartado.
		if (dec.Cancel || dec.Action != domain.OpNone) && cancelOp != nil {
			cancelOp()
			cancelOp = nil
		}
		if dec.Cancel {
			gen++
		}
		prev := status
		status = dec.Next
		if dec.Action != domain.OpNone {
			gen++
			opCtx, cancel := context.WithCancel(ctx)
			cancelOp = cancel
			hangupFirst := dec.Action == domain.OpHangupDial || (dec.Action == domain.OpDial && lingering)
			if dec.Action == domain.OpDial || dec.Action == domain.OpHangupDial {
				lingering = false
			}
			ops.Add(1)
			go s.execute(ctx, opCtx, gen, dec.Action, hangupFirst, dec.Manual, &ops)
		}
		for _, n := range dec.Notices {
			s.deps.Log.Info(n.Text, "aviso", string(n.Kind))
		}
		if prev.State != status.State {
			s.deps.Log.Info("estado", "de", string(prev.State), "para", string(status.State))
		}
		arm() // antes de publicar: quem observa já encontra o temporizador armado
		if len(dec.Notices) > 0 || !reflect.DeepEqual(prev, status) {
			s.deps.OnUpdate(Update{
				Name: s.vpn.Name, Status: status, Notices: dec.Notices,
				PauseChanged: prev.PauseRecord() != status.PauseRecord(),
			})
		}
		return dec.Reply
	}
	wake := func() {
		if status.Op != domain.OpNone {
			pendingWake = true
			return
		}
		pendingWake = false
		// Um despertar que já esteja no canal chegou antes deste ciclo e
		// é coberto por ele: descartá-lo evita um ciclo a mais.
		select {
		case <-s.wake:
		default:
		}
		apply(domain.Input{Kind: domain.InWake})
	}

	arm()
	s.deps.OnUpdate(Update{Name: s.vpn.Name, Status: status})
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C():
			apply(domain.Input{Kind: domain.InTick})
		case <-s.wake:
			wake()
		case m := <-s.in:
			switch {
			case m.panic != "":
				panic(m.panic) // o orquestrador recupera e recria o supervisor
			case m.result && m.gen != gen:
				// operação cancelada: resultado velho, descartado
			case m.result:
				if cancelOp != nil {
					cancelOp() // a operação já voltou; libera o contexto
					cancelOp = nil
				}
				if m.in.Kind == domain.InLinkResult {
					lingering = m.lingering
					if m.probeErr {
						// Inconclusivo: mantém o enlace como estava e deixa o
						// alcance decidir; se estava caída, disca como antes.
						m.in.LinkUp = status.State == domain.Conectada || status.State == domain.Degradada
						m.in.Network = true
					}
				}
				apply(m.in)
			default:
				r := apply(m.in)
				if r == nil {
					r = &domain.Reply{}
				}
				m.reply <- *r
			}
		}
		if pendingWake && status.Op == domain.OpNone {
			wake()
		}
	}
}

// execute roda uma operação fora do ator e devolve o resultado como entrada.
func (s *Supervisor) execute(runCtx, ctx context.Context, gen uint64, op domain.Op, hangupFirst, manual bool, wg *sync.WaitGroup) {
	defer wg.Done()
	deliver := func(m message) {
		m.gen = gen
		select {
		case s.in <- m:
		case <-runCtx.Done():
		}
	}
	defer func() {
		if r := recover(); r != nil {
			deliver(message{panic: fmt.Sprintf("panic na operação %v: %v\n%s", op, r, debug.Stack())})
		}
	}()
	entry := s.vpn.RasEntry
	switch op {
	case domain.OpProbeLink:
		res, err := s.deps.Link.Probe(ctx, entry)
		if err != nil {
			s.deps.Log.Warn("consultando o enlace", "erro", err)
			res = adapters.LinkResult{}
		}
		deliver(message{result: true, probeErr: err != nil, lingering: !res.Up && res.Handle != 0,
			in: domain.Input{Kind: domain.InLinkResult, LinkUp: res.Up, Network: res.Network}})
	case domain.OpProbeReach:
		r := s.deps.Checker.Check(ctx)
		if r.Err != nil {
			s.deps.Log.Debug("verificação de alcance", "erro", r.Err)
		}
		deliver(message{result: true, in: domain.Input{Kind: domain.InReachResult, ReachOK: r.OK, RTT: r.RTT}})
	case domain.OpDial, domain.OpHangupDial:
		release, err := s.deps.Queue.Acquire(ctx, manual)
		if err != nil {
			return // cancelada enquanto esperava a fila
		}
		defer release()
		s.deps.Log.Info("discando", "manual", manual, "desligaAntes", hangupFirst)
		out := s.deps.Dialer.Dial(ctx, adapters.DialJob{
			Name: s.vpn.Name, Entry: entry, HangupFirst: hangupFirst,
			Timeout: time.Duration(s.vpn.ConnectTimeoutSeconds) * time.Second,
		})
		if out.Cancelled {
			return
		}
		if out.Err != nil {
			s.deps.Log.Warn("discagem falhou", "classe", out.Err.Class.String(), "codigo", out.Err.Code, "mensagem", out.Err.Message)
		}
		deliver(message{result: true, in: domain.Input{Kind: domain.InDialResult, DialErr: out.Err, Fingerprint: out.Fingerprint}})
	}
}
