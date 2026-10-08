package domain

import (
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// InputKind é o tipo de entrada do ator.
type InputKind int

const (
	InTick              InputKind = iota // temporizador do próximo ciclo
	InWake                               // desconexão RAS ou mudança de rede (já agregadas)
	InPowerResume                        // retomada de energia / salto de relógio
	InLinkResult                         // resultado de OpProbeLink
	InReachResult                        // resultado de OpProbeReach
	InDialResult                         // resultado de OpDial/OpHangupDial
	InCheckNow                           // comando: verificar agora
	InReconnect                          // comando: reconectar agora
	InPause                              // comando: pausar
	InResume                             // comando: retomar
	InCredentialChanged                  // o cofre ou a credencial do Windows mudou
)

// Input é uma entrada do ator. Só os campos do tipo em questão importam.
type Input struct {
	Kind InputKind
	// InLinkResult
	LinkUp  bool
	Network bool
	// InReachResult
	ReachOK bool
	RTT     time.Duration
	// InDialResult (nil = conectou)
	DialErr *DialError
	// InPause: zero = indefinida
	PauseUntil time.Time
	// Impressão digital atual da credencial (InReconnect, InResume,
	// InCredentialChanged, InDialResult).
	Fingerprint string
}

// Env traz o que é externo à função: o instante e um aleatório em [0,1).
type Env struct {
	Now  time.Time
	Rand float64
}

// ReplyCode é a resposta a um comando.
type ReplyCode string

const (
	ReplyOK                  ReplyCode = ""
	ReplyPaused              ReplyCode = "paused"
	ReplyAlreadyReconnecting ReplyCode = "already_reconnecting"
	ReplyCredentialRejected  ReplyCode = "credential_rejected"
	ReplyDisabled            ReplyCode = "disabled"
)

// Reply é a resposta a um comando, com mensagem em português.
type Reply struct {
	Code    ReplyCode
	Message string
}

// Decision é a saída de Decide.
type Decision struct {
	Next Status
	// Action a executar agora (OpNone = nenhuma).
	Action Op
	// Cancel pede para cancelar a operação em andamento antes de Action.
	Cancel bool
	// Manual põe a discagem na frente da fila global.
	Manual bool
	// Reply só existe para comandos.
	Reply   *Reply
	Notices []Notice
}

// Jitter do backoff (§4.5).
const backoffJitter = 0.2

// resumeDelay é a espera após retomada de energia (§4.6).
const resumeDelay = 5 * time.Second

// Decide é a política inteira: dado o estado, a entrada, a config e o
// ambiente, devolve o novo estado e o que fazer.
func Decide(s Status, in Input, p Params, env Env) Decision {
	d := Decision{Next: s}
	switch in.Kind {
	case InTick:
		onTick(&d, p, env)
	case InWake:
		onWake(&d)
	case InPowerResume:
		onPowerResume(&d, env)
	case InLinkResult:
		onLink(&d, in, p, env)
	case InReachResult:
		onReach(&d, in, p, env)
	case InDialResult:
		onDial(&d, in, p, env)
	case InCheckNow:
		onCheckNow(&d)
	case InReconnect:
		onReconnect(&d, in, env)
	case InPause:
		onPause(&d, in, env)
	case InResume:
		onResume(&d, in, p, env)
	case InCredentialChanged:
		onCredentialChanged(&d, in, env)
	}
	return d
}

func (d *Decision) set(st State, now time.Time) {
	if d.Next.State != st {
		d.Next.State = st
		d.Next.Since = now
	}
}

func (d *Decision) start(op Op) {
	d.Action = op
	if op == OpHangupDial {
		op = OpDial
	}
	d.Next.Op = op
}

// goDown entra num estado "fora do ar" e avisa a queda uma vez.
func (d *Decision) goDown(st State, p Params, now time.Time) {
	prev := d.Next.State
	d.Next.Failures = 0
	if (prev == Conectada || prev == Degradada) && d.Next.DownSince.IsZero() && d.Next.WasUp {
		d.Next.DownSince = now
		d.Notices = append(d.Notices, noticeDown(p.Name))
	}
	d.set(st, now)
}

// goUp entra em Conectada e avisa a volta se houve queda.
func (d *Decision) goUp(p Params, now time.Time) {
	if !d.Next.DownSince.IsZero() {
		d.Notices = append(d.Notices, noticeUp(p.Name, now.Sub(d.Next.DownSince)))
		d.Next.DownSince = time.Time{}
	}
	d.Next.Failures = 0
	d.Next.Attempt = 0
	d.Next.NextAttempt = time.Time{}
	d.Next.WasUp = true
	d.Next.Blocked = "" // conectada: a memória de bloqueio acabou
	d.set(Conectada, now)
}

// backoff conta uma tentativa e arma a próxima discagem permitida.
func (d *Decision) backoff(p Params, env Env) {
	d.Next.Attempt++
	wait := shared.Backoff{Base: p.Interval, Max: p.MaxBackoff, Jitter: backoffJitter}.Delay(d.Next.Attempt, env.Rand)
	d.Next.NextAttempt = env.Now.Add(wait)
}

func isBlocked(st State) bool { return st == CredencialInvalida || st == ErroConfig }

// idle diz se o estado aceita ciclos automáticos.
func idle(s Status) bool {
	return s.Op == OpNone && s.State != Pausada && s.State != Desativada && !isBlocked(s.State)
}

func onTick(d *Decision, p Params, env Env) {
	s := d.Next
	switch {
	case s.State == Pausada:
		if !s.PausedIndefinite && !env.Now.Before(s.PausedUntil) {
			resume(d, "", p, env)
		}
	case idle(s):
		d.Next.NextTick = time.Time{}
		d.start(OpProbeLink)
	}
}

func onWake(d *Decision) {
	if idle(d.Next) {
		d.Next.NextTick = time.Time{}
		d.start(OpProbeLink)
	}
}

func onPowerResume(d *Decision, env Env) {
	d.Next.Attempt = 0
	d.Next.NextAttempt = time.Time{}
	if idle(d.Next) {
		d.Next.NextTick = env.Now.Add(resumeDelay)
	}
}

func onLink(d *Decision, in Input, p Params, env Env) {
	now := env.Now
	if d.Next.Op == OpDial {
		return // verificação atrasada: a discagem em curso é dona do estado
	}
	d.Next.Op = OpNone
	d.Next.LastCheck = now
	s := d.Next
	if s.State == Pausada || s.State == Desativada {
		return // resultado atrasado de um ciclo que a pausa venceu
	}
	if isBlocked(s.State) {
		// Bloqueado só verifica (checkNow): se alguém conectou, saiu do bloqueio.
		if in.LinkUp {
			d.Next.Blocked, d.Next.LastErr = "", nil
			d.goUp(p, now)
			d.Next.NextTick = now.Add(p.Interval)
		}
		return
	}
	switch {
	case !in.Network:
		d.goDown(SemRede, p, now)
		d.Next.NextTick = now.Add(p.Interval)
	case !in.LinkUp:
		if now.Before(s.NextAttempt) {
			if s.State != Reconectando {
				d.goDown(Desconectada, p, now)
			}
			d.Next.NextTick = s.NextAttempt
			return
		}
		d.goDown(Reconectando, p, now)
		d.Next.NextTick = time.Time{}
		d.start(OpDial)
	case p.CheckKind == config.CheckLink:
		d.Next.LastRTT = 0
		d.goUp(p, now)
		d.Next.NextTick = now.Add(p.Interval)
	case now.Before(s.GraceUntil):
		// Carência (só ping/tcp; link já saiu acima): não verifica alcance.
		// O "voltou", o WasUp e o reset do backoff esperam o primeiro
		// alcance OK, nunca o evento de enlace.
		d.Next.LastRTT = 0
		d.set(Conectada, now)
		d.Next.NextTick = now.Add(p.Interval)
	default:
		d.start(OpProbeReach)
	}
}

func onReach(d *Decision, in Input, p Params, env Env) {
	now := env.Now
	if d.Next.Op == OpDial {
		return // idem onLink
	}
	d.Next.Op = OpNone
	d.Next.LastCheck = now
	if !idle(d.Next) {
		return
	}
	if in.ReachOK {
		d.Next.LastRTT = in.RTT
		d.goUp(p, now)
		d.Next.NextTick = now.Add(p.Interval)
		return
	}
	d.Next.Failures++
	if d.Next.Failures >= p.Failures {
		// Túnel zumbi: enlace de pé, alvo mudo por N verificações.
		d.goDown(Reconectando, p, now)
		if now.Before(d.Next.NextAttempt) {
			// Backoff em curso: espera, e a próxima falha de alcance
			// já derruba de novo.
			d.Next.Failures = p.Failures - 1
			d.Next.NextTick = d.Next.NextAttempt
			return
		}
		d.backoff(p, env)
		d.Next.NextTick = time.Time{}
		d.start(OpHangupDial)
		return
	}
	if d.Next.State != Degradada {
		d.set(Degradada, now)
	}
	d.Next.NextTick = now.Add(p.Interval)
}

func onDial(d *Decision, in Input, p Params, env Env) {
	now := env.Now
	d.Next.Op = OpNone
	if d.Next.State == Pausada || d.Next.State == Desativada {
		return
	}
	e := in.DialErr
	if e == nil {
		d.Next.GraceUntil = now.Add(p.Grace)
		d.Next.LastErr = nil
		d.Next.Blocked = ""
		d.Next.LastRTT = 0
		d.Next.Reconnects = appendRecent(d.Next.Reconnects, now)
		if p.CheckKind == config.CheckLink {
			d.goUp(p, now)
		} else {
			// Discar não prova que o túnel passa tráfego: o "voltou" e o
			// reset do backoff esperam o primeiro alcance OK.
			d.Next.Failures = 0
			d.set(Conectada, now)
		}
		d.Next.NextTick = now.Add(p.Interval)
		return
	}
	d.Next.LastErr = e
	switch e.Class {
	case ras.ClassCredencial:
		d.goDown(CredencialInvalida, p, now)
		d.Next.Blocked = CredencialInvalida
		d.Next.BlockedFP = in.Fingerprint
		d.Next.RejectedAt = now
		d.Next.NextTick = time.Time{}
		d.Notices = append(d.Notices, noticeCredential(p.Name))
	case ras.ClassConfiguracao:
		d.goDown(ErroConfig, p, now)
		d.Next.Blocked = ErroConfig
		d.Next.NextTick = time.Time{}
		d.Notices = append(d.Notices, noticeConfig(p.Name, ConfigErrorText(e, p.Entry)))
	case ras.ClassJaDiscando:
		// Outro processo está discando: reavalia o enlace no próximo ciclo.
		d.goDown(Reconectando, p, now)
		d.Next.NextTick = now.Add(p.Interval)
	default:
		d.goDown(Reconectando, p, now)
		d.backoff(p, env)
		d.Next.NextTick = d.Next.NextAttempt
	}
}

func appendRecent(ts []time.Time, now time.Time) []time.Time {
	out := make([]time.Time, 0, len(ts)+1)
	for _, t := range ts {
		if now.Sub(t) < 24*time.Hour {
			out = append(out, t)
		}
	}
	return append(out, now)
}

// resume sai da pausa: volta ao bloqueio anterior se a causa não mudou,
// senão recomeça em Desconhecido verificando já.
func resume(d *Decision, fp string, p Params, env Env) {
	now := env.Now
	d.Next.PausedUntil, d.Next.PausedIndefinite = time.Time{}, false
	b := d.Next.Blocked
	if b == CredencialInvalida && credChanged(fp, d.Next.BlockedFP) {
		b = ""
	}
	if b != "" {
		d.set(b, now)
		d.Next.NextTick = time.Time{}
		return
	}
	d.Next.Blocked = ""
	d.set(Desconhecido, now)
	d.Next.NextTick = time.Time{}
	d.start(OpProbeLink)
}
