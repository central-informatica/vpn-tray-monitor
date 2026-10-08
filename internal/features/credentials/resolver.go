package credentials

import (
	"context"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// Origem da credencial.
const (
	SourceVault   = "cofre"
	SourceWindows = "windows"
	SourceNone    = "nenhuma"
)

// Resolution é a credencial resolvida para uma discagem.
type Resolution struct {
	User     string
	Password shared.Secret
	Saved    *ras.Saved
	// Fingerprint é "" somente quando não há credencial (SourceNone).
	Fingerprint string
	Source      string
}

// Resolver aplica a ordem cofre → Windows → nenhuma.
type Resolver struct {
	Vault Vault
	RAS   ras.Client
}

// Resolve devolve a credencial da VPN. Entrada inexistente no catálogo
// volta como *ras.Error 623 (o monitor classifica como ErroConfig). Falha de
// leitura/decifragem do cofre volta como erro, nunca como "sem credencial".
func (r Resolver) Resolve(_ context.Context, name, entry string) (Resolution, error) {
	saved, err := r.RAS.Saved(entry)
	if err != nil {
		return Resolution{}, err
	}
	user, pw, ok, err := r.Vault.Get(name)
	if err != nil {
		return Resolution{}, err
	}
	if ok {
		return Resolution{User: user, Password: pw, Saved: saved,
			Fingerprint: SourceVault + ":" + r.Vault.Fingerprint(name), Source: SourceVault}, nil
	}
	if saved.HasPassword {
		return Resolution{Saved: saved, Fingerprint: SourceWindows + ":" + saved.Fingerprint(), Source: SourceWindows}, nil
	}
	return Resolution{Saved: saved, Source: SourceNone}, nil
}

// Fingerprint identifica a credencial atual sem decifrá-la. "" só quando não
// há credencial; arquivo do cofre ilegível não vira "".
func (r Resolver) Fingerprint(_ context.Context, name, entry string) string {
	if fp := r.Vault.Fingerprint(name); fp != "" {
		return SourceVault + ":" + fp
	}
	if saved, err := r.RAS.Saved(entry); err == nil && saved.HasPassword {
		return SourceWindows + ":" + saved.Fingerprint()
	}
	return ""
}
