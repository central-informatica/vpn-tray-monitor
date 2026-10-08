package domain

import (
	"fmt"
	"math"
	"time"
)

// ManualRetryWindow é o intervalo mínimo entre tentativas manuais com a
// mesma credencial já rejeitada (§4.7): impede bloquear a conta no AD.
const ManualRetryWindow = 15 * time.Minute

var ok = &Reply{Code: ReplyOK}

func onCheckNow(d *Decision) {
	s := d.Next
	switch {
	case s.State == Desativada:
		d.Reply = &Reply{ReplyDisabled, "VPN desativada"}
	case s.State == Pausada:
		d.Reply = &Reply{ReplyPaused, "VPN pausada"}
	case s.Op != OpNone:
		d.Reply = ok // a verificação/discagem em curso já responde
	default:
		// Inclui os estados bloqueados: verifica o enlace, sem discar.
		d.Next.NextTick = time.Time{}
		d.start(OpProbeLink)
		d.Reply = ok
	}
}

func onReconnect(d *Decision, in Input, p Params, env Env) {
	s, now := d.Next, env.Now
	switch {
	case s.State == Desativada:
		d.Reply = &Reply{ReplyDisabled, "VPN desativada"}
		return
	case s.State == Pausada:
		d.Reply = &Reply{ReplyPaused, "VPN pausada; retome antes de reconectar"}
		return
	case s.Op == OpDial:
		d.Reply = &Reply{ReplyAlreadyReconnecting, "já reconectando"}
		return
	case s.State == CredencialInvalida && in.Fingerprint == s.BlockedFP:
		ref := s.RejectedAt
		if s.LastManualTry.After(ref) {
			ref = s.LastManualTry
		}
		if wait := ref.Add(ManualRetryWindow).Sub(now); wait > 0 {
			mins := int(math.Ceil(wait.Minutes()))
			d.Reply = &Reply{ReplyCredentialRejected,
				fmt.Sprintf("credencial já rejeitada; tente novamente em %d min ou atualize a credencial", mins)}
			return
		}
	}
	if s.State == CredencialInvalida {
		d.Next.LastManualTry = now
	}
	d.Cancel = s.Op != OpNone
	d.Next.Blocked = ""
	d.Next.Failures = 0
	d.Next.NextTick = time.Time{}
	d.set(Reconectando, now)
	d.start(OpHangupDial)
	d.Manual = true
	d.Reply = ok
}

func onPause(d *Decision, in Input, env Env) {
	if d.Next.State == Desativada {
		d.Reply = &Reply{ReplyDisabled, "VPN desativada"}
		return
	}
	d.Cancel = d.Next.Op != OpNone
	d.Next.Op = OpNone
	if isBlocked(d.Next.State) {
		d.Next.Blocked = d.Next.State
	}
	d.Next.DownSince = time.Time{}
	d.Next.PausedUntil = in.PauseUntil
	d.Next.PausedIndefinite = in.PauseUntil.IsZero()
	d.Next.NextTick = in.PauseUntil
	d.set(Pausada, env.Now)
	d.Reply = ok
}

func onResume(d *Decision, in Input, p Params, env Env) {
	d.Reply = ok
	if d.Next.State != Pausada {
		return
	}
	resume(d, in.Fingerprint, p, env)
}

func onCredentialChanged(d *Decision, in Input, p Params, env Env) {
	s := d.Next
	if s.Blocked != CredencialInvalida && s.State != CredencialInvalida {
		return
	}
	if in.Fingerprint == s.BlockedFP {
		return
	}
	if s.State == Pausada {
		d.Next.Blocked = ""
		return
	}
	d.Next.Blocked = ""
	d.Next.LastErr = nil
	d.set(Desconhecido, env.Now)
	d.Next.NextTick = time.Time{}
	d.start(OpProbeLink)
}
