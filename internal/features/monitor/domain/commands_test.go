package domain

import (
	"strings"
	"testing"
	"time"
)

func TestCheckNow(t *testing.T) {
	cases := []struct {
		from   Status
		reply  ReplyCode
		action Op
	}{
		{st(Conectada), ReplyOK, OpProbeLink},
		{st(CredencialInvalida), ReplyOK, OpProbeLink},
		{st(Conectada, withOp(OpProbeReach)), ReplyOK, OpNone},
		{st(Pausada), ReplyPaused, OpNone},
		{st(Desativada), ReplyDisabled, OpNone},
	}
	for _, c := range cases {
		d := Decide(c.from, Input{Kind: InCheckNow}, params(), env(0))
		if d.Reply == nil || d.Reply.Code != c.reply || d.Action != c.action {
			t.Errorf("%s/%v: %+v", c.from.State, c.from.Op, d)
		}
	}
}

func TestCheckNowInBlockedStateNeverDials(t *testing.T) {
	s := st(CredencialInvalida, withOp(OpProbeLink), func(s *Status) { s.Blocked = CredencialInvalida })
	d := Decide(s, Input{Kind: InLinkResult, Network: true, LinkUp: false}, params(), env(0))
	if d.Action != OpNone || d.Next.State != CredencialInvalida {
		t.Fatalf("bloqueado não disca: %+v", d)
	}
	d = Decide(s, Input{Kind: InLinkResult, Network: true, LinkUp: true}, params(), env(0))
	if d.Next.State != Conectada || d.Next.Blocked != "" {
		t.Fatalf("conectada por fora sai do bloqueio: %+v", d.Next)
	}
}

func TestReconnect(t *testing.T) {
	d := Decide(st(Conectada), Input{Kind: InReconnect}, params(), env(0))
	if d.Action != OpHangupDial || !d.Manual || d.Next.State != Reconectando || d.Reply.Code != ReplyOK || len(d.Notices) != 0 {
		t.Fatalf("manual: %+v", d)
	}
	d = Decide(st(Conectada, withOp(OpProbeReach)), Input{Kind: InReconnect}, params(), env(0))
	if !d.Cancel || d.Action != OpHangupDial {
		t.Fatalf("cancela a verificação em curso: %+v", d)
	}
	if d := Decide(st(Reconectando, withOp(OpDial)), Input{Kind: InReconnect}, params(), env(0)); d.Reply.Code != ReplyAlreadyReconnecting || d.Action != OpNone {
		t.Fatalf("já discando: %+v", d)
	}
	paused := st(Pausada, func(s *Status) { s.PausedIndefinite = true })
	if d := Decide(paused, Input{Kind: InReconnect}, params(), env(0)); d.Reply.Code != ReplyPaused || d.Next.State != Pausada {
		t.Fatalf("pausada recusa sem mudar estado: %+v", d)
	}
	if d := Decide(st(ErroConfig, func(s *Status) { s.Blocked = ErroConfig }), Input{Kind: InReconnect}, params(), env(0)); d.Action != OpHangupDial {
		t.Fatalf("ErroConfig faz uma tentativa: %+v", d)
	}
}

func TestReconnectRejectedCredentialRateLimited(t *testing.T) {
	rejected := st(CredencialInvalida, func(s *Status) {
		s.Blocked, s.BlockedFP, s.RejectedAt = CredencialInvalida, "fp1", t0
	})
	d := Decide(rejected, Input{Kind: InReconnect, Fingerprint: "fp1"}, params(), env(time.Minute))
	if d.Reply.Code != ReplyCredentialRejected || d.Action != OpNone || !strings.Contains(d.Reply.Message, "14 min") {
		t.Fatalf("mesma credencial em 1 min: %+v", d.Reply)
	}
	d = Decide(rejected, Input{Kind: InReconnect, Fingerprint: "fp2"}, params(), env(time.Minute))
	if d.Action != OpHangupDial {
		t.Fatal("credencial nova permite tentar")
	}
	d = Decide(rejected, Input{Kind: InReconnect, Fingerprint: "fp1"}, params(), env(15*time.Minute))
	if d.Action != OpHangupDial || !d.Next.LastManualTry.Equal(t0.Add(15*time.Minute)) {
		t.Fatal("após 15 min permite uma tentativa")
	}
	// A tentativa manual falha de novo: o próximo clique espera mais 15 min.
	s := d.Next
	d = Decide(s, Input{Kind: InDialResult, DialErr: dialErr(691), Fingerprint: "fp1"}, params(), env(16*time.Minute))
	d = Decide(d.Next, Input{Kind: InReconnect, Fingerprint: "fp1"}, params(), env(20*time.Minute))
	if d.Reply.Code != ReplyCredentialRejected {
		t.Fatalf("cliques repetidos não podem discar: %+v", d.Reply)
	}
}

func TestPauseWinsOverRunningCycle(t *testing.T) {
	until := t0.Add(15 * time.Minute)
	d := Decide(st(Reconectando, withOp(OpDial)), Input{Kind: InPause, PauseUntil: until}, params(), env(0))
	if !d.Cancel || d.Next.State != Pausada || d.Next.Op != OpNone || !d.Next.NextTick.Equal(until) {
		t.Fatalf("%+v", d)
	}
	late := Decide(d.Next, Input{Kind: InDialResult}, params(), env(time.Second))
	if late.Next.State != Pausada {
		t.Fatal("resultado atrasado não tira da pausa")
	}
	d = Decide(st(Conectada), Input{Kind: InPause}, params(), env(0))
	if !d.Next.PausedIndefinite || !d.Next.NextTick.IsZero() {
		t.Fatalf("pausa indefinida: %+v", d.Next)
	}
}

func TestPauseExpiresOnTick(t *testing.T) {
	until := t0.Add(time.Hour)
	s := Decide(st(Conectada), Input{Kind: InPause, PauseUntil: until}, params(), env(0)).Next
	if d := Decide(s, Input{Kind: InTick}, params(), env(59*time.Minute)); d.Next.State != Pausada {
		t.Fatal("antes do prazo continua pausada")
	}
	d := Decide(s, Input{Kind: InTick}, params(), env(time.Hour))
	if d.Next.State != Desconhecido || d.Action != OpProbeLink {
		t.Fatalf("pausa expirada: %+v", d)
	}
}

func TestResumeReturnsToBlockedUnlessCauseChanged(t *testing.T) {
	cred := st(CredencialInvalida, func(s *Status) { s.Blocked, s.BlockedFP = CredencialInvalida, "fp1" })
	paused := Decide(cred, Input{Kind: InPause}, params(), env(0)).Next
	d := Decide(paused, Input{Kind: InResume, Fingerprint: "fp1"}, params(), env(time.Minute))
	if d.Next.State != CredencialInvalida || d.Action != OpNone {
		t.Fatalf("mesma credencial volta ao bloqueio: %+v", d.Next)
	}
	d = Decide(paused, Input{Kind: InResume, Fingerprint: "fp2"}, params(), env(time.Minute))
	if d.Next.State != Desconhecido || d.Action != OpProbeLink {
		t.Fatalf("credencial nova verifica: %+v", d.Next)
	}
	cfg := Decide(st(ErroConfig), Input{Kind: InPause}, params(), env(0)).Next
	if d := Decide(cfg, Input{Kind: InResume}, params(), env(0)); d.Next.State != ErroConfig {
		t.Fatalf("ErroConfig volta ao bloqueio: %+v", d.Next)
	}
	if d := Decide(st(Conectada), Input{Kind: InResume}, params(), env(0)); d.Reply.Code != ReplyOK || d.Next.State != Conectada {
		t.Fatal("retomar sem pausa é no-op")
	}
}

func TestCredentialChanged(t *testing.T) {
	cred := st(CredencialInvalida, func(s *Status) { s.Blocked, s.BlockedFP = CredencialInvalida, "fp1" })
	if d := Decide(cred, Input{Kind: InCredentialChanged, Fingerprint: "fp1"}, params(), env(0)); d.Action != OpNone {
		t.Fatal("mesma impressão: nada muda")
	}
	d := Decide(cred, Input{Kind: InCredentialChanged, Fingerprint: "fp2"}, params(), env(0))
	if d.Next.State != Desconhecido || d.Action != OpProbeLink {
		t.Fatalf("credencial nova sai do bloqueio e verifica: %+v", d)
	}
	paused := Decide(cred, Input{Kind: InPause}, params(), env(0)).Next
	d = Decide(paused, Input{Kind: InCredentialChanged, Fingerprint: "fp2"}, params(), env(0))
	if d.Next.State != Pausada || d.Next.Blocked != "" {
		t.Fatalf("em pausa só esquece o bloqueio: %+v", d.Next)
	}
	if d := Decide(st(Conectada), Input{Kind: InCredentialChanged, Fingerprint: "x"}, params(), env(0)); d.Action != OpNone {
		t.Fatal("fora do bloqueio é ignorado")
	}
}

func TestDisabledIgnoresEverything(t *testing.T) {
	s := st(Desativada)
	for _, k := range []InputKind{InTick, InWake, InPowerResume, InCheckNow, InReconnect, InPause, InResume} {
		d := Decide(s, Input{Kind: k}, params(), env(0))
		if d.Action != OpNone || d.Next.State != Desativada {
			t.Errorf("entrada %v mudou a VPN desativada: %+v", k, d)
		}
	}
}

func TestGraceLinkEventDoesNotGoUpForPing(t *testing.T) {
	p := params() // ping
	s := Status{State: Desconhecido, Since: t0, WasUp: false}
	s.Op, s.Attempt = OpDial, 2
	d := Decide(s, Input{Kind: InDialResult}, p, env(0))
	if d.Next.WasUp || d.Next.Attempt != 2 {
		t.Fatalf("discagem OK com ping não chama goUp: %+v", d.Next)
	}
	s = d.Next
	s.Op = OpProbeLink
	d = Decide(s, Input{Kind: InLinkResult, Network: true, LinkUp: true}, p, env(5*time.Second))
	if d.Next.WasUp || d.Next.Attempt != 2 || d.Next.State != Conectada {
		t.Fatalf("evento de enlace na carência não chama goUp: %+v", d.Next)
	}
	s = d.Next
	s.Op = OpProbeReach
	d = Decide(s, Input{Kind: InReachResult, ReachOK: true}, p, env(10*time.Second))
	if !d.Next.WasUp || d.Next.Attempt != 0 {
		t.Fatalf("alcance OK faz goUp: %+v", d.Next)
	}
}
