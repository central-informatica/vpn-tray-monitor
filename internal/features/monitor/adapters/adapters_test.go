package adapters

import (
	"context"
	"errors"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/fake"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

func TestPingChecker(t *testing.T) {
	p := fake.NewPinger()
	c := NewChecker(config.Check{Kind: config.CheckPing, Host: "10.0.0.1", TimeoutSeconds: 1}, p, nil)
	if r := c.Check(context.Background()); r.OK {
		t.Fatal("sem resposta")
	}
	p.SetReachable("10.0.0.1", true)
	if r := c.Check(context.Background()); !r.OK || r.RTT != 12*time.Millisecond {
		t.Fatalf("%+v", r)
	}
	p.SetError("10.0.0.1", errors.New("resolver"))
	if r := c.Check(context.Background()); r.OK || r.Err == nil {
		t.Fatalf("erro conta como falha: %+v", r)
	}
}

func TestPingCheckerAbandonsHungEcho(t *testing.T) {
	p := fake.NewPinger()
	p.SetHang(true) // IcmpSendEcho2 real ignora ctx
	defer p.SetHang(false)
	c := NewChecker(config.Check{Kind: config.CheckPing, Host: "10.0.0.1", TimeoutSeconds: 30}, p, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	start := time.Now()
	r := c.Check(ctx)
	if r.OK || r.Err == nil || time.Since(start) > time.Second {
		t.Fatalf("cancelamento deve abandonar o eco preso: %+v em %s", r, time.Since(start))
	}
}

func TestTCPChecker(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	c := NewChecker(config.Check{Kind: config.CheckTCP, Host: "127.0.0.1", Port: addr.Port, TimeoutSeconds: 1}, nil, nil)
	if r := c.Check(context.Background()); !r.OK {
		t.Fatalf("porta aberta: %+v", r)
	}
	ln.Close()
	if r := c.Check(context.Background()); r.OK {
		t.Fatal("porta fechada não é sucesso")
	}
}

func TestLinkProber(t *testing.T) {
	r := fake.NewRAS("VPN Matriz")
	n := fake.NewNet()
	p := LinkProber{RAS: r, Net: n}
	if res, err := p.Probe(context.Background(), "VPN Matriz"); err != nil || res.Up || !res.Network {
		t.Fatalf("caída com rede: %+v %v", res, err)
	}
	n.SetPhysical(false)
	if res, _ := p.Probe(context.Background(), "VPN Matriz"); res.Up || res.Network {
		t.Fatalf("sem rede: %+v", res)
	}
	r.SetActive("VPN Matriz")
	if res, _ := p.Probe(context.Background(), "vpn matriz"); !res.Up || !res.Network {
		t.Fatalf("ativa (nome sem diferenciar maiúsculas): %+v", res)
	}
	r.SetActiveErr(errors.New("rasman parado"))
	if _, err := p.Probe(context.Background(), "VPN Matriz"); err == nil {
		t.Fatal("erro do RAS deve propagar")
	}
}

type stubCreds struct {
	creds Credentials
	err   error
}

func (s stubCreds) Resolve(context.Context, string, string) (Credentials, error) {
	return s.creds, s.err
}
func (s stubCreds) Fingerprint(context.Context, string, string) string { return s.creds.Fingerprint }

func newDialer(r *fake.RAS, c stubCreds) *Dialer {
	return &Dialer{RAS: r, Creds: c, Clock: shared.RealClock{}, PollInterval: time.Millisecond}
}

func TestDialSuccessAndErrors(t *testing.T) {
	r := fake.NewRAS("VPN Matriz")
	d := newDialer(r, stubCreds{creds: Credentials{Fingerprint: "fp"}})
	job := DialJob{Name: "Matriz", Entry: "VPN Matriz", Timeout: time.Second}
	if out := d.Dial(context.Background(), job); out.Err != nil || out.Fingerprint != "fp" || !r.IsActive("VPN Matriz") {
		t.Fatalf("sucesso: %+v", out)
	}
	r.Drop("VPN Matriz")
	r.Script("VPN Matriz", fake.DialOutcome{Code: 691, Polls: 2})
	out := d.Dial(context.Background(), job)
	if out.Err == nil || out.Err.Class != ras.ClassCredencial || out.Err.Code != 691 || out.Err.Message == "" {
		t.Fatalf("691: %+v", out.Err)
	}
	r.Script("VPN Matriz", fake.DialOutcome{Immediate: true, Code: 623})
	if out := d.Dial(context.Background(), job); out.Err.Class != ras.ClassConfiguracao {
		t.Fatalf("623 imediato: %+v", out.Err)
	}
}

func TestDialTimeoutAndCancelHangUp(t *testing.T) {
	r := fake.NewRAS("VPN Matriz")
	d := newDialer(r, stubCreds{})
	r.Script("VPN Matriz", fake.DialOutcome{Polls: -1})
	out := d.Dial(context.Background(), DialJob{Name: "Matriz", Entry: "VPN Matriz", Timeout: 20 * time.Millisecond})
	if out.Err == nil || out.Err.Class != ras.ClassTransitorio || !slices.Contains(r.Calls(), "HangUp VPN Matriz") {
		t.Fatalf("timeout deve desligar: %+v %v", out.Err, r.Calls())
	}

	r2 := fake.NewRAS("VPN Matriz")
	r2.Script("VPN Matriz", fake.DialOutcome{Polls: -1})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	out = newDialer(r2, stubCreds{}).Dial(ctx, DialJob{Name: "Matriz", Entry: "VPN Matriz", Timeout: time.Minute})
	if !out.Cancelled || !slices.Contains(r2.Calls(), "HangUp VPN Matriz") {
		t.Fatalf("cancelamento deve desligar: %+v %v", out, r2.Calls())
	}
}

func TestDialHangupFirst(t *testing.T) {
	r := fake.NewRAS("VPN Matriz")
	r.SetActive("VPN Matriz")
	d := newDialer(r, stubCreds{})
	d.Dial(context.Background(), DialJob{Name: "Matriz", Entry: "VPN Matriz", Timeout: time.Second, HangupFirst: true})
	if calls := r.Calls(); len(calls) < 2 || calls[0] != "HangUp VPN Matriz" || calls[1] != "StartDial VPN Matriz" {
		t.Fatalf("zumbi: desliga e depois disca: %v", calls)
	}
}

func TestDialCredentialResolveError(t *testing.T) {
	r := fake.NewRAS()
	d := newDialer(r, stubCreds{err: &ras.Error{Op: "RasGetEntryDialParamsW", Code: 623}})
	out := d.Dial(context.Background(), DialJob{Name: "Matriz", Entry: "Nenhuma", Timeout: time.Second})
	if out.Err == nil || out.Err.Code != 623 || out.Err.Class != ras.ClassConfiguracao {
		t.Fatalf("%+v", out.Err)
	}
}
