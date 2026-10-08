package fake

import (
	"sync"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/acl"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/netwatch"
)

// Net é um netwatch.Watcher controlável.
type Net struct {
	mu       sync.Mutex
	physical bool
	ch       chan struct{}
}

// NewNet cria o fake com rede física presente.
func NewNet() *Net { return &Net{physical: true, ch: make(chan struct{}, 1)} }

// SetPhysical liga/desliga a rede física e emite um aviso de mudança.
func (n *Net) SetPhysical(ok bool) {
	n.mu.Lock()
	n.physical = ok
	n.mu.Unlock()
	select {
	case n.ch <- struct{}{}:
	default:
	}
}

func (n *Net) Changes() <-chan struct{} { return n.ch }

func (n *Net) HasPhysicalDefaultRoute() (bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.physical, nil
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
