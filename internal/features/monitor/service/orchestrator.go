package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/icmp"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/adapters"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// Paths são os arquivos do serviço.
type Paths struct {
	ConfigFile string
	StateFile  string
	LogFile    string
}

// Options são as dependências do orquestrador.
type Options struct {
	Paths   Paths
	Clock   shared.Clock
	RAS     ras.Client
	Pinger  icmp.Pinger
	TCPDial adapters.DialFunc
	Link    LinkProber
	Dialer  Dialer
	Creds   Fingerprinter
	Log     *slog.Logger
	Events  logging.EventSink
	Rand    func() float64
	// OnGlobals aplica logLevel e limites de log quando a config muda.
	OnGlobals func(config.Config)
	// RestartDelay é a espera antes de recriar um supervisor que entrou em
	// pânico (dobra a cada repetição, até 60 s). Padrão 5 s.
	RestartDelay time.Duration
	// CommandTimeout limita a espera por um supervisor. Padrão 5 s.
	CommandTimeout time.Duration
	// StopTimeout é o prazo para os supervisores pararem em Stop (e numa
	// recarga). Padrão 7 s, deixando folga nos 10 s que o SCM espera.
	StopTimeout time.Duration
}

// Textos do estado publicado durante a recriação e com o disjuntor aberto.
const (
	restartingText = "reiniciando após falha interna"
	trippedText    = "falha interna repetida; monitoramento desta VPN suspenso — veja o log"
)

// Disjuntor de panics (§4.9): breakerPanics panics seguidos, cada supervisor
// vivendo menos que breakerLife, suspendem a VPN até recarga da config ou
// reconexão manual. Um supervisor que viva breakerLife zera a contagem.
const (
	breakerPanics = 3
	breakerLife   = 10 * time.Minute
)

type running struct {
	vpn    config.VPN
	sup    *Supervisor // nil enquanto recria após panic ou com o disjuntor aberto
	last   domain.Status
	cancel context.CancelFunc
	done   chan struct{}
	// tripped: disjuntor aberto, sem supervisor; revive recebe o pedido de
	// recriação de um reconnect manual e devolve o supervisor novo.
	tripped bool
	revive  chan chan *Supervisor
}

// Orchestrator mantém um supervisor por VPN, distribui eventos e é o
// backend do pipe.
type Orchestrator struct {
	opts    Options
	queue   *DialQueue
	bus     *bus
	applyMu sync.Mutex // serializa Apply e mutações de config

	// stopping fecha no início de Stop, antes de applyMu: uma recarga que
	// esteja esperando supervisores desiste na hora.
	stopping chan struct{}
	stopOnce sync.Once

	mu         sync.Mutex
	ctx        context.Context
	cancelRoot context.CancelFunc
	stopped    bool
	cfg        config.Config
	state      config.State
	sups       map[string]*running
	// draining são os supervisores que uma recarga está parando; Stop
	// espera por eles também.
	draining    map[*running]struct{}
	lastWritten string // hash do último config.json gravado pelo serviço
	// diskInvalid é o problema do config.json em disco, enquanto ele estiver
	// inválido; nesse estado as mudanças pelo pipe são recusadas para não
	// sobrescrever a edição manual (ou o arquivo inteiro, se inválido desde a partida).
	diskInvalid error
}

// New cria o orquestrador com a config e o estado iniciais.
func New(opts Options, cfg config.Config, st config.State) *Orchestrator {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.Events == nil {
		opts.Events = logging.NopSink{}
	}
	if opts.Clock == nil {
		opts.Clock = shared.RealClock{}
	}
	if opts.RestartDelay == 0 {
		opts.RestartDelay = 5 * time.Second
	}
	if opts.CommandTimeout == 0 {
		opts.CommandTimeout = 5 * time.Second
	}
	if opts.StopTimeout == 0 {
		opts.StopTimeout = 7 * time.Second
	}
	if opts.OnGlobals == nil {
		opts.OnGlobals = func(config.Config) {}
	}
	pauses := make(map[string]config.Pause, len(st.Pauses))
	for k, p := range st.Pauses {
		pauses[k] = p
	}
	st.Pauses = pauses
	return &Orchestrator{opts: opts, queue: &DialQueue{}, bus: newBus(256), stopping: make(chan struct{}),
		cfg: cfg, state: st, sups: map[string]*running{}, draining: map[*running]struct{}{}}
}

func errStopping() error {
	return &ipc.Error{Code: ipc.CodeInternal, Message: "serviço parando"}
}

// resumePeriod é o tique do detector de salto de relógio.
const resumePeriod = 5 * time.Second

// Start sobe os supervisores e o detector de retomada. Não bloqueia.
func (o *Orchestrator) Start(ctx context.Context) {
	o.mu.Lock()
	o.ctx, o.cancelRoot = context.WithCancel(ctx)
	root, cfg := o.ctx, o.cfg
	o.mu.Unlock()
	// O detector registra o instante inicial e arma o tique aqui, antes de
	// devolver: um salto de relógio logo após Start não se perde.
	det := &shared.ResumeDetector{Period: resumePeriod}
	det.Observe(o.opts.Clock.Now())
	tick := o.opts.Clock.NewTimer(resumePeriod)
	o.applyMu.Lock()
	err := o.apply(cfg)
	o.applyMu.Unlock()
	if err != nil {
		tick.Stop()
		return
	}
	go o.watchResume(root, det, tick)
}

// Stop encerra os supervisores (discagens em curso são desligadas), avisa
// os assinantes e grava o estado. VPNs conectadas continuam de pé. O prazo
// (StopTimeout) é absoluto, contado da entrada: uma recarga em curso desiste
// ao ver stopping e não soma a espera dela à da parada.
func (o *Orchestrator) Stop() {
	deadline := time.Now().Add(o.opts.StopTimeout)
	o.stopOnce.Do(func() {
		o.mu.Lock()
		o.stopped = true
		if o.cancelRoot != nil {
			o.cancelRoot() // supervisores e detector de retomada
		}
		o.mu.Unlock()
		close(o.stopping)
	})
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	o.mu.Lock()
	all := make([]*running, 0, len(o.sups)+len(o.draining))
	for k, r := range o.sups {
		all = append(all, r)
		delete(o.sups, k)
	}
	for r := range o.draining {
		all = append(all, r)
	}
	o.mu.Unlock()
	o.stopAll(all, deadline, nil)
	o.bus.publish(ipc.MustMessage("", ipc.TypeServiceStopping, struct{}{}))
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := config.SaveState(o.opts.Paths.StateFile, o.state); err != nil {
		o.opts.Log.Error("gravando state.json", "erro", err)
	}
}

// stopAll cancela todos os supervisores de uma vez (param em paralelo, cada
// um esperando suas operações por até StopWait) e espera até deadline (tempo
// real: é o SCM que está esperando). Quem não voltar no prazo é abandonado
// (registrado no log): o estado é gravado mesmo assim e atualizações tardias
// são ignoradas por onUpdate. Fechar abort encerra a espera na hora.
func (o *Orchestrator) stopAll(rs []*running, deadline time.Time, abort <-chan struct{}) {
	if len(rs) == 0 {
		return
	}
	for _, r := range rs {
		r.cancel()
	}
	// Um canal que fica fechado, e não um Timer (cujo C dispara uma vez só).
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	for _, r := range rs {
		select {
		case <-r.done:
		case <-ctx.Done():
			o.opts.Log.Warn("supervisor não parou no prazo; seguindo sem ele", "vpn", r.vpn.Name)
		case <-abort:
			return
		}
	}
}

// apply cria, remove ou reinicia só as VPNs cujo trecho mudou (§4.9), além
// das suspensas pelo disjuntor. Chamar com applyMu travado. Depois de Stop
// não faz nada e devolve erro.
func (o *Orchestrator) apply(cfg config.Config) error {
	o.mu.Lock()
	if o.stopped {
		o.mu.Unlock()
		return errStopping()
	}
	o.cfg = cfg
	var stop []*running
	var start []config.VPN
	keep := map[string]bool{}
	for _, v := range cfg.VPNs {
		key := config.NameKey(v.Name)
		keep[key] = true
		if r, ok := o.sups[key]; ok {
			if r.vpn == v && !r.tripped {
				continue
			}
			stop = append(stop, r)
			delete(o.sups, key)
		}
		start = append(start, v)
	}
	for key, r := range o.sups {
		if !keep[key] {
			stop = append(stop, r)
			delete(o.sups, key)
		}
	}
	for _, r := range stop {
		o.draining[r] = struct{}{}
	}
	pausesChanged := false
	for key := range o.state.Pauses {
		if !keep[key] {
			delete(o.state.Pauses, key)
			pausesChanged = true
		}
	}
	if pausesChanged {
		if err := config.SaveState(o.opts.Paths.StateFile, o.state); err != nil {
			o.opts.Log.Error("gravando state.json", "erro", err)
		}
	}
	o.mu.Unlock()

	o.stopAll(stop, time.Now().Add(o.opts.StopTimeout), o.stopping)

	o.mu.Lock()
	defer o.mu.Unlock()
	for _, r := range stop {
		delete(o.draining, r)
	}
	if o.stopped {
		return errStopping() // Stop chegou durante a espera: nada é relançado
	}
	for _, v := range start {
		o.sups[config.NameKey(v.Name)] = o.launch(v)
	}
	// Publicado com o.mu travado: nenhum vpnState novo passa na frente.
	o.bus.publish(ipc.MustMessage("", ipc.TypeSnapshot, o.snapshotLocked()))
	return nil
}

// launch sobe o supervisor sob recover. Panic → log, Event Log e recriação
// após RestartDelay (dobrando até 60 s; volta ao início se o supervisor
// rodou mais de 1 min), a partir do último estado (domain.Restart: guarda
// bloqueio e backoff). breakerPanics panics rápidos seguidos abrem o
// disjuntor. Chamar com o.mu travado.
func (o *Orchestrator) launch(v config.VPN) *running {
	ctx, cancel := context.WithCancel(o.ctx)
	r := &running{vpn: v, cancel: cancel, done: make(chan struct{}), revive: make(chan chan *Supervisor)}
	key := config.NameKey(v.Name)
	log := logging.ForVPN(o.opts.Log, v.Name)
	params := domain.ParamsFrom(v)
	r.last = domain.Initial(params, o.opts.Clock.Now(), o.state.Pauses[key])
	initial := r.last

	go func() {
		defer close(r.done)
		delay := o.opts.RestartDelay
		quick := 0                  // panics seguidos de supervisores de vida curta
		recreated := false          // initial veio de Restart
		var notify chan *Supervisor // reconnect manual esperando o supervisor novo
		for {
			var sup *Supervisor
			sup = NewSupervisor(v, initial, Deps{
				Clock: o.opts.Clock, Link: o.opts.Link, Dialer: o.opts.Dialer, Queue: o.queue,
				Checker: adapters.NewChecker(v.Check, o.opts.Pinger, o.opts.TCPDial),
				Creds:   o.opts.Creds, Rand: o.opts.Rand, Log: log,
				OnUpdate: func(u Update) { o.onUpdate(r, sup, u) },
			})
			o.mu.Lock()
			r.sup = sup
			o.mu.Unlock()
			if notify != nil {
				notify <- sup
				notify = nil
			}
			if recreated && credentialBlocked(initial) {
				// O cofre pode ter mudado durante a recriação: o aviso não
				// pode se perder (o domínio ignora se a impressão não mudou).
				go func() {
					cctx, ccancel := context.WithTimeout(ctx, o.opts.CommandTimeout)
					defer ccancel()
					sup.CredentialChanged(cctx)
				}()
			}
			started := o.opts.Clock.Now()
			p := runRecovered(ctx, sup)
			if p == nil || ctx.Err() != nil {
				return
			}
			lived := o.opts.Clock.Now().Sub(started)
			if lived >= breakerLife {
				quick = 0
			}
			quick++
			if lived > time.Minute {
				delay = o.opts.RestartDelay
			}
			detail := fmt.Sprint(p)
			o.mu.Lock()
			base := r.last // último estado do supervisor que caiu
			r.sup = nil
			o.mu.Unlock()

			if quick >= breakerPanics {
				log.Error("supervisor em pânico repetidamente; monitoramento suspenso", "panics", quick, "panic", detail)
				o.mu.Lock()
				r.tripped = true
				r.last = trippedStatus(base, o.opts.Clock.Now())
				o.publishLocked(r)
				o.mu.Unlock()
				o.opts.Events.Error(fmt.Sprintf("VPN %s: supervisor em pânico: %s", v.Name, firstLine(detail)))
				o.opts.Events.Error(fmt.Sprintf("VPN %s: %s", v.Name, trippedText))
				select {
				case <-ctx.Done():
					return
				case notify = <-r.revive:
				}
				log.Info("reconexão manual: recriando o supervisor suspenso")
				quick, delay = 0, o.opts.RestartDelay
			} else {
				// Arma a espera antes de registrar: quem vê o registro já
				// encontra a recriação agendada.
				t := o.opts.Clock.NewTimer(delay)
				o.mu.Lock()
				r.last = restartingStatus(base, o.opts.Clock.Now())
				o.publishLocked(r)
				o.mu.Unlock()
				log.Error("supervisor em pânico; recriando", "espera", delay, "panic", detail)
				o.opts.Events.Error(fmt.Sprintf("VPN %s: supervisor em pânico, recriando em %s: %s",
					v.Name, delay, firstLine(detail)))
				select {
				case <-ctx.Done():
					t.Stop()
					return
				case <-t.C():
				}
				delay = min(delay*2, time.Minute)
			}
			o.mu.Lock()
			initial = domain.Restart(base, params, o.opts.Clock.Now(), o.state.Pauses[key])
			r.last = initial
			r.tripped = false
			o.mu.Unlock()
			recreated = true
		}
	}()
	return r
}

func credentialBlocked(s domain.Status) bool {
	return s.State == domain.CredencialInvalida || (s.State == domain.Pausada && s.Blocked == domain.CredencialInvalida)
}

// restartingStatus é o estado publicado enquanto o supervisor é recriado:
// não mantém um "Conectada" que ninguém está verificando.
func restartingStatus(base domain.Status, now time.Time) domain.Status {
	s := base
	s.Op = domain.OpNone
	if s.State != domain.Pausada && s.State != domain.Desativada && s.State != domain.Desconhecido {
		s.State, s.Since = domain.Desconhecido, now
	}
	s.NextAttempt = time.Time{}
	s.LastErr = &domain.DialError{Class: ras.ClassTransitorio, Message: restartingText}
	return s
}

// trippedStatus é o estado publicado com o disjuntor aberto.
func trippedStatus(base domain.Status, now time.Time) domain.Status {
	s := base
	s.Op = domain.OpNone
	if s.State != domain.ErroConfig {
		s.State, s.Since = domain.ErroConfig, now
	}
	s.NextAttempt, s.NextTick = time.Time{}, time.Time{}
	s.LastErr = &domain.DialError{Class: ras.ClassConfiguracao, Message: trippedText}
	return s
}

// publishLocked publica o estado atual de r. Chamar com o.mu travado.
func (o *Orchestrator) publishLocked(r *running) {
	o.bus.publish(ipc.MustMessage("", ipc.TypeVPNState, ToView(r.vpn, r.last, o.opts.Clock.Now())))
}

// firstLine corta a pilha que acompanha o panic: o Event Log recebe só o
// resumo; o log do serviço guarda tudo.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// runRecovered roda o ator e devolve o panic dele (nil se saiu normalmente),
// com a pilha do ator anexada.
func runRecovered(ctx context.Context, sup *Supervisor) (p any) {
	defer func() {
		if r := recover(); r != nil {
			p = fmt.Sprintf("%v\n--- pilha do ator ---\n%s", r, debug.Stack())
		}
	}()
	sup.Run(ctx)
	return nil
}

// onUpdate roda dentro do ator do supervisor: só trava o.mu (nunca mantido
// enquanto se espera um supervisor) e publica sem bloquear.
func (o *Orchestrator) onUpdate(r *running, sup *Supervisor, u Update) {
	o.mu.Lock()
	defer o.mu.Unlock()
	key := config.NameKey(r.vpn.Name)
	if o.sups[key] != r || r.sup != sup {
		return // supervisor antigo (reiniciado, em pânico ou removido)
	}
	r.last = u.Status
	if u.PauseChanged {
		// Só a pausa vai para o disco; nada de credencial ou impressão digital.
		if rec := u.Status.PauseRecord(); rec == (config.Pause{}) {
			delete(o.state.Pauses, key)
		} else {
			o.state.Pauses[key] = rec
		}
		if err := config.SaveState(o.opts.Paths.StateFile, o.state); err != nil {
			o.opts.Log.Error("gravando state.json", "erro", err)
		}
	}
	o.publishLocked(r)
	for _, n := range u.Notices {
		o.bus.publish(ipc.MustMessage("", ipc.TypeNotice, ipc.NoticeEvent{VPN: r.vpn.Name, Kind: string(n.Kind), Text: n.Text}))
	}
}

func (o *Orchestrator) snapshotLocked() ipc.Snapshot {
	now := o.opts.Clock.Now()
	snap := ipc.Snapshot{VPNs: []ipc.VPNView{}, Notifications: o.cfg.Notifications}
	for _, v := range o.cfg.VPNs {
		if r, ok := o.sups[config.NameKey(v.Name)]; ok {
			snap.VPNs = append(snap.VPNs, ToView(v, r.last, now))
		}
	}
	return snap
}

// Status devolve o snapshot atual.
func (o *Orchestrator) Status() ipc.Snapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.snapshotLocked()
}

// Subscribe registra um assinante; o primeiro evento é o snapshot. O
// registro é feito sob o mesmo lock que publica as atualizações, para não
// perder evento entre o snapshot e a inscrição.
func (o *Orchestrator) Subscribe() (<-chan ipc.Message, func()) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.bus.subscribe(ipc.MustMessage("", ipc.TypeSnapshot, o.snapshotLocked()))
}

func (o *Orchestrator) supervisors() []*Supervisor {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]*Supervisor, 0, len(o.sups))
	for _, r := range o.sups {
		if r.sup != nil {
			out = append(out, r.sup)
		}
	}
	return out
}

// Wake reavalia todas as VPNs (mudança de rede, desconexão RAS).
func (o *Orchestrator) Wake() {
	for _, s := range o.supervisors() {
		s.Wake()
	}
}

// PowerResume zera o backoff de todas e verifica em 5 s.
func (o *Orchestrator) PowerResume() {
	o.opts.Log.Info("retomada de energia detectada")
	ctx, cancel := context.WithTimeout(context.Background(), o.opts.CommandTimeout)
	defer cancel()
	for _, s := range o.supervisors() {
		s.PowerResume(ctx)
	}
}

// CredentialsChanged avisa todas as VPNs de que o cofre mudou.
func (o *Orchestrator) CredentialsChanged() {
	ctx, cancel := context.WithTimeout(context.Background(), o.opts.CommandTimeout)
	defer cancel()
	for _, s := range o.supervisors() {
		s.CredentialChanged(ctx)
	}
}

// watchResume detecta suspensão por salto de relógio (§4.6): o tique de 5 s
// chega e o relógio de parede andou mais que 2× o menor intervalo além dele.
func (o *Orchestrator) watchResume(ctx context.Context, det *shared.ResumeDetector, t shared.Timer) {
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
		}
		o.mu.Lock()
		det.Threshold = 2 * minInterval(o.cfg)
		o.mu.Unlock()
		if det.Observe(o.opts.Clock.Now()) {
			o.PowerResume()
		}
		t.Reset(resumePeriod)
	}
}

func minInterval(c config.Config) time.Duration {
	m := time.Duration(config.DefaultInterval) * time.Second
	for _, v := range c.VPNs {
		if d := time.Duration(v.IntervalSeconds) * time.Second; d < m {
			m = d
		}
	}
	return m
}

// lookup acha a VPN e o supervisor dela. tripped diz se o disjuntor está
// aberto (aí r vem junto do erro, para o reconnect manual recriar).
func (o *Orchestrator) lookup(name string) (r *running, sup *Supervisor, tripped bool, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopped {
		return nil, nil, false, errStopping()
	}
	r, ok := o.sups[config.NameKey(name)]
	switch {
	case !ok:
		return nil, nil, false, &ipc.Error{Code: ipc.CodeNotFound, Message: fmt.Sprintf("VPN %q não existe", name)}
	case r.tripped:
		return r, nil, true, &ipc.Error{Code: ipc.CodeInternal, Message: trippedText + "; use reconectar para retomar"}
	case r.sup == nil:
		return r, nil, false, &ipc.Error{Code: ipc.CodeInternal, Message: "VPN reiniciando; tente de novo"}
	}
	return r, r.sup, false, nil
}

func replyErr(r domain.Reply, err error) error {
	if errors.Is(err, ErrStopped) || errors.Is(err, context.DeadlineExceeded) {
		return &ipc.Error{Code: ipc.CodeInternal, Message: "supervisor indisponível (reiniciando); tente de novo"}
	}
	if err != nil {
		return err
	}
	codes := map[domain.ReplyCode]string{
		domain.ReplyPaused: ipc.CodePaused, domain.ReplyAlreadyReconnecting: ipc.CodeAlreadyReconnecting,
		domain.ReplyCredentialRejected: ipc.CodeCredentialRejected, domain.ReplyDisabled: ipc.CodeDisabled,
	}
	if c, ok := codes[r.Code]; ok {
		return &ipc.Error{Code: c, Message: r.Message}
	}
	return nil
}

// command envia um comando a uma VPN com prazo (os métodos do supervisor
// bloqueiam até o ator atender).

// command envia um comando a uma VPN com prazo (os métodos do supervisor
// bloqueiam até o ator atender).
func (o *Orchestrator) command(name string, f func(*Supervisor, context.Context) (domain.Reply, error)) error {
	_, s, _, err := o.lookup(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.opts.CommandTimeout)
	defer cancel()
	return replyErr(f(s, ctx))
}

// CheckNow pede verificação imediata.
func (o *Orchestrator) CheckNow(name string) error {
	return o.command(name, (*Supervisor).CheckNow)
}

// Reconnect pede reconexão manual. Com o disjuntor aberto, recria antes o
// supervisor (a partir do último estado: a proteção de credencial vale).
func (o *Orchestrator) Reconnect(name string) error {
	r, s, tripped, err := o.lookup(name)
	if err != nil && !tripped {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.opts.CommandTimeout)
	defer cancel()
	if s == nil {
		unavailable := &ipc.Error{Code: ipc.CodeInternal, Message: "VPN reiniciando; tente de novo"}
		ch := make(chan *Supervisor, 1)
		select {
		case r.revive <- ch:
		case <-r.done:
			return unavailable
		case <-ctx.Done():
			return unavailable
		}
		select {
		case s = <-ch:
		case <-r.done:
			return unavailable
		case <-ctx.Done():
			return unavailable
		}
	}
	return replyErr(s.Reconnect(ctx))
}

// Pause pausa até until (nil = até retomar).
func (o *Orchestrator) Pause(name string, until *time.Time) error {
	var u time.Time
	if until != nil {
		if !until.After(o.opts.Clock.Now()) {
			return &ipc.Error{Code: ipc.CodeBadRequest, Message: "o fim da pausa já passou"}
		}
		u = *until
	}
	return o.command(name, func(s *Supervisor, ctx context.Context) (domain.Reply, error) { return s.Pause(ctx, u) })
}

// Resume retoma.
func (o *Orchestrator) Resume(name string) error {
	return o.command(name, (*Supervisor).Resume)
}
