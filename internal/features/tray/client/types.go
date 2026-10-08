// Package client é o cliente do pipe da bandeja: mantém uma conexão inscrita
// nos eventos do serviço, reconecta sozinho e repassa os pedidos do menu.
// Não conhece walk nem o view-model; fala só pelos tipos de core/ipc.
package client

import (
	"errors"
	"sync/atomic"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

// ConnState é o estado da conexão com o serviço.
type ConnState int

const (
	// Connecting: ainda não houve resposta (início da bandeja).
	Connecting ConnState = iota
	// Connected: hello trocado e inscrito nos eventos.
	Connected
	// Unavailable: serviço ausente, parado ou conexão perdida; tentando de novo.
	Unavailable
	// Stopping: o serviço avisou que está parando (serviceStopping).
	Stopping
	// Incompatible: protocolo diferente; "atualize o VPN Monitor".
	Incompatible
	// NotService: o pipe é servido por outro processo (PID ≠ serviço).
	NotService
)

func (s ConnState) String() string {
	switch s {
	case Connecting:
		return "conectando"
	case Connected:
		return "conectado"
	case Unavailable:
		return "indisponível"
	case Stopping:
		return "parando"
	case Incompatible:
		return "incompatível"
	case NotService:
		return "pipe não é do serviço"
	}
	return "?"
}

// Conn descreve a conexão. Message traz o motivo (erro do dial, do hello…).
type Conn struct {
	State         ConnState
	Message       string
	ServerVersion string
}

// EventKind diz qual campo de Event vale.
type EventKind int

const (
	EvConn EventKind = iota
	EvSnapshot
	EvVPNState
	EvNotice
	EvConfigStatus
)

// Event é o que a bandeja recebe: mudança de conexão ou evento do serviço.
type Event struct {
	Kind         EventKind
	Conn         Conn
	Snapshot     ipc.Snapshot
	VPN          ipc.VPNView
	Notice       ipc.NoticeEvent
	ConfigStatus ipc.ConfigStatus
}

// ErrNotConnected: não há conexão com o serviço para enviar o pedido.
var ErrNotConnected = errors.New("sem conexão com o serviço VPN Monitor")

// Stats conta o que a decodificação tolerante deixou passar (serviço mais
// novo que a bandeja): eventos de tipo desconhecido ou ilegíveis, que são
// descartados, e mensagens com campos que esta bandeja não conhece, que são
// aproveitadas sem eles. Aparece em "Sobre" (a bandeja não grava log).
type Stats struct {
	DroppedEvents int64
	UnknownFields int64
}

type counters struct{ events, fields atomic.Int64 }

func (c *counters) stats() Stats {
	return Stats{DroppedEvents: c.events.Load(), UnknownFields: c.fields.Load()}
}
