package ras

import (
	"context"
	"fmt"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// Handle é um HRASCONN.
type Handle uintptr

// State resume o RASCONNSTATE no que interessa ao monitor.
type State int

const (
	StateConnecting State = iota
	StateConnected
	StateDisconnected
)

func (s State) String() string {
	if s < StateConnecting || s > StateDisconnected {
		return fmt.Sprintf("State(%d)", int(s))
	}
	return [...]string{"conectando", "conectada", "desconectada"}[s]
}

// Status é o estado de uma conexão. Code é o erro RAS quando a discagem
// terminou em falha (0 se não houve).
type Status struct {
	State State
	Code  uint32
}

// StatusFromRaw converte o RASCONNSTATUSW decodificado.
func StatusFromRaw(cs ConnStatus) Status {
	switch {
	case cs.State == RASCS_Connected:
		return Status{State: StateConnected}
	case cs.State == RASCS_Disconnected || cs.Error != 0:
		return Status{State: StateDisconnected, Code: cs.Error}
	}
	return Status{State: StateConnecting}
}

// ActiveConn é uma conexão RAS ativa.
type ActiveConn struct {
	Handle Handle
	Entry  string
}

// Saved são os parâmetros que o Windows guarda para a entrada
// (RasGetEntryDialParams). Desde o Vista, o campo de senha traz um marcador,
// não a senha: ele é repassado intacto ao RasDialW e só seu hash é comparado.
type Saved struct {
	params      DialParams
	User        string
	HasPassword bool
}

// NewSaved embrulha os parâmetros lidos (ou montados pelo fake).
func NewSaved(p DialParams, hasPassword bool) *Saved {
	return &Saved{params: p.Clone(), User: p.User(), HasPassword: hasPassword}
}

// Fingerprint é o hash do marcador; "" sem senha salva. Nunca logar.
func (s *Saved) Fingerprint() string {
	if s == nil || !s.HasPassword {
		return ""
	}
	return s.params.PasswordFingerprint()
}

// String não expõe o marcador.
func (s *Saved) String() string { return fmt.Sprintf("ras.Saved{user=%q}", s.User) }

// DialRequest descreve uma discagem. Com Saved, parte dos parâmetros salvos
// do Windows; User/Password, se informados, sobrescrevem.
type DialRequest struct {
	Entry    string
	User     string
	Password shared.Secret
	Saved    *Saved
}

// BuildDialParams monta o RASDIALPARAMSW da discagem. Com Saved, o tamanho é
// o do buffer salvo (o que a API aceitou); senão, size.
func BuildDialParams(req DialRequest, size uint32) (DialParams, error) {
	var p DialParams
	if req.Saved != nil {
		p = req.Saved.params.Clone()
	} else {
		p = NewDialParams(size)
	}
	if err := p.SetEntry(req.Entry); err != nil {
		return nil, fmt.Errorf("entrada: %w", err)
	}
	if req.User != "" {
		if err := p.SetUser(req.User); err != nil {
			return nil, fmt.Errorf("usuário: %w", err)
		}
	}
	if !req.Password.IsEmpty() {
		if err := p.SetPassword(req.Password.Reveal()); err != nil {
			p.Wipe()
			return nil, fmt.Errorf("senha: %w", err)
		}
	}
	return p, nil
}

// Client é o acesso à API RAS (rasapi32.dll).
type Client interface {
	// Entries lista as entradas do catálogo de todos os usuários.
	Entries() ([]string, error)
	// Active lista as conexões ativas (RasEnumConnections).
	Active() ([]ActiveConn, error)
	// StartDial inicia uma discagem assíncrona (RasDialW) e devolve o handle.
	// Falha imediata vem como *Error.
	StartDial(req DialRequest) (Handle, error)
	// Status consulta RasGetConnectStatus; handle inválido = desconectada.
	// Após um estado final de falha o handle continua válido: quem chama
	// DEVE chamar HangUp para liberá-lo.
	Status(h Handle) (Status, error)
	// HangUp desliga e espera o handle ser liberado.
	HangUp(h Handle) error
	// Saved lê os parâmetros salvos da entrada (RasGetEntryDialParams).
	Saved(entry string) (*Saved, error)
	// WatchDisconnects avisa (agregado) a cada desconexão de qualquer entrada.
	WatchDisconnects(ctx context.Context) (<-chan struct{}, error)
	// ErrorText devolve a mensagem do Windows para o código.
	ErrorText(code uint32) string
}
