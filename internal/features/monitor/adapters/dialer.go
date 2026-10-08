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

// Dial executa a discagem inteira. Cancelar ctx (pausa, parada) ou estourar
// Timeout desliga com RasHangUp antes de voltar.
func (d *Dialer) Dial(ctx context.Context, job DialJob) DialOutcome {
	if job.HangupFirst {
		if a, found, err := FindActive(d.RAS, job.Entry); err == nil && found {
			_ = d.RAS.HangUp(a.Handle)
		}
	}
	creds, err := d.Creds.Resolve(ctx, job.Name, job.Entry)
	if err != nil {
		var re *ras.Error
		if errors.As(err, &re) {
			return DialOutcome{Err: d.dialErr(re.Code)}
		}
		return DialOutcome{Err: &domain.DialError{Class: ras.ClassTransitorio, Message: "resolvendo credencial: " + err.Error()}}
	}
	out := DialOutcome{Fingerprint: creds.Fingerprint}
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
			_ = d.RAS.HangUp(h)
			out.Cancelled = true
			out.Err = &domain.DialError{Class: ras.ClassTransitorio, Message: "discagem cancelada"}
			return out
		case <-deadline.C():
			_ = d.RAS.HangUp(h)
			out.Err = &domain.DialError{Class: ras.ClassTransitorio,
				Message: fmt.Sprintf("tempo esgotado após %s", job.Timeout)}
			return out
		case <-tick.C():
			st, err := d.RAS.Status(h)
			if err != nil {
				_ = d.RAS.HangUp(h)
				out.Err = &domain.DialError{Class: ras.ClassTransitorio, Message: err.Error()}
				return out
			}
			switch st.State {
			case ras.StateConnected:
				return out
			case ras.StateDisconnected:
				_ = d.RAS.HangUp(h)
				out.Err = d.dialErr(st.Code)
				return out
			}
			tick.Reset(poll)
		}
	}
}
