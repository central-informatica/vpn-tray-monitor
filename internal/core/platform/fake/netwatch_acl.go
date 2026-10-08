package fake

import (
	"slices"
	"sync"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/acl"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/netwatch"
)

// Net é um netwatch.Watcher controlável: uma interface física (ligada ou
// não) e interfaces PPP com rota padrão, decididas por netwatch.DecideNetwork.
type Net struct {
	mu          sync.Mutex
	physical    bool
	ppp         []string
	lastExclude []string
	ch          chan struct{}
}

// NewNet cria o fake com rede física presente.
func NewNet() *Net { return &Net{physical: true, ch: make(chan struct{}, 1)} }

// SetPhysical liga/desliga a rede física e emite um aviso de mudança.
func (n *Net) SetPhysical(ok bool) {
	n.mu.Lock()
	n.physical = ok
	n.mu.Unlock()
	n.notify()
}

// SetPPP troca as interfaces PPP ativas com rota padrão (pelo alias, que é o
// nome da entrada/conexão) e emite um aviso de mudança.
func (n *Net) SetPPP(aliases ...string) {
	n.mu.Lock()
	n.ppp = slices.Clone(aliases)
	n.mu.Unlock()
	n.notify()
}

// LastExclude devolve as exclusões da última chamada a HasNetwork.
func (n *Net) LastExclude() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.lastExclude)
}

func (n *Net) notify() {
	select {
	case n.ch <- struct{}{}:
	default:
	}
}

func (n *Net) Changes() <-chan struct{} { return n.ch }

func (n *Net) HasNetwork(exclude []string) (bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastExclude = slices.Clone(exclude)
	var routes []netwatch.Route
	if n.physical {
		routes = append(routes, netwatch.Route{IfType: 6, OperUp: true}) // Ethernet
	}
	for _, a := range n.ppp {
		routes = append(routes, netwatch.Route{IfType: 23, Alias: a, OperUp: true})
	}
	return netwatch.DecideNetwork(routes, exclude)
}

func (n *Net) Close() error { return nil }

var _ netwatch.Watcher = (*Net)(nil)

// ACL registra as pastas garantidas.
type ACL struct {
	mu   sync.Mutex
	Dirs []string
}

func (a *ACL) EnsureDir(path string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Dirs = append(a.Dirs, path)
	return false, nil
}

var _ acl.Securer = (*ACL)(nil)
