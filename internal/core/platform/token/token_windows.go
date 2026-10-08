//go:build windows

package token

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// tokenOwner espelha TOKEN_OWNER (x/sys/windows não o define).
type tokenOwner struct{ Owner *windows.SID }

// SetDefaultOwnerAdmins define Administradores (BA) como dono padrão dos
// objetos que este processo criar a partir de agora (TokenOwner do token do
// processo; vale para todas as threads que não estejam personificando).
// Exige processo elevado: no token filtrado, BA é só "deny-only" e o Windows
// recusa (ERROR_INVALID_OWNER). Confere relendo o token.
func SetDefaultOwnerAdmins() error {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_QUERY, &tok); err != nil {
		return fmt.Errorf("abrindo o token do processo: %w", err)
	}
	defer tok.Close()
	ba, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	o := tokenOwner{Owner: ba}
	if err := windows.SetTokenInformation(tok, windows.TokenOwner, (*byte)(unsafe.Pointer(&o)), uint32(unsafe.Sizeof(o))); err != nil {
		return fmt.Errorf("definindo Administradores como dono padrão: %w", err)
	}
	got, err := defaultOwner(tok)
	if err != nil {
		return fmt.Errorf("relendo o dono padrão: %w", err)
	}
	if !got.Equals(ba) {
		return fmt.Errorf("dono padrão ficou %s, esperava Administradores", got)
	}
	return nil
}

// DefaultOwner devolve o dono padrão (TokenOwner) do token do processo, no
// formato "DOMÍNIO\\nome" quando a conta resolve, senão o SID textual.
func DefaultOwner() (string, error) {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &tok); err != nil {
		return "", fmt.Errorf("abrindo o token do processo: %w", err)
	}
	defer tok.Close()
	sid, err := defaultOwner(tok)
	if err != nil {
		return "", err
	}
	if account, domain, _, err := sid.LookupAccount(""); err == nil {
		return domain + "\\" + account + " (" + sid.String() + ")", nil
	}
	return sid.String(), nil
}

// defaultOwner lê o TokenOwner do token (cópia do SID).
func defaultOwner(tok windows.Token) (*windows.SID, error) {
	var n uint32
	_ = windows.GetTokenInformation(tok, windows.TokenOwner, nil, 0, &n)
	if n == 0 {
		return nil, errors.New("tamanho de TokenOwner indisponível")
	}
	buf := make([]uint64, (n+7)/8) // alinhado para o ponteiro
	if err := windows.GetTokenInformation(tok, windows.TokenOwner, (*byte)(unsafe.Pointer(&buf[0])), n, &n); err != nil {
		return nil, err
	}
	return (*tokenOwner)(unsafe.Pointer(&buf[0])).Owner.Copy()
}
