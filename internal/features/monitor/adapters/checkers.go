// Package adapters liga o domínio do monitor à plataforma: verificações de
// alcance (ping, tcp, link), sonda de enlace e discador RAS.
package adapters

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/icmp"
)

// ReachResult é o resultado de uma verificação de alcance. Err explica por
// que o teste nem pôde ser feito (conta como falha de alcance).
type ReachResult struct {
	OK  bool
	RTT time.Duration
	Err error
}

// Checker verifica o alcance do alvo da VPN.
type Checker interface {
	Check(ctx context.Context) ReachResult
}

// DialFunc abre uma conexão TCP (net.Dialer.DialContext; injetável em teste).
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// NewChecker escolhe o verificador pelo check.kind.
func NewChecker(c config.Check, pinger icmp.Pinger, dial DialFunc) Checker {
	timeout := time.Duration(c.TimeoutSeconds) * time.Second
	switch c.Kind {
	case config.CheckPing:
		return pingChecker{pinger: pinger, host: c.Host, timeout: timeout}
	case config.CheckTCP:
		if dial == nil {
			dial = (&net.Dialer{}).DialContext
		}
		return tcpChecker{dial: dial, addr: net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), timeout: timeout}
	}
	return linkChecker{}
}

type pingChecker struct {
	pinger  icmp.Pinger
	host    string
	timeout time.Duration
}

// Check não fica refém do IcmpSendEcho2, que é síncrono e ignora ctx
// (até 30 s): o eco roda numa goroutine e, se ctx terminar antes (pausa,
// parada do serviço), o resultado é abandonado. A goroutine termina sozinha
// quando o eco esgota o próprio prazo.
func (p pingChecker) Check(ctx context.Context) ReachResult {
	ctx, cancel := context.WithTimeout(ctx, p.timeout+time.Second)
	defer cancel()
	type result struct {
		r   icmp.Result
		err error
	}
	ch := make(chan result, 1)
	go func() {
		r, err := p.pinger.Ping(ctx, p.host, p.timeout)
		ch <- result{r, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			return ReachResult{Err: res.err}
		}
		return ReachResult{OK: res.r.OK, RTT: res.r.RTT}
	case <-ctx.Done():
		return ReachResult{Err: ctx.Err()}
	}
}

type tcpChecker struct {
	dial    DialFunc
	addr    string
	timeout time.Duration
}

func (t tcpChecker) Check(ctx context.Context) ReachResult {
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	start := time.Now()
	c, err := t.dial(ctx, "tcp", t.addr)
	if err != nil {
		return ReachResult{Err: err}
	}
	_ = c.Close()
	return ReachResult{OK: true, RTT: time.Since(start)}
}

// linkChecker: no tipo link o enlace basta; nunca é chamado pelo domínio,
// mas existe para o check único da CLI ter um resultado uniforme.
type linkChecker struct{}

func (linkChecker) Check(context.Context) ReachResult { return ReachResult{OK: true} }
