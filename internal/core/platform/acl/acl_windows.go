//go:build windows

package acl

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

type winSecurer struct{}

// New devolve o Securer real.
func New() Securer { return winSecurer{} }

func (winSecurer) EnsureDir(path string) (bool, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return false, err
	}
	cur, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, fmt.Errorf("lendo a segurança de %s: %w", path, err)
	}
	if Matches(cur.String()) {
		return false, nil
	}
	sd, err := windows.SecurityDescriptorFromString(DirSDDL)
	if err != nil {
		return false, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return false, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return false, err
	}
	// Dono = Administradores (tira o WRITE_DAC implícito de quem pré-criou a
	// pasta); PROTECTED desliga a herança; o Windows propaga as ACEs OICI.
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner, nil, dacl, nil)
	if err != nil {
		return false, fmt.Errorf("aplicando a segurança em %s: %w", path, err)
	}
	return true, nil
}
