package domain

import (
	"fmt"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
)

// Tabela estados × operação × backoff × instante × entradas × impressão
// digital: confere invariantes que nenhum caminho pode quebrar.
func TestInvariantsStatesByInputs(t *testing.T) {
	type start struct {
		state   State
		blocked State // memória do bloqueio (Pausada)
	}
	var starts []start
	for _, st := range []State{Desconhecido, Conectada, Degradada, Reconectando, Desconectada, CredencialInvalida, ErroConfig, SemRede, Desativada} {
		starts = append(starts, start{st, ""})
	}
	for _, b := range []State{"", CredencialInvalida, ErroConfig} {
		starts = append(starts, start{Pausada, b})
	}
	ops := []Op{OpNone, OpProbeLink, OpProbeReach, OpDial}
	nextAttempts := []time.Time{{}, t0.Add(time.Hour)}
	// 1 s após a rejeição; >15 min após ela; >PausedUntil (t0+1h).
	nows := []time.Duration{time.Second, 16 * time.Minute, 2 * time.Hour}
	fps := []string{"", "fp1", "fp2"} // fp1 = credencial bloqueada
	kinds := []Input{
		{Kind: InTick}, {Kind: InWake}, {Kind: InPowerResume},
		{Kind: InLinkResult, Network: true, LinkUp: true}, {Kind: InLinkResult, Network: true}, {Kind: InLinkResult},
		{Kind: InReachResult, ReachOK: true}, {Kind: InReachResult},
		{Kind: InDialResult}, {Kind: InDialResult, DialErr: dialErr(809)}, {Kind: InDialResult, DialErr: dialErr(691)},
		{Kind: InDialResult, DialErr: dialErr(623)}, {Kind: InDialResult, DialErr: dialErr(756)},
		{Kind: InCheckNow}, {Kind: InReconnect},
		{Kind: InPause}, {Kind: InPause, PauseUntil: t0.Add(time.Hour)},
		{Kind: InResume}, {Kind: InCredentialChanged},
	}
	p := params()
	for _, from := range starts {
		for _, op := range ops {
			for _, na := range nextAttempts {
				for _, dt := range nows {
					for _, fp := range fps {
						for _, base := range kinds {
							in := base
							in.Fingerprint = fp
							s := Status{State: from.state, Since: t0, Op: op, Attempt: 2, NextAttempt: na,
								BlockedFP: "fp1", RejectedAt: t0, PausedUntil: t0.Add(time.Hour), Blocked: from.blocked}
							if isBlocked(from.state) {
								s.Blocked = from.state
							}
							name := fmt.Sprintf("%s(%s)/op%d/na%v/+%v/fp%q/in%d", from.state, from.blocked, op, !na.IsZero(), dt, fp, in.Kind)
							d := Decide(s, in, p, env(dt))
							dials := d.Action == OpDial || d.Action == OpHangupDial
							manual := in.Kind == InReconnect

							// Desativada nunca age nem sai do lugar.
							if from.state == Desativada && (d.Action != OpNone || d.Next.State != Desativada) {
								t.Errorf("%s: desativada agiu: %+v", name, d)
							}
							// Pausada (antes e depois) nunca age nem disca.
							if d.Next.State == Pausada && d.Action != OpNone {
								t.Errorf("%s: pausada com ação %v", name, d.Action)
							}
							if from.state == Pausada {
								if dials {
									t.Errorf("%s: pausada discou", name)
								}
								expires := in.Kind == InTick && !s.PausedIndefinite && !env(dt).Now.Before(s.PausedUntil)
								if in.Kind != InResume && !expires && d.Next.State != Pausada {
									t.Errorf("%s: saiu da pausa sem resume nem prazo", name)
								}
								// Sair da pausa com a causa do bloqueio intacta volta
								// ao bloqueio, nunca disca nem verifica.
								leaves := in.Kind == InResume || expires
								causeSame := from.blocked == ErroConfig ||
									(from.blocked == CredencialInvalida && !credChanged(fp, "fp1"))
								// O tick passa fingerprint vazia: a causa nunca "mudou".
								if expires {
									causeSame = from.blocked != ""
								}
								if leaves && causeSame && (d.Next.State != from.blocked || d.Action != OpNone) {
									t.Errorf("%s: deveria voltar ao bloqueio %s: %+v", name, from.blocked, d)
								}
							}
							// Bloqueado só disca com reconexão manual permitida.
							if isBlocked(from.state) && dials {
								allowed := manual && (from.state == ErroConfig ||
									credChanged(fp, "fp1") || env(dt).Now.Sub(t0) >= ManualRetryWindow)
								if !allowed {
									t.Errorf("%s: bloqueado discou sem permissão", name)
								}
							}
							// Credencial rejeitada, mesma impressão (ou vazia), dentro da janela.
							if from.state == CredencialInvalida && manual && !credChanged(fp, "fp1") &&
								env(dt).Now.Sub(t0) < ManualRetryWindow && dials {
								t.Errorf("%s: repetiu credencial rejeitada", name)
							}
							// credentialChanged com impressão vazia nunca desbloqueia.
							if in.Kind == InCredentialChanged && fp == "" && from.state == CredencialInvalida &&
								(d.Next.State != CredencialInvalida || d.Action != OpNone) {
								t.Errorf("%s: impressão vazia desbloqueou", name)
							}
							// SemRede nunca gasta nem mexe no backoff.
							if in.Kind == InLinkResult && !in.Network &&
								(d.Next.Attempt != s.Attempt || !d.Next.NextAttempt.Equal(s.NextAttempt)) {
								t.Errorf("%s: sem rede alterou o backoff: %+v", name, d.Next)
							}
							// Nunca discar com outra discagem em andamento.
							if op == OpDial && dials {
								t.Errorf("%s: discagem dupla", name)
							}
							// Verificação atrasada não derruba a discagem em curso.
							if op == OpDial && (in.Kind == InLinkResult || in.Kind == InReachResult) &&
								(d.Next.Op != OpDial || d.Next.State != from.state) {
								t.Errorf("%s: resultado de verificação mexeu na discagem: %+v", name, d.Next)
							}
							// Op em andamento coerente com a ação pedida.
							if d.Action == OpHangupDial && d.Next.Op != OpDial || d.Action != OpNone && d.Action != OpHangupDial && d.Next.Op != d.Action {
								t.Errorf("%s: Op %v não reflete a ação %v", name, d.Next.Op, d.Action)
							}
							// Comandos sempre respondem; entradas automáticas nunca.
							isCmd := in.Kind >= InCheckNow && in.Kind <= InResume
							if isCmd != (d.Reply != nil) {
								t.Errorf("%s: resposta %v para comando=%v", name, d.Reply, isCmd)
							}
						}
					}
				}
			}
		}
	}
}

// autoInputs são as entradas automáticas (sem comando do usuário) que um
// estado bloqueado recebe ao longo do tempo.
func autoInputs(fp string) []Input {
	return []Input{
		{Kind: InTick}, {Kind: InWake}, {Kind: InPowerResume},
		{Kind: InLinkResult, Network: true}, {Kind: InLinkResult},
		{Kind: InReachResult}, {Kind: InCredentialChanged, Fingerprint: fp},
	}
}

// assertNoAutoDial alimenta s com entradas automáticas por 2 h (cada ação
// pedida é respondida como o mundo responderia: enlace caído) e falha se
// alguma levar a discar.
func assertNoAutoDial(t *testing.T, name string, s Status, p Params, fp string, from time.Time) {
	t.Helper()
	for step := range 120 {
		now := from.Add(time.Duration(step) * time.Minute)
		for _, in := range autoInputs(fp) {
			d := Decide(s, in, p, Env{Now: now, Rand: 0.5})
			if d.Action == OpDial || d.Action == OpHangupDial {
				t.Fatalf("%s: discagem automática com a credencial rejeitada (+%d min, entrada %d): %+v", name, step, in.Kind, d.Next)
			}
			s = d.Next
			if d.Action == OpProbeLink {
				s = Decide(s, Input{Kind: InLinkResult, Network: true}, p, Env{Now: now, Rand: 0.5}).Next
			}
		}
	}
}

// CredencialInvalida → desativar → reativar (setEnabled false/true, que
// recria o supervisor por Reconfigure, ou por Restart após panic) não pode
// esquecer a rejeição: sem credencial nova, nenhuma discagem automática (§4.7).
func TestInvariantCredentialBlockSurvivesDisableEnable(t *testing.T) {
	on := params()
	off := params()
	off.Enabled = false
	blocked := Status{State: CredencialInvalida, Since: t0, Blocked: CredencialInvalida, BlockedFP: "fp1",
		RejectedAt: t0, LastErr: dialErr(691)}
	pauses := map[string]config.Pause{"sem pausa": {}, "pausa indefinida": {Indefinite: true}}
	for pname, pause := range pauses {
		for _, fp := range []string{"", "fp1"} {
			for _, via := range []string{"Reconfigure", "Restart"} {
				name := fmt.Sprintf("%s/%s/fp%q", via, pname, fp)
				var s Status
				if via == "Reconfigure" {
					s = Reconfigure(blocked, on, off, t0.Add(time.Second), pause)
				} else {
					s = Restart(blocked, off, t0.Add(time.Second), pause)
				}
				if s.State != Desativada {
					t.Fatalf("%s: desativar deveria dar Desativada: %+v", name, s)
				}
				if via == "Reconfigure" {
					s = Reconfigure(s, off, on, t0.Add(2*time.Second), config.Pause{})
				} else {
					s = Restart(s, on, t0.Add(2*time.Second), config.Pause{})
				}
				if s.State != CredencialInvalida || s.BlockedFP != "fp1" || !s.RejectedAt.Equal(t0) {
					t.Fatalf("%s: reativar deveria voltar ao bloqueio: %+v", name, s)
				}
				assertNoAutoDial(t, name, s, on, fp, t0.Add(2*time.Second))
				// Reconexão manual logo após reativar respeita a janela.
				d := Decide(s, Input{Kind: InReconnect, Fingerprint: fp}, on, Env{Now: t0.Add(time.Minute)})
				if d.Action != OpNone || d.Reply == nil || d.Reply.Code != ReplyCredentialRejected {
					t.Fatalf("%s: reconnect dentro da janela: %+v", name, d)
				}
			}
		}
	}
	// Credencial nova após reativar: o aviso de mudança desbloqueia.
	s := Reconfigure(Reconfigure(blocked, on, off, t0, config.Pause{}), off, on, t0, config.Pause{})
	d := Decide(s, Input{Kind: InCredentialChanged, Fingerprint: "fp2"}, on, Env{Now: t0})
	if d.Next.State != Desconhecido || d.Action != OpProbeLink {
		t.Fatalf("credencial nova deveria desbloquear: %+v", d)
	}
}

// Memória vinda do state.json (sem impressão digital): bloqueia até a janela
// de 15 min; a impressão vista depois da partida passa a ser a rejeitada,
// e só uma mudança real (outra impressão) desbloqueia antes.
func TestInvariantStartupMemoryWithoutFingerprint(t *testing.T) {
	p := params()
	until := t0.Add(ManualRetryWindow)
	mem := CredMemory{FPUnknown: true, RejectedAt: t0, Until: until}
	s := WithCredMemory(Initial(p, t0.Add(time.Minute), config.Pause{}), mem, t0.Add(time.Minute))
	if s.State != CredencialInvalida || !s.NextTick.Equal(until) || s.Op != OpNone {
		t.Fatalf("partida dentro da janela deveria bloquear com tique no fim: %+v", s)
	}
	// Qualquer impressão (não vazia) na partida não desbloqueia: é aprendida.
	for _, fp := range []string{"", "fpA", "fpB"} {
		d := Decide(s, Input{Kind: InCredentialChanged, Fingerprint: fp}, p, Env{Now: t0.Add(2 * time.Minute)})
		if d.Next.State != CredencialInvalida || d.Action != OpNone {
			t.Fatalf("fp %q: impressão desconhecida desbloqueou: %+v", fp, d)
		}
		if fp != "" && (d.Next.BlockedFP != fp || d.Next.BlockedFPUnknown) {
			t.Fatalf("fp %q: deveria aprender a impressão: %+v", fp, d.Next)
		}
		// Reconnect com impressão desconhecida respeita a janela.
		r := Decide(s, Input{Kind: InReconnect, Fingerprint: fp}, p, Env{Now: t0.Add(2 * time.Minute)})
		if r.Action != OpNone || r.Reply == nil || r.Reply.Code != ReplyCredentialRejected {
			t.Fatalf("fp %q: reconnect na janela: %+v", fp, r)
		}
	}
	// Sem discagem automática até a janela vencer.
	cur := s
	for m := 1; m < 14; m++ {
		for _, in := range autoInputs("fpA") {
			d := Decide(cur, in, p, Env{Now: t0.Add(time.Duration(m) * time.Minute)})
			if d.Action == OpDial || d.Action == OpHangupDial || d.Next.State != CredencialInvalida {
				t.Fatalf("+%d min: saiu do bloqueio antes da janela: %+v", m, d)
			}
			cur = d.Next
		}
	}
	// Mudança real depois de aprender: desbloqueia.
	d := Decide(cur, Input{Kind: InCredentialChanged, Fingerprint: "fpB"}, p, Env{Now: t0.Add(14 * time.Minute)})
	if d.Next.State != Desconhecido || d.Action != OpProbeLink {
		t.Fatalf("mudança real após a partida deveria desbloquear: %+v", d)
	}
	// Janela vencida: o tique sai do bloqueio e verifica.
	d = Decide(cur, Input{Kind: InTick}, p, Env{Now: until})
	if d.Next.State != Desconhecido || d.Action != OpProbeLink || d.Next.Blocked != "" {
		t.Fatalf("fim da janela deveria liberar: %+v", d)
	}
	// Pausada na partida: retomar dentro da janela volta ao bloqueio (tique
	// no fim); retomar depois dela libera.
	ps := WithCredMemory(Initial(p, t0, config.Pause{Indefinite: true}), mem, t0)
	if ps.State != Pausada || ps.Blocked != CredencialInvalida {
		t.Fatalf("pausada: %+v", ps)
	}
	d = Decide(ps, Input{Kind: InResume, Fingerprint: "fpA"}, p, Env{Now: t0.Add(time.Minute)})
	if d.Next.State != CredencialInvalida || d.Action != OpNone || !d.Next.NextTick.Equal(until) {
		t.Fatalf("resume na janela: %+v", d)
	}
	d = Decide(ps, Input{Kind: InResume, Fingerprint: "fpA"}, p, Env{Now: until})
	if d.Next.State != Desconhecido || d.Action != OpProbeLink {
		t.Fatalf("resume após a janela: %+v", d)
	}
	// Nova rejeição real: a memória volta a ser completa e sem prazo.
	d = Decide(Status{State: Reconectando, Op: OpDial, Blocked: CredencialInvalida, BlockedFPUnknown: true, BlockedUntil: until},
		Input{Kind: InDialResult, DialErr: dialErr(691), Fingerprint: "fpA"}, p, Env{Now: t0.Add(time.Minute)})
	if d.Next.BlockedFP != "fpA" || d.Next.BlockedFPUnknown || !d.Next.BlockedUntil.IsZero() || !d.Next.NextTick.IsZero() {
		t.Fatalf("rejeição nova: %+v", d.Next)
	}
}
