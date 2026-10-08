// Package domain tem o modelo do monitor de uma VPN: estados, entradas e a
// política de transição como função pura (Decide). Nada aqui faz E/S.
package domain

import (
	"fmt"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

// State é o estado de uma VPN (§4.2). O texto é o mesmo do protocolo.
type State string

const (
	Desconhecido       State = "Desconhecido"
	Conectada          State = "Conectada"
	Degradada          State = "Degradada"
	Reconectando       State = "Reconectando"
	Desconectada       State = "Desconectada"
	CredencialInvalida State = "CredencialInvalida"
	ErroConfig         State = "ErroConfig"
	Pausada            State = "Pausada"
	SemRede            State = "SemRede"
	Desativada         State = "Desativada"
)

// isDown diz se o estado conta como "fora do ar" para o aviso de queda.
// Degradada não conta: uma perda de ping isolada não é queda.
func isDown(s State) bool {
	switch s {
	case Reconectando, Desconectada, CredencialInvalida, ErroConfig, SemRede:
		return true
	}
	return false
}

// Params é o trecho da config que o domínio usa, já em durações.
type Params struct {
	Name           string
	Entry          string
	Enabled        bool
	CheckKind      config.CheckKind
	Interval       time.Duration
	Failures       int
	Grace          time.Duration
	ConnectTimeout time.Duration
	MaxBackoff     time.Duration
}

// ParamsFrom converte a config de uma VPN.
func ParamsFrom(v config.VPN) Params {
	sec := func(n int) time.Duration { return time.Duration(n) * time.Second }
	return Params{
		Name: v.Name, Entry: v.RasEntry, Enabled: v.Enabled, CheckKind: v.Check.Kind,
		Interval: sec(v.IntervalSeconds), Failures: v.FailuresBeforeReconnect,
		Grace: sec(v.GraceAfterConnectSeconds), ConnectTimeout: sec(v.ConnectTimeoutSeconds),
		MaxBackoff: sec(v.MaxBackoffSeconds),
	}
}

// Op é a operação externa em andamento (ou a pedir).
type Op int

const (
	OpNone       Op = iota
	OpProbeLink     // ras.Status + presença de rede física
	OpProbeReach    // ping/tcp
	OpDial          // discar
	OpHangupDial    // desligar e discar (túnel zumbi, reconexão manual)
)

// DialError descreve a falha de uma discagem.
type DialError struct {
	Class   ras.Class
	Code    uint32
	Message string
}

// Status é o estado completo de uma VPN, do qual o supervisor é o dono.
type Status struct {
	State State
	Since time.Time
	// Op em andamento (OpNone se ocioso). OpHangupDial vira OpDial.
	Op Op
	// Failures é o contador de falhas de alcance consecutivas.
	Failures int
	// Attempt conta discagens falhas seguidas (expoente do backoff).
	Attempt     int
	NextTick    time.Time // próximo despertar do temporizador (zero = nenhum)
	NextAttempt time.Time // próxima discagem permitida pelo backoff
	GraceUntil  time.Time
	// Pausa.
	PausedUntil      time.Time
	PausedIndefinite bool
	// Blocked guarda CredencialInvalida/ErroConfig durante uma pausa.
	Blocked State
	// BlockedFP é a impressão digital da credencial rejeitada.
	BlockedFP     string
	RejectedAt    time.Time
	LastManualTry time.Time
	LastErr       *DialError
	LastCheck     time.Time
	LastRTT       time.Duration
	DownSince     time.Time
	Reconnects    []time.Time
}

// Initial é o estado ao criar o supervisor. pause vem do state.json.
func Initial(p Params, now time.Time, pause config.Pause) Status {
	s := Status{State: Desconhecido, Since: now, NextTick: now}
	switch {
	case !p.Enabled:
		s = Status{State: Desativada, Since: now}
	case pause.Indefinite:
		s = Status{State: Pausada, Since: now, PausedIndefinite: true}
	case pause.UntilUnix > now.Unix():
		u := time.Unix(pause.UntilUnix, 0)
		s = Status{State: Pausada, Since: now, PausedUntil: u, NextTick: u}
	}
	return s
}

// PauseRecord devolve a pausa a persistir (zero se não pausada).
func (s Status) PauseRecord() config.Pause {
	if s.State != Pausada {
		return config.Pause{}
	}
	if s.PausedIndefinite {
		return config.Pause{Indefinite: true}
	}
	return config.Pause{UntilUnix: s.PausedUntil.Unix()}
}

// Reconnects24h conta reconexões bem-sucedidas nas últimas 24 h.
func (s Status) Reconnects24h(now time.Time) int {
	n := 0
	for _, t := range s.Reconnects {
		if now.Sub(t) < 24*time.Hour {
			n++
		}
	}
	return n
}

// NoticeKind classifica os avisos ao usuário (§4.8).
type NoticeKind string

const (
	NoticeDown       NoticeKind = "down"
	NoticeUp         NoticeKind = "up"
	NoticeCredential NoticeKind = "credential"
	NoticeConfig     NoticeKind = "config"
)

// Notice é um aviso para balão e log.
type Notice struct {
	Kind NoticeKind
	Text string
}

// FormatOutage escreve uma duração como "45 s", "4 min", "2 h", "2 h 5 min".
func FormatOutage(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	h, m := int(d.Hours()), int(d.Minutes())%60
	if m == 0 {
		return fmt.Sprintf("%d h", h)
	}
	return fmt.Sprintf("%d h %d min", h, m)
}

func noticeDown(name string) Notice {
	return Notice{NoticeDown, fmt.Sprintf("VPN %s caiu", name)}
}

func noticeUp(name string, outage time.Duration) Notice {
	return Notice{NoticeUp, fmt.Sprintf("VPN %s voltou (fora do ar por %s)", name, FormatOutage(outage))}
}

func noticeCredential(name string) Notice {
	return Notice{NoticeCredential, fmt.Sprintf("VPN %s: credencial rejeitada — rode vpnmon-svc credential set \"%s\"", name, name)}
}

// ConfigErrorText explica um erro de configuração, com a dica da §4.4 para
// entrada inexistente.
func ConfigErrorText(e *DialError, entry string) string {
	if e.Code == ras.ERROR_CANNOT_FIND_PHONEBOOK_ENTRY {
		return fmt.Sprintf("a entrada RAS %q não existe no catálogo de todos os usuários; recrie-a com Add-VpnConnection -AllUserConnection", entry)
	}
	return fmt.Sprintf("erro %d: %s", e.Code, e.Message)
}

func noticeConfig(name, reason string) Notice {
	return Notice{NoticeConfig, fmt.Sprintf("VPN %s: erro de configuração: %s", name, reason)}
}
