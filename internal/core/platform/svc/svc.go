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
