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

// String dá o nome legível da operação (mensagens de log e de panic).
func (o Op) String() string {
	switch o {
	case OpNone:
		return "nenhuma"
	case OpProbeLink:
		return "sonda de enlace"
	case OpProbeReach:
		return "verificação de alcance"
	case OpDial:
		return "discagem"
	case OpHangupDial:
		return "desligar e discar"
	}
	return fmt.Sprintf("Op(%d)", int(o))
}

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
	BlockedFP string
	// BlockedFPUnknown: a memória veio do state.json, que não guarda a
	// impressão; a primeira impressão vista depois passa a ser a rejeitada
	// (nunca conta como mudança).
	BlockedFPUnknown bool
	// BlockedUntil, se não zero, é o fim de um bloqueio por credencial que
	// sai sozinho (memória da partida: fim da janela de 15 min).
	BlockedUntil  time.Time
	RejectedAt    time.Time
	LastManualTry time.Time
	LastErr       *DialError
	LastCheck     time.Time
	LastRTT       time.Duration
	DownSince     time.Time
	// WasUp marca que a VPN já esteve Conectada: sem isso não há aviso de queda.
	WasUp      bool
	Reconnects []time.Time
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

// Restart é o estado ao recriar o supervisor de uma VPN que já rodava (após
// panic), a partir do último estado publicado. Diferente de Initial, guarda a
// memória que protege a conta no AD e o backoff (§4.7, §4.9): bloqueio,
// impressão digital rejeitada, tentativas manuais, último erro, histórico de
// reconexões, Attempt/NextAttempt, WasUp e DownSince. A operação em curso se
// perdeu com o supervisor antigo (Op zera). pause vem do state.json.
//
// Desativada guarda só a memória de credencial rejeitada (CredMemoryOf): ao
// reativar, volta a CredencialInvalida e só sai com credencial nova, com
// reconexão manual respeitando a janela ou no fim de BlockedUntil.
func Restart(last Status, p Params, now time.Time, pause config.Pause) Status {
	if !p.Enabled {
		s := Initial(p, now, pause)
		if m, ok := CredMemoryOf(last); ok {
			s = WithCredMemory(s, m, now)
		}
		return s
	}
	s := last
	s.Op = OpNone
	s.PausedUntil, s.PausedIndefinite = time.Time{}, false
	set := func(st State) {
		if s.State != st {
			s.State, s.Since = st, now
		}
	}
	switch {
	case pause.Indefinite || pause.UntilUnix > now.Unix():
		if isBlocked(s.State) {
			s.Blocked = s.State
		}
		set(Pausada)
		s.NextTick = time.Time{}
		if pause.Indefinite {
			s.PausedIndefinite = true
		} else {
			s.PausedUntil = time.Unix(pause.UntilUnix, 0)
			s.NextTick = s.PausedUntil
		}
	case s.Blocked != "" || isBlocked(s.State):
		// Bloqueado (ou reconexão manual de um bloqueado em curso): volta ao
		// bloqueio, sem ciclo automático; só comando ou credencial nova saem dele.
		if s.Blocked == "" {
			s.Blocked = s.State
		}
		set(s.Blocked)
		s.NextTick = time.Time{}
		if s.Blocked == CredencialInvalida {
			s.NextTick = s.BlockedUntil // fim da janela da partida (zero = nenhum)
		}
	case s.State == Pausada || s.State == Desativada:
		set(Desconhecido)
		s.NextTick = now
	default:
		// Verifica já; a sonda de enlace respeita NextAttempt antes de discar.
		s.NextTick = now
	}
	return s
}

// Reconfigure é o estado ao recriar o supervisor porque a config da VPN mudou
// (old → p). Parte de Restart (guarda a memória de bloqueio, as reconexões,
// WasUp/DownSince e o backoff), mas o que foi medido com a config antiga deixa
// de valer: Failures e LastRTT zeram e Degradada volta a Desconhecido. Com
// intervalo ou teto do backoff novos, o prazo pendente não passa de now+teto.
//
// Com a entrada RAS trocada, o enlace, o alcance e o backoff eram da entrada
// antiga e zeram (estado volta a Desconhecido, e um ErroConfig dela sai do
// bloqueio), mas a memória de credencial rejeitada fica: o cofre é por nome
// de VPN, e a mesma credencial seria discada de novo.
func Reconfigure(last Status, old, p Params, now time.Time, pause config.Pause) Status {
	s := Restart(last, p, now, pause)
	if !p.Enabled {
		return s
	}
	set := func(st State) {
		if s.State != st {
			s.State, s.Since = st, now
		}
	}
	s.Failures, s.LastRTT = 0, 0
	if s.State == Degradada {
		set(Desconhecido)
	}
	if old.Interval != p.Interval || old.MaxBackoff != p.MaxBackoff {
		if limit := now.Add(p.MaxBackoff); !s.NextAttempt.IsZero() && s.NextAttempt.After(limit) {
			s.NextAttempt = limit
		}
	}
	if old.Entry != p.Entry {
		s.Attempt, s.NextAttempt, s.GraceUntil, s.LastCheck = 0, time.Time{}, time.Time{}, time.Time{}
		if s.Blocked == ErroConfig {
			s.Blocked = ""
		}
		switch s.State {
		case ErroConfig, Conectada, Reconectando, Desconectada, SemRede:
			set(Desconhecido)
			s.NextTick = now
		}
		if s.State != CredencialInvalida && s.Blocked != CredencialInvalida {
			s.LastErr = nil
		}
	}
	return s
}

// CredMemory é a memória de uma credencial rejeitada (§4.7): o que impede
// discar de novo com ela e bloquear a conta no AD. Sobrevive à recriação do
// supervisor (desativar/reativar, remover/adicionar, reinício do serviço).
type CredMemory struct {
	FP            string
	FPUnknown     bool // sem impressão (memória vinda do state.json)
	RejectedAt    time.Time
	LastManualTry time.Time
	// Until: o bloqueio sai sozinho nesse instante (zero = só com credencial
	// nova ou reconexão manual).
	Until   time.Time
	LastErr *DialError
}

// Ref é o instante da última tentativa com a credencial rejeitada: a mais
// recente entre a rejeição e a tentativa manual.
func (m CredMemory) Ref() time.Time {
	if m.LastManualTry.After(m.RejectedAt) {
		return m.LastManualTry
	}
	return m.RejectedAt
}

// CredMemoryOf extrai a memória de credencial rejeitada de s; ok=false se
// s não está (nem guarda) um bloqueio por credencial.
func CredMemoryOf(s Status) (m CredMemory, ok bool) {
	if s.State != CredencialInvalida && s.Blocked != CredencialInvalida {
		return CredMemory{}, false
	}
	return CredMemory{FP: s.BlockedFP, FPUnknown: s.BlockedFPUnknown, RejectedAt: s.RejectedAt,
		LastManualTry: s.LastManualTry, Until: s.BlockedUntil, LastErr: s.LastErr}, true
}

// WithCredMemory aplica a memória a um estado recém-criado (Initial). Ativa:
// CredencialInvalida, sem ciclo automático (com Until, um tique no fim).
// Pausada ou Desativada: a memória fica guardada em Blocked e volta ao
// retomar/reativar.
func WithCredMemory(s Status, m CredMemory, now time.Time) Status {
	s.Blocked = CredencialInvalida
	s.BlockedFP, s.BlockedFPUnknown = m.FP, m.FPUnknown
	s.RejectedAt, s.LastManualTry, s.BlockedUntil = m.RejectedAt, m.LastManualTry, m.Until
	switch s.State {
	case Desativada:
		s.LastErr = nil // a bandeja não mostra erro de uma VPN desativada
	case Pausada:
		s.LastErr = m.LastErr
	default:
		s.LastErr = m.LastErr
		s.Op = OpNone
		if s.State != CredencialInvalida {
			s.State, s.Since = CredencialInvalida, now
		}
		s.NextTick = m.Until
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
