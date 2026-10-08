// Package svc integra com o Service Control Manager: ciclo de vida do
// serviço, eventos de energia, instalação manual e consulta do PID.
// A lógica do laço de controle (Loop) é neutra e testada no Linux; o
// adaptador Windows só traduz tipos.
package svc

import (
	"context"
	"time"
)

// Nome e textos do serviço (§8).
const (
	ServiceName        = "VPNMonitor"
	DisplayName        = "VPN Monitor"
	Description        = "Mantém as VPNs nativas do Windows conectadas e reconecta após quedas."
	DefaultStopTimeout = 10 * time.Second
)

// Política do serviço no SCM (§8, §9), a mesma no `vpnmon-svc install` e no
// MSI. Como a MsiServiceConfigFailureActions do Windows Installer não funciona
// (documentado pela Microsoft), o próprio serviço reaplica recuperação e
// preshutdown a cada partida (EnsurePolicy); o MSI só registra o serviço.
const (
	// PreshutdownTimeout é quanto o SCM espera o serviço no preshutdown.
	PreshutdownTimeout = 15 * time.Second
	// RecoveryReset zera a contagem de falhas após um dia sem falhar.
	RecoveryReset = 24 * time.Hour
)

// RecoveryDelays são as esperas antes de reiniciar após a 1ª, 2ª e 3ª falha.
func RecoveryDelays() []time.Duration {
	return []time.Duration{5 * time.Second, 30 * time.Second, 60 * time.Second}
}

// Dependencies são os serviços de que o VPNMonitor depende.
func Dependencies() []string { return []string{"RasMan"} }

// RecoveryStep é uma ação de recuperação do SCM: reiniciar (ou outra coisa)
// após uma espera.
type RecoveryStep struct {
	Restart bool
	Delay   time.Duration
}

// Policy é a parte da configuração do serviço no SCM que o VPN Monitor
// controla.
type Policy struct {
	Actions     []RecoveryStep
	Reset       time.Duration
	NonCrash    bool // FailureActionsOnNonCrashFailures
	Preshutdown time.Duration
}

// WantedPolicy é a política do spec (§9).
func WantedPolicy() Policy {
	var steps []RecoveryStep
	for _, d := range RecoveryDelays() {
		steps = append(steps, RecoveryStep{Restart: true, Delay: d})
	}
	return Policy{Actions: steps, Reset: RecoveryReset, NonCrash: true, Preshutdown: PreshutdownTimeout}
}

// PolicyChanges diz o que precisa ser gravado. Ações e período de reset vão
// juntos (uma só chamada ao SCM, SERVICE_CONFIG_FAILURE_ACTIONS).
type PolicyChanges struct {
	Recovery, NonCrash, Preshutdown bool
}

// Any diz se há algo a gravar.
func (c PolicyChanges) Any() bool { return c.Recovery || c.NonCrash || c.Preshutdown }

// DiffPolicy compara a política lida do SCM com a desejada. Só o que diverge
// é regravado: regravar as ações de recuperação zera a contagem de falhas do
// SCM, e o serviço reaplica a política a cada partida (inclusive logo depois
// de uma falha, quando a contagem decide a próxima espera).
func DiffPolicy(cur, want Policy) PolicyChanges {
	recovery := cur.Reset != want.Reset || len(cur.Actions) != len(want.Actions)
	for i := 0; !recovery && i < len(want.Actions); i++ {
		recovery = cur.Actions[i] != want.Actions[i]
	}
	return PolicyChanges{
		Recovery:    recovery,
		NonCrash:    cur.NonCrash != want.NonCrash,
		Preshutdown: cur.Preshutdown != want.Preshutdown,
	}
}

// Eventos de energia (pbt.h) que indicam retomada.
const (
	PBT_APMRESUMESUSPEND   = 0x7
	PBT_APMRESUMEAUTOMATIC = 0x12
)

// Cmd é um pedido do SCM já traduzido.
type Cmd int

const (
	CmdStop Cmd = iota
	CmdPreShutdown
	CmdPowerEvent
)

// Request é um pedido do SCM.
type Request struct {
	Cmd       Cmd
	EventType uint32
}

// State é o estado informado ao SCM.
type State int

const (
	StateStartPending State = iota
	StateRunning
	StateStopPending
)

// Hooks é o que o serviço faz.
type Hooks struct {
	// Run sobe tudo e bloqueia até ctx ser cancelado; deve voltar em StopTimeout.
	Run func(ctx context.Context) error
	// OnResume é chamado na retomada de energia, em goroutine própria (não
	// bloqueia o laço do SCM). Uma retomada pode chamá-lo duas vezes
	// (PBT_APMRESUMEAUTOMATIC e PBT_APMRESUMESUSPEND): deve ser idempotente e rápido.
	OnResume func()
	// StopTimeout é o prazo para Run voltar após o pedido de parada.
	StopTimeout time.Duration
}

// Loop é o corpo do handler do SCM. Informa estados por report e devolve o
// código de saída: 0 em parada pedida (mesmo que Run estoure o prazo, para
// não disparar a recuperação do SCM durante parada/atualização); 1 só se Run
// terminou sozinho com erro.
func Loop(h Hooks, reqs <-chan Request, report func(State)) uint32 {
	if h.StopTimeout <= 0 {
		h.StopTimeout = DefaultStopTimeout
	}
	report(StateStartPending)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	report(StateRunning)
	for {
		select {
		case err := <-done:
			report(StateStopPending)
			if err != nil {
				return 1
			}
			return 0
		case r, ok := <-reqs:
			if !ok {
				r = Request{Cmd: CmdStop}
			}
			switch r.Cmd {
			case CmdStop, CmdPreShutdown:
				report(StateStopPending)
				cancel()
				select {
				case <-done:
					return 0
				case <-time.After(h.StopTimeout):
					return 0
				}
			case CmdPowerEvent:
				if (r.EventType == PBT_APMRESUMEAUTOMATIC || r.EventType == PBT_APMRESUMESUSPEND) && h.OnResume != nil {
					go h.OnResume()
				}
			}
		}
	}
}
