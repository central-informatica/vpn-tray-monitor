package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// Credentials é a credencial resolvida para uma discagem.
type Credentials struct {
	User     string
	Password shared.Secret
	Saved    *ras.Saved // parâmetros salvos do Windows (marcador intacto)
	// Fingerprint identifica a credencial (arquivo do cofre ou marcador do
	// Windows) sem revelá-la; "" sem credencial.
	Fingerprint string
	Source      string // "cofre", "windows" ou "nenhuma"
}

// CredentialResolver resolve a credencial (implementado por features/credentials).
type CredentialResolver interface {
	Resolve(ctx context.Context, name, entry string) (Credentials, error)
	Fingerprint(ctx context.Context, name, entry string) string
}

// DialOutcome é o resultado de Dialer.Dial.
type DialOutcome struct {
	Err         *domain.DialError // nil = conectou
	Fingerprint string
	Cancelled   bool
}

// Dialer disca de forma assíncrona e acompanha com RasGetConnectStatus.
type Dialer struct {
	RAS          ras.Client
	Creds        CredentialResolver
	Clock        shared.Clock
	PollInterval time.Duration // padrão 250 ms (§4.4)
}

// DialJob é uma discagem pedida pelo supervisor.
type DialJob struct {
	Name, Entry string
	Timeout     time.Duration
	HangupFirst bool
}

func (d *Dialer) dialErr(code uint32) *domain.DialError {
	return &domain.DialError{Class: ras.Classify(code), Code: code, Message: d.RAS.ErrorText(code)}
}

// hangUp desliga h; falha vira sufixo da mensagem do erro final (§12: nada
// de erro engolido). err pode ser nil quando só o HangUp interessa.
func (d *Dialer) hangUp(h ras.Handle, e *domain.DialError) {
	if err := d.RAS.HangUp(h); err != nil && e != nil {
		e.Message += "; desligar falhou: " + err.Error()
	}
}

// rasErr classifica falhas de consulta como as de StartDial.
func (d *Dialer) rasErr(err error) *domain.DialError {
	var re *ras.Error
	if errors.As(err, &re) {
		return d.dialErr(re.Code)
	}
	return &domain.DialError{Class: ras.ClassTransitorio, Message: err.Error()}
}

// Dial executa a discagem inteira. Cancelar ctx (pausa, parada) ou estourar
// Timeout desliga com RasHangUp antes de voltar.
func (d *Dialer) Dial(ctx context.Context, job DialJob) DialOutcome {
	if job.HangupFirst {
		a, found, err := FindActive(d.RAS, job.Entry)
		if err != nil {
			return DialOutcome{Err: &domain.DialError{Class: ras.ClassTransitorio,
				Message: "consultando conexões antes de rediscar: " + err.Error()}}
		}
		if found {
			if err := d.RAS.HangUp(a.Handle); err != nil {
				return DialOutcome{Err: &domain.DialError{Class: ras.ClassTransitorio,
					Message: "desligando conexão antiga: " + err.Error()}}
			}
		}
	}
	creds, err := d.Creds.Resolve(ctx, job.Name, job.Entry)
	if err != nil {
		if ctx.Err() != nil {
			return DialOutcome{Cancelled: true, Err: &domain.DialError{Class: ras.ClassTransitorio, Message: "discagem cancelada"}}
		}
		var re *ras.Error
		if errors.As(err, &re) {
			return DialOutcome{Err: d.dialErr(re.Code)}
		}
		return DialOutcome{Err: &domain.DialError{Class: ras.ClassTransitorio, Message: "resolvendo credencial: " + err.Error()}}
	}
	out := DialOutcome{Fingerprint: creds.Fingerprint}
	if ctx.Err() != nil {
		creds.Password.Wipe()
		out.Cancelled = true
		out.Err = &domain.DialError{Class: ras.ClassTransitorio, Message: "discagem cancelada"}
		return out
	}
	h, err := d.RAS.StartDial(ras.DialRequest{Entry: job.Entry, User: creds.User, Password: creds.Password, Saved: creds.Saved})
	creds.Password.Wipe()
	if err != nil {
		var re *ras.Error
		if errors.As(err, &re) {
			out.Err = d.dialErr(re.Code)
		} else {
			out.Err = &domain.DialError{Class: ras.ClassConfiguracao, Message: err.Error()}
		}
		return out
	}
	poll := d.PollInterval
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	deadline := d.Clock.NewTimer(job.Timeout)
	defer deadline.Stop()
	tick := d.Clock.NewTimer(poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			out.Cancelled = true
			out.Err = &domain.DialError{Class: ras.ClassTransitorio, Message: "discagem cancelada"}
			d.hangUp(h, out.Err)
			return out
		case <-deadline.C():
			out.Err = &domain.DialError{Class: ras.ClassTransitorio,
				Message: fmt.Sprintf("tempo esgotado após %s", job.Timeout)}
			d.hangUp(h, out.Err)
			return out
		case <-tick.C():
			st, err := d.RAS.Status(h)
			if err != nil {
				out.Err = d.rasErr(err)
				d.hangUp(h, out.Err)
				return out
			}
			switch st.State {
			case ras.StateConnected:
				return out
			case ras.StateDisconnected:
				out.Err = d.dialErr(st.Code)
				d.hangUp(h, out.Err)
				return out
			}
			tick.Reset(poll)
		}
	}
}
