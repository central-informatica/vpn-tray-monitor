// Package netwatch avisa mudanças de endereço/rota e diz se a máquina tem
// alguma interface física com rota padrão (§4.3 passo 5, §4.6).
package netwatch

import (
	"context"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// Watcher observa a rede.
type Watcher interface {
	// Changes recebe um aviso (agregado) por notificação bruta do SO.
	Changes() <-chan struct{}
	// HasPhysicalDefaultRoute diz se alguma interface física ativa tem rota padrão.
	HasPhysicalDefaultRoute() (bool, error)
	Close() error
}

// Tipos de interface (ipifcons.h) que não contam como rede física.
const (
	ifTypeSoftwareLoopback = 24
	ifTypePPP              = 23
	ifTypePropVirtual      = 53
	ifTypeTunnel           = 131
)

// Route é uma rota já combinada com os dados da interface.
type Route struct {
	PrefixLen uint8
	IfType    uint32
	OperUp    bool
}

// IsVirtualIfType diz se o tipo é PPP/RAS, túnel, loopback ou virtual.
func IsVirtualIfType(t uint32) bool {
	switch t {
	case ifTypeSoftwareLoopback, ifTypePPP, ifTypePropVirtual, ifTypeTunnel:
		return true
	}
	return false
}

// HasPhysicalDefault decide a partir das rotas: alguma rota padrão (/0) em
// interface física e ativa? A rota padrão da própria VPN não conta.
func HasPhysicalDefault(routes []Route) bool {
	for _, r := range routes {
		if r.PrefixLen == 0 && r.OperUp && !IsVirtualIfType(r.IfType) {
			return true
		}
	}
	return false
}

// DefaultQuiet é a espera após a última notificação (§4.6): a própria
// discagem gera uma rajada delas.
const DefaultQuiet = 2 * time.Second

// Debounce repassa um aviso só depois de quiet sem novas notificações.
func Debounce(ctx context.Context, clock shared.Clock, in <-chan struct{}, quiet time.Duration) <-chan struct{} {
	out := make(chan struct{}, 1)
	go func() {
		var timer shared.Timer
		var fire <-chan time.Time
		for {
			select {
			case <-ctx.Done():
				if timer != nil {
					timer.Stop()
				}
				return
			case _, ok := <-in:
				if !ok {
					return
				}
				if timer == nil {
					timer = clock.NewTimer(quiet)
				} else {
					timer.Reset(quiet)
				}
				fire = timer.C()
			case <-fire:
				fire = nil
				select {
				case out <- struct{}{}:
				default:
				}
			}
		}
	}()
	return out
}
