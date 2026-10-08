package domain

import (
	"fmt"
	"testing"
	"time"
)

// Tabela estados × entradas: para cada estado de partida (com e sem
// operação em andamento) e cada entrada possível (com variações), confere
// invariantes que nenhum caminho pode quebrar.
func TestInvariantsStatesByInputs(t *testing.T) {
	states := []State{Desconhecido, Conectada, Degradada, Reconectando, Desconectada, CredencialInvalida, ErroConfig, Pausada, SemRede, Desativada}
	ops := []Op{OpNone, OpProbeLink, OpProbeReach, OpDial}
	inputs := []Input{
		{Kind: InTick}, {Kind: InWake}, {Kind: InPowerResume},
		{Kind: InLinkResult, Network: true, LinkUp: true}, {Kind: InLinkResult, Network: true}, {Kind: InLinkResult},
		{Kind: InReachResult, ReachOK: true}, {Kind: InReachResult},
		{Kind: InDialResult}, {Kind: InDialResult, DialErr: dialErr(809)}, {Kind: InDialResult, DialErr: dialErr(691)},
		{Kind: InDialResult, DialErr: dialErr(623)}, {Kind: InDialResult, DialErr: dialErr(756)},
		{Kind: InCheckNow}, {Kind: InReconnect, Fingerprint: "fp1"}, {Kind: InReconnect, Fingerprint: "fp2"},
		{Kind: InPause}, {Kind: InPause, PauseUntil: t0.Add(time.Hour)},
		{Kind: InResume, Fingerprint: "fp1"}, {Kind: InResume, Fingerprint: "fp2"},
		{Kind: InCredentialChanged, Fingerprint: "fp1"}, {Kind: InCredentialChanged, Fingerprint: "fp2"},
	}
	p := params()
	for _, from := range states {
		for _, op := range ops {
			for _, in := range inputs {
				s := Status{State: from, Since: t0, Op: op, Attempt: 2, NextAttempt: t0.Add(time.Minute),
					BlockedFP: "fp1", RejectedAt: t0, PausedUntil: t0.Add(2 * time.Hour)}
				if isBlocked(from) {
					s.Blocked = from
				}
				name := fmt.Sprintf("%s/op%d/in%d", from, op, in.Kind)
				d := Decide(s, in, p, env(time.Second))
				dials := d.Action == OpDial || d.Action == OpHangupDial

				// Desativada nunca age.
				if from == Desativada && (d.Action != OpNone || d.Next.State != Desativada) {
					t.Errorf("%s: desativada agiu: %+v", name, d)
				}
				// Pausada só sai da pausa por resume ou pelo prazo; parada, não age.
				if d.Next.State == Pausada && d.Action != OpNone {
					t.Errorf("%s: pausada com ação %v", name, d.Action)
				}
				if from == Pausada && in.Kind != InResume && in.Kind != InTick && d.Next.State != Pausada {
					t.Errorf("%s: saiu da pausa sem resume", name)
				}
				// Bloqueado nunca disca sem pedido manual.
				if isBlocked(from) && dials && in.Kind != InReconnect {
					t.Errorf("%s: bloqueado discou sozinho", name)
				}
				// Credencial rejeitada: reconexão manual com a mesma credencial
				// dentro de 15 min nunca disca.
				if from == CredencialInvalida && in.Kind == InReconnect && in.Fingerprint == "fp1" && dials {
					t.Errorf("%s: repetiu credencial rejeitada", name)
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
