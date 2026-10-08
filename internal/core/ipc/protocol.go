// Package ipc é o canal bandeja/CLI ↔ serviço: named pipe \\.\pipe\vpnmon,
// uma mensagem JSON por linha (§6).
package ipc

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
)

// Constantes do protocolo (§6.1, §6.2).
const (
	// EnvelopeVersion é o "v" fixo do envelope; só muda se o formato da linha mudar.
	EnvelopeVersion = 1
	// ProtocolVersion é a versão negociada no Hello.Protocol; um servidor que
	// receba outra versão responde error{incompatible}.
	ProtocolVersion = 1
	// MaxMessage é o máximo de bytes de conteúdo de uma linha (sem o '\n').
	MaxMessage = 64 * 1024
	MaxConns   = 32
	PipeName   = `\\.\pipe\vpnmon`
	// PipeSDDL: Rede negada; SYSTEM e Administradores total; Usuários
	// Interativos leitura e escrita. P = sem herança. Para IU a máscara é
	// explícita (0x0012019b = FILE_GENERIC_READ | FILE_WRITE_DATA |
	// FILE_WRITE_EA | FILE_WRITE_ATTRIBUTES): "GW" incluiria
	// FILE_APPEND_DATA, que em pipes é FILE_CREATE_PIPE_INSTANCE e deixaria
	// um usuário abrir uma instância falsa do pipe.
	PipeSDDL = "D:P(D;;GA;;;NU)(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x0012019b;;;IU)"
	// PipeIUMask é a máscara de IU acima; FileCreatePipeInstance não pode estar nela.
	PipeIUMask             = 0x0012019b
	FileCreatePipeInstance = 0x0004
)

// Tipos de mensagem.
const (
	TypeHello          = "hello"
	TypeOK             = "ok"
	TypeError          = "error"
	TypeStatus         = "status"
	TypeCheckNow       = "checkNow"
	TypeReconnect      = "reconnect"
	TypePause          = "pause"
	TypeResume         = "resume"
	TypeSetEnabled     = "setEnabled"
	TypeAddVPN         = "addVpn"
	TypeUpdateVPN      = "updateVpn"
	TypeRemoveVPN      = "removeVpn"
	TypeListRasEntries = "listRasEntries"
	TypeGetConfig      = "getConfig"
	TypeSetGlobal      = "setGlobal"
	TypeLogTail        = "logTail"
	TypeSubscribe      = "subscribe"
	// Eventos (após subscribe).
	TypeSnapshot        = "snapshot"
	TypeVPNState        = "vpnState"
	TypeNotice          = "notice"
	TypeConfigStatus    = "configStatus"
	TypeServiceStopping = "serviceStopping"
)

// ErrNotService: o pipe existe, mas o processo que o serve não é o serviço
// VPNMonitor (PID diferente do informado pelo SCM). A conexão é recusada.
var ErrNotService = errors.New("o pipe não é servido pelo serviço VPN Monitor")

// Message é o envelope de toda linha.
type Message struct {
	V       int             `json:"v"`
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Códigos de erro.
const (
	CodeBadRequest          = "bad_request"
	CodeUnknownType         = "unknown_type"
	CodeIncompatible        = "incompatible"
	CodeNotFound            = "not_found"
	CodeInvalidConfig       = "invalid_config"
	CodePaused              = "paused"
	CodeAlreadyReconnecting = "already_reconnecting"
	CodeCredentialRejected  = "credential_rejected"
	CodeDisabled            = "disabled"
	CodeBusy                = "busy"
	CodeInternal            = "internal"
)

// Error é o payload de uma resposta "error". Fields aponta erros de
// validação por campo (para a janela de configurações).
type Error struct {
	Code    string              `json:"code"`
	Message string              `json:"message"`
	Fields  []config.FieldError `json:"fields,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Valores de VPNView.State (os mesmos de domain.State; um teste do serviço
// os amarra) e de ErrorInfo.Class: a bandeja os usa sem importar o monitor.
const (
	StateDesconhecido       = "Desconhecido"
	StateConectada          = "Conectada"
	StateDegradada          = "Degradada"
	StateReconectando       = "Reconectando"
	StateDesconectada       = "Desconectada"
	StateCredencialInvalida = "CredencialInvalida"
	StateErroConfig         = "ErroConfig"
	StatePausada            = "Pausada"
	StateSemRede            = "SemRede"
	StateDesativada         = "Desativada"

	ClassTransitorio  = "transitorio"
	ClassCredencial   = "credencial"
	ClassConfiguracao = "configuracao"
)

// Valores de NoticeEvent.Kind (os mesmos de domain.NoticeKind; um teste do
// serviço os amarra).
const (
	NoticeDown       = "down"
	NoticeUp         = "up"
	NoticeCredential = "credential"
	NoticeConfig     = "config"
)

// Payloads de pedidos.
type (
	Hello struct {
		Protocol   int    `json:"protocol"`
		AppVersion string `json:"appVersion"`
	}
	VPNRef struct {
		VPN string `json:"vpn"`
	}
	PauseRequest struct {
		VPN       string `json:"vpn"`
		UntilUnix *int64 `json:"untilUnix"` // null ou ausente = pausa indefinida (até retomar)
	}
	SetEnabledRequest struct {
		VPN     string `json:"vpn"`
		Enabled bool   `json:"enabled"`
	}
	AddVPNRequest struct {
		Config config.RawVPN `json:"config"`
	}
	UpdateVPNRequest struct {
		Name   string        `json:"name"`
		Config config.RawVPN `json:"config"`
	}
	RemoveVPNRequest struct {
		Name string `json:"name"`
	}
	SetGlobalRequest struct {
		Notifications *bool   `json:"notifications,omitempty"`
		LogLevel      *string `json:"logLevel,omitempty"`
	}
	LogTailRequest struct {
		MaxBytes int `json:"maxBytes"`
	}
)

// Payloads de respostas e eventos.
type (
	// VPNView é o estado público de uma VPN (snapshot e vpnState).
	VPNView struct {
		Name             string     `json:"name"`
		Entry            string     `json:"entry"`
		Enabled          bool       `json:"enabled"`
		CheckKind        string     `json:"checkKind"`
		State            string     `json:"state"`
		SinceUnix        int64      `json:"sinceUnix"`
		LastCheckUnix    int64      `json:"lastCheckUnix,omitempty"`
		LatencyMs        int64      `json:"latencyMs,omitempty"` // ausente = 0
		Failures         int        `json:"failures"`
		Attempt          int        `json:"attempt"`
		NextAttemptUnix  int64      `json:"nextAttemptUnix,omitempty"`
		Reconnects24h    int        `json:"reconnects24h"`
		LastError        *ErrorInfo `json:"lastError,omitempty"`
		PausedUntilUnix  int64      `json:"pausedUntilUnix,omitempty"`
		PausedIndefinite bool       `json:"pausedIndefinite,omitempty"`
		// BlockedUntilUnix: em CredencialInvalida restaurada do state.json
		// (sem LastError), o instante em que o serviço tenta de novo sozinho.
		BlockedUntilUnix int64 `json:"blockedUntilUnix,omitempty"`
	}
	ErrorInfo struct {
		Class   string `json:"class"`
		Code    uint32 `json:"code"`
		Message string `json:"message"`
	}
	Snapshot struct {
		VPNs          []VPNView `json:"vpns"`
		Notifications bool      `json:"notifications"`
		// Config é o estado atual do config.json: quem se inscreve depois de
		// um configStatus ruim fica sabendo pelo snapshot.
		Config *ConfigStatus `json:"config,omitempty"`
	}
	NoticeEvent struct {
		VPN  string `json:"vpn"`
		Kind string `json:"kind"`
		Text string `json:"text"`
	}
	ConfigStatus struct {
		OK      bool                `json:"ok"`
		Message string              `json:"message,omitempty"`
		Fields  []config.FieldError `json:"fields,omitempty"`
	}
	RasEntry struct {
		Name      string `json:"name"`
		Monitored bool   `json:"monitored"`
	}
	RasEntries struct {
		Entries []RasEntry `json:"entries"`
	}
	LogTail struct {
		Text string `json:"text"`
	}
)

// NewMessage monta uma mensagem com payload serializado.
func NewMessage(id, typ string, payload any) (Message, error) {
	m := Message{V: EnvelopeVersion, ID: id, Type: typ}
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return Message{}, err
		}
		m.Payload = b
	}
	return m, nil
}

// MustMessage é NewMessage para payloads que sempre serializam (tipos deste pacote).
func MustMessage(id, typ string, payload any) Message {
	m, err := NewMessage(id, typ, payload)
	if err != nil {
		panic(err)
	}
	return m
}

// ErrorMessage monta a resposta de erro.
func ErrorMessage(id string, e *Error) Message { return MustMessage(id, TypeError, e) }

// DecodePayload decodifica estritamente (campos desconhecidos são erro).
// Observação: o encoding/json aceita chaves com caixa diferente e duplicadas
// (vale a última); tolerado porque o pipe é local e autenticado por ACL.
func DecodePayload(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := config.DecodeStrict(raw, out); err != nil {
		return &Error{Code: CodeBadRequest, Message: err.Error()}
	}
	return nil
}
