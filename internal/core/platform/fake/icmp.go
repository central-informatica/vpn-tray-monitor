package fake

import (
	"context"
	"sync"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/icmp"
)

// Pinger é um icmp.Pinger roteirizável.
type Pinger struct {
	mu        sync.Mutex
	reachable map[string]bool
	errs      map[string]error
	block     bool
	hang      chan struct{}
	calls     int
	// RTT devolvido nos sucessos.
	RTT time.Duration
}

// NewPinger cria o fake; hosts não configurados não respondem.
func NewPinger() *Pinger {
	return &Pinger{reachable: map[string]bool{}, errs: map[string]error{}, RTT: 12 * time.Millisecond}
}

// SetReachable define se host responde.
func (p *Pinger) SetReachable(host string, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reachable[host] = ok
}

// SetError faz Ping(host) falhar com err (nil remove).
func (p *Pinger) SetError(host string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errs[host] = err
}

// SetBlock faz Ping esperar o ctx terminar (verificação travada).
func (p *Pinger) SetBlock(b bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.block = b
}

// SetHang(true) faz Ping travar ignorando o ctx, como o IcmpSendEcho2 real
// (síncrono); SetHang(false) solta as chamadas presas.
func (p *Pinger) SetHang(h bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if h && p.hang == nil {
		p.hang = make(chan struct{})
	}
	if !h && p.hang != nil {
		close(p.hang)
		p.hang = nil
	}
}

// Calls conta as chamadas.
func (p *Pinger) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *Pinger) Ping(ctx context.Context, host string, _ time.Duration) (icmp.Result, error) {
	p.mu.Lock()
	p.calls++
	block, hang, err, ok, rtt := p.block, p.hang, p.errs[host], p.reachable[host], p.RTT
	p.mu.Unlock()
	if hang != nil {
		<-hang
	}
	if block {
		<-ctx.Done()
		return icmp.Result{}, ctx.Err()
	}
	if err != nil {
		return icmp.Result{}, err
	}
	if !ok {
		return icmp.Result{Status: icmp.IP_REQ_TIMED_OUT}, nil
	}
	return icmp.Result{OK: true, RTT: rtt}, nil
}

var _ icmp.Pinger = (*Pinger)(nil)
