package domain

import (
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

func env(d time.Duration) Env { return Env{Now: t0.Add(d), Rand: 0.5} }

func st(state State, mut ...func(*Status)) Status {
	s := Status{State: state, Since: t0}
	for _, m := range mut {
		m(&s)
	}
	return s
}

func withOp(op Op) func(*Status) { return func(s *Status) { s.Op = op } }

func noticeKinds(d Decision) []NoticeKind {
	var k []NoticeKind
	for _, n := range d.Notices {
		k = append(k, n.Kind)
	}
	return k
}

func TestTickStartsLinkProbe(t *testing.T) {
	for _, state := range []State{Desconhecido, Conectada, Degradada, Reconectando, Desconectada, SemRede} {
		d := Decide(st(state), Input{Kind: InTick}, params(), env(0))
		if d.Action != OpProbeLink || d.Next.Op != OpProbeLink {
			t.Errorf("%s: ação %v", state, d.Action)
		}
	}
	for _, state := range []State{CredencialInvalida, ErroConfig, Desativada} {
		if d := Decide(st(state), Input{Kind: InTick}, params(), env(0)); d.Action != OpNone {
			t.Errorf("%s não deve verificar sozinho", state)
		}
	}
	if d := Decide(st(Conectada, withOp(OpDial)), Input{Kind: InTick}, params(), env(0)); d.Action != OpNone {
		t.Error("com operação em andamento o tique é ignorado")
	}
}

func TestLinkResults(t *testing.T) {
	p := params()
	cases := []struct {
		name   string
		from   Status
		in     Input
		state  State
		action Op
		notes  int
		text   string
	}{
		{"sem rede", st(Conectada, withOp(OpProbeLink)), Input{Kind: InLinkResult, Network: false}, SemRede, OpNone, 1, "VPN Matriz caiu"},
		{"enlace caído disca", st(Conectada, withOp(OpProbeLink)), Input{Kind: InLinkResult, Network: true}, Reconectando, OpDial, 1, "VPN Matriz caiu"},
		{"enlace caído na partida não avisa", st(Desconhecido, withOp(OpProbeLink)), Input{Kind: InLinkResult, Network: true}, Reconectando, OpDial, 0, ""},
		{"enlace de pé verifica alcance", st(Desconhecido, withOp(OpProbeLink)), Input{Kind: InLinkResult, Network: true, LinkUp: true}, Desconhecido, OpProbeReach, 0, ""},
	}
	for _, c := range cases {
		d := Decide(c.from, c.in, p, env(time.Second))
		if d.Next.State != c.state || d.Action != c.action || len(d.Notices) != c.notes {
			t.Errorf("%s: estado %s ação %v avisos %v", c.name, d.Next.State, d.Action, noticeKinds(d))
		}
		if c.text != "" && d.Notices[0].Text != c.text {
			t.Errorf("%s: texto %q, quer %q", c.name, d.Notices[0].Text, c.text)
		}
	}
}

func TestLinkKindAndGraceSkipReach(t *testing.T) {
	p := params()
	p.CheckKind = config.CheckLink
	d := Decide(st(Desconhecido, withOp(OpProbeLink)), Input{Kind: InLinkResult, Network: true, LinkUp: true}, p, env(0))
	if d.Next.State != Conectada || d.Action != OpNone || !d.Next.NextTick.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("link: %+v", d)
	}
	p = params()
	grace := st(Conectada, withOp(OpProbeLink), func(s *Status) { s.GraceUntil = t0.Add(15 * time.Second) })
	if d := Decide(grace, Input{Kind: InLinkResult, Network: true, LinkUp: true}, p, env(10*time.Second)); d.Action != OpNone {
		t.Fatal("durante a carência não verifica alcance")
	}
	if d := Decide(grace, Input{Kind: InLinkResult, Network: true, LinkUp: true}, p, env(15*time.Second)); d.Action != OpProbeReach {
		t.Fatal("após a carência verifica alcance")
	}
}

func TestBackoffGateKeepsWaiting(t *testing.T) {
	waiting := st(Reconectando, withOp(OpProbeLink), func(s *Status) { s.NextAttempt = t0.Add(time.Minute); s.Attempt = 2 })
	d := Decide(waiting, Input{Kind: InLinkResult, Network: true}, params(), env(10*time.Second))
	if d.Action != OpNone || d.Next.State != Reconectando || !d.Next.NextTick.Equal(t0.Add(time.Minute)) {
		t.Fatalf("despertar antes do backoff não disca: %+v", d)
	}
	fresh := st(Conectada, withOp(OpProbeLink), func(s *Status) { s.NextAttempt = t0.Add(time.Minute) })
	if d := Decide(fresh, Input{Kind: InLinkResult, Network: true}, params(), env(0)); d.Next.State != Desconectada {
		t.Fatalf("queda dentro do backoff = Desconectada, veio %s", d.Next.State)
	}
}

func TestNoNetworkDoesNotSpendBackoff(t *testing.T) {
	s := st(Reconectando, withOp(OpProbeLink), func(s *Status) { s.Attempt = 3 })
	d := Decide(s, Input{Kind: InLinkResult, Network: false}, params(), env(0))
	if d.Next.State != SemRede || d.Next.Attempt != 3 || d.Action != OpNone {
		t.Fatalf("%+v", d.Next)
	}
}

func TestReachFailuresThenZombieHangup(t *testing.T) {
	p := params()
	s := st(Conectada, withOp(OpProbeReach))
	for i := 1; i < p.Failures; i++ {
		d := Decide(s, Input{Kind: InReachResult}, p, env(0))
		if d.Next.State != Degradada || d.Next.Failures != i || d.Action != OpNone || len(d.Notices) != 0 {
			t.Fatalf("falha %d: %+v", i, d)
		}
		s = d.Next
		s.Op = OpProbeReach
	}
	d := Decide(s, Input{Kind: InReachResult}, p, env(0))
	if d.Next.State != Reconectando || d.Action != OpHangupDial || d.Next.Op != OpDial || d.Next.Failures != 0 {
		t.Fatalf("zumbi: %+v", d)
	}
	if k := noticeKinds(d); len(k) != 1 || k[0] != NoticeDown || d.Notices[0].Text != "VPN Matriz caiu" {
		t.Fatalf("avisos %v", d.Notices)
	}
}

func TestReachOKResetsAndReportsLatency(t *testing.T) {
	s := st(Degradada, withOp(OpProbeReach), func(s *Status) { s.Failures = 2 })
	d := Decide(s, Input{Kind: InReachResult, ReachOK: true, RTT: 12 * time.Millisecond}, params(), env(0))
	if d.Next.State != Conectada || d.Next.Failures != 0 || d.Next.LastRTT != 12*time.Millisecond || len(d.Notices) != 0 {
		t.Fatalf("%+v", d)
	}
}

func dialErr(code uint32) *DialError {
	return &DialError{Class: ras.Classify(code), Code: code, Message: "msg"}
}

func TestDialResults(t *testing.T) {
	p := params()
	dialing := st(Reconectando, withOp(OpDial), func(s *Status) { s.DownSince = t0 })

	d := Decide(dialing, Input{Kind: InDialResult}, p, env(4*time.Minute))
	if d.Next.State != Conectada || !d.Next.GraceUntil.Equal(t0.Add(4*time.Minute+15*time.Second)) || d.Next.Reconnects24h(t0.Add(4*time.Minute)) != 1 {
		t.Fatalf("sucesso: %+v", d.Next)
	}
	if len(d.Notices) != 1 || d.Notices[0].Text != "VPN Matriz voltou (fora do ar por 4 min)" {
		t.Fatalf("aviso de volta: %+v", d.Notices)
	}

	d = Decide(dialing, Input{Kind: InDialResult, DialErr: dialErr(691), Fingerprint: "fp1"}, p, env(0))
	if d.Next.State != CredencialInvalida || d.Next.BlockedFP != "fp1" || d.Next.NextTick != (time.Time{}) {
		t.Fatalf("691: %+v", d.Next)
	}
	if k := noticeKinds(d); len(k) != 1 || k[0] != NoticeCredential || d.Notices[0].Text != "VPN Matriz: credencial rejeitada — rode vpnmon-svc credential set \"Matriz\"" {
		t.Fatalf("aviso 691: %+v", d.Notices)
	}

	d = Decide(dialing, Input{Kind: InDialResult, DialErr: dialErr(623)}, p, env(0))
	if d.Next.State != ErroConfig || len(d.Notices) != 1 || d.Notices[0].Kind != NoticeConfig || d.Notices[0].Text != "VPN Matriz: erro de configuração: a entrada RAS \"VPN Matriz\" não existe no catálogo de todos os usuários; recrie-a com Add-VpnConnection -AllUserConnection" {
		t.Fatalf("623: %+v", d)
	}

	d = Decide(dialing, Input{Kind: InDialResult, DialErr: dialErr(756)}, p, env(0))
	if d.Next.State != Reconectando || d.Next.Attempt != 0 || !d.Next.NextTick.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("756: %+v", d.Next)
	}
}

func TestTransientBackoffGrows(t *testing.T) {
	p := params()
	s := st(Reconectando, withOp(OpDial))
	var waits []time.Duration
	for i := 0; i < 6; i++ {
		d := Decide(s, Input{Kind: InDialResult, DialErr: dialErr(809)}, p, env(0))
		waits = append(waits, d.Next.NextAttempt.Sub(t0))
		if d.Next.State != Reconectando || !d.Next.NextTick.Equal(d.Next.NextAttempt) {
			t.Fatalf("tentativa %d: %+v", i, d.Next)
		}
		s = d.Next
		s.Op = OpDial
	}
	want := []time.Duration{30, 60, 120, 240, 300, 300}
	for i := range want {
		if waits[i] != want[i]*time.Second {
			t.Fatalf("esperas %v", waits)
		}
	}
}

func TestPowerResumeResetsBackoff(t *testing.T) {
	s := st(Reconectando, func(s *Status) { s.Attempt = 4; s.NextAttempt = t0.Add(5 * time.Minute) })
	d := Decide(s, Input{Kind: InPowerResume}, params(), env(0))
	if d.Next.Attempt != 0 || !d.Next.NextAttempt.IsZero() || !d.Next.NextTick.Equal(t0.Add(5*time.Second)) {
		t.Fatalf("%+v", d.Next)
	}
}

func TestWakeAggregatesWhileBusy(t *testing.T) {
	if d := Decide(st(Conectada), Input{Kind: InWake}, params(), env(0)); d.Action != OpProbeLink {
		t.Fatal("despertar ocioso verifica")
	}
	if d := Decide(st(Conectada, withOp(OpProbeReach)), Input{Kind: InWake}, params(), env(0)); d.Action != OpNone {
		t.Fatal("despertar durante operação se agrega a ela")
	}
}

func TestOneDownNoticePerOutage(t *testing.T) {
	p := params()
	s := st(Conectada, withOp(OpProbeLink))
	d := Decide(s, Input{Kind: InLinkResult, Network: true}, p, env(0))
	total := len(d.Notices)
	if total != 1 || d.Notices[0].Text != "VPN Matriz caiu" {
		t.Fatalf("primeiro aviso: %+v", d.Notices)
	}
	s = d.Next
	for i := 0; i < 3; i++ { // falhas seguidas: sem novo "caiu"
		d = Decide(s, Input{Kind: InDialResult, DialErr: dialErr(809)}, p, env(0))
		s = d.Next
		s.Op = OpProbeLink
		d = Decide(s, Input{Kind: InLinkResult, Network: true}, p, env(time.Hour))
		total += len(d.Notices)
		s = d.Next
	}
	if total != 1 {
		t.Fatalf("avisos de queda = %d, quer 1", total)
	}
}
