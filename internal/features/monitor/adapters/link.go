package adapters

import (
	"context"
	"strings"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/netwatch"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

// LinkResult é o resultado da sonda de enlace.
type LinkResult struct {
	Up      bool // a entrada RAS está conectada
	Network bool // há interface física com rota padrão
	Handle  ras.Handle
}

// LinkProber consulta o RAS e a rede.
type LinkProber struct {
	RAS ras.Client
	Net netwatch.Watcher
}

// FindActive procura a conexão ativa da entrada (o Windows não diferencia
// maiúsculas em nomes de entrada).
func FindActive(c ras.Client, entry string) (ras.ActiveConn, bool, error) {
	conns, err := c.Active()
	if err != nil {
		return ras.ActiveConn{}, false, err
	}
	for _, a := range conns {
		if strings.EqualFold(a.Entry, entry) {
			return a, true, nil
		}
	}
	return ras.ActiveConn{}, false, nil
}

// Probe diz se o enlace está de pé e se há rede física. Com o enlace de pé
// a rede é presumida. Falha ao consultar rotas não impede discar.
func (p LinkProber) Probe(_ context.Context, entry string) (LinkResult, error) {
	a, found, err := FindActive(p.RAS, entry)
	if err != nil {
		return LinkResult{}, err
	}
	if found {
		st, err := p.RAS.Status(a.Handle)
		if err != nil {
			return LinkResult{}, err
		}
		if st.State == ras.StateConnected {
			return LinkResult{Up: true, Network: true, Handle: a.Handle}, nil
		}
	}
	network := true
	if p.Net != nil {
		if ok, err := p.Net.HasPhysicalDefaultRoute(); err == nil {
			network = ok
		}
	}
	return LinkResult{Network: network}, nil
}
