// Package netwatch avisa mudanças de endereço/rota e diz se a máquina tem
// rede: alguma interface ativa com rota padrão que não seja a própria VPN
// (§4.3 passo 5, §4.6).
package netwatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// Watcher observa a rede.
type Watcher interface {
	// Changes entrega as notificações brutas do SO (coalescidas só se o
	// consumidor atrasar); quem consome aplica Debounce. Um Watcher por processo.
	Changes() <-chan struct{}
	// HasNetwork diz se há rede: alguma interface ativa com rota padrão que
	// seja física ou PPP fora de exclude (as entradas RAS monitoradas).
	// Regra completa em DecideNetwork.
	HasNetwork(exclude []string) (bool, error)
	Close() error
}

// Tipos de interface (ipifcons.h) que não são físicos; desses, só PPP fora
// das entradas monitoradas conta como rede (DecideNetwork).
const (
	ifTypeSoftwareLoopback = 24
	ifTypePPP              = 23
	ifTypePropVirtual      = 53
	ifTypeTunnel           = 131
)

// ErrInterfaceGone marca a interface que sumiu entre a leitura das rotas e a
// da interface: não conta nem como rede nem como falha.
var ErrInterfaceGone = errors.New("interface sumiu entre as leituras")

// Route é uma rota já combinada com os dados da interface. Err≠nil: a
// leitura da interface falhou (ErrInterfaceGone se ela sumiu) e os demais
// campos da interface não valem.
type Route struct {
	PrefixLen uint8
	IfType    uint32
	// Alias é o nome da interface; numa interface PPP é o nome da entrada
	// RAS/conexão discada.
	Alias  string
	OperUp bool
	Err    error
}

// IsVirtualIfType diz se o tipo é PPP/RAS, túnel, loopback ou virtual.
func IsVirtualIfType(t uint32) bool {
	switch t {
	case ifTypeSoftwareLoopback, ifTypePPP, ifTypePropVirtual, ifTypeTunnel:
		return true
	}
	return false
}

// DecideNetwork decide a partir das rotas se há rede: alguma rota padrão (/0)
// em interface ativa que seja física, ou PPP (IfType 23) com alias não vazio
// que não seja uma das entradas em exclude (comparação sem diferenciar
// maiúsculas, após trim). PPP com alias vazio não conta (conservador: não dá
// para saber se é a VPN monitorada). Premissa: no Windows toda conexão RAS
// (PPTP, L2TP, SSTP, IKEv2) e o PPPoE aparecem como PPP com o nome da entrada
// como alias — ainda A VALIDAR numa máquina real. Assim a VPN monitorada
// nunca conta, mas um PPPoE (ou outra discagem não monitorada) conta. Túneis,
// loopback e virtuais (131, 53, 24) nunca contam.
//
// Interface que sumiu (ErrInterfaceGone) é ignorada; se todas as demais rotas
// padrão falharam na leitura, o resultado é inconclusivo e volta erro.
func DecideNetwork(routes []Route, exclude []string) (bool, error) {
	defaults, failed, gone := 0, 0, 0
	var lastErr error
	found := false
	for _, r := range routes {
		if r.PrefixLen != 0 {
			continue
		}
		defaults++
		switch {
		case errors.Is(r.Err, ErrInterfaceGone):
			gone++
		case r.Err != nil:
			failed++
			lastErr = r.Err
		case r.OperUp && countsAsNetwork(r, exclude):
			found = true
		}
	}
	if found {
		return true, nil
	}
	if failed > 0 && failed == defaults-gone {
		return false, fmt.Errorf("lendo as interfaces das rotas padrão: %w", lastErr)
	}
	return false, nil
}

func countsAsNetwork(r Route, exclude []string) bool {
	if !IsVirtualIfType(r.IfType) {
		return true
	}
	if r.IfType != ifTypePPP {
		return false
	}
	alias := strings.TrimSpace(r.Alias)
	if alias == "" {
		return false
	}
	for _, e := range exclude {
		if strings.EqualFold(alias, strings.TrimSpace(e)) {
			return false
		}
	}
	return true
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
