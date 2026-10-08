package adapters

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/netwatch"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

// LinkResult é o resultado da sonda de enlace.
type LinkResult struct {
	Up      bool // a entrada RAS está conectada
	Network bool // há rede (física ou PPP não monitorada) com rota padrão
	// Handle da conexão ativa da entrada. Com Up=false e Handle≠0 a entrada
	// está presa (ativa sem conectar): a discagem precisa desligar antes.
	Handle ras.Handle
}

// LinkProber consulta o RAS e a rede.
type LinkProber struct {
	RAS ras.Client
	Net netwatch.Watcher
	// Monitored são as entradas RAS das VPNs da config: uma interface PPP
	// com um desses nomes nunca conta como rede. nil = só a entrada sondada.
	Monitored *Entries
}

// Entries é o conjunto de entradas RAS monitoradas, trocado a cada mudança
// da config e lido a cada sonda; seguro para uso concorrente. O valor zero
// (e o ponteiro nil) é o conjunto vazio.
type Entries struct {
	p atomic.Pointer[[]string]
}

// EntriesOf devolve o conjunto com as entradas de todas as VPNs da config,
// inclusive as desativadas (discadas à mão continuam sendo VPN, não rede).
func EntriesOf(c config.Config) *Entries {
	e := new(Entries)
	e.SetFrom(c)
	return e
}

// SetFrom troca o conjunto pelas entradas das VPNs de c.
func (e *Entries) SetFrom(c config.Config) {
	names := make([]string, 0, len(c.VPNs))
	for _, v := range c.VPNs {
		names = append(names, v.RasEntry)
	}
	e.Set(names)
}

// Set troca o conjunto (copia names).
func (e *Entries) Set(names []string) {
	c := slices.Clone(names)
	e.p.Store(&c)
}

// List devolve o conjunto atual; não alterar o retorno.
func (e *Entries) List() []string {
	if e == nil {
		return nil
	}
	if p := e.p.Load(); p != nil {
		return *p
	}
	return nil
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

// Probe diz se o enlace está de pé e se há rede (netwatch.DecideNetwork,
// excluindo as entradas monitoradas e a própria entry). Com o enlace de pé
// a rede é presumida. Falha ao consultar rotas não impede discar.
// Erro só quando nem a enumeração funciona (resultado inconclusivo); entrada
// enumerada com Status falhando é handle em desmontagem: caída e presa.
func (p LinkProber) Probe(_ context.Context, entry string) (LinkResult, error) {
	var lingering ras.Handle
	a, found, err := FindActive(p.RAS, entry)
	if err != nil {
		return LinkResult{}, err
	}
	if found {
		st, err := p.RAS.Status(a.Handle)
		if err == nil && st.State == ras.StateConnected {
			return LinkResult{Up: true, Network: true, Handle: a.Handle}, nil
		}
		lingering = a.Handle
	}
	network := true
	if p.Net != nil {
		exclude := append(slices.Clone(p.Monitored.List()), entry)
		if ok, err := p.Net.HasNetwork(exclude); err == nil {
			network = ok
		}
	}
	return LinkResult{Network: network, Handle: lingering}, nil
}
