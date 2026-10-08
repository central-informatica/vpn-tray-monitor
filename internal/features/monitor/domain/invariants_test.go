package domain

import (
	"fmt"
	"testing"
	"time"
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
