//go:build windows

package token

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// Elevado: o que o processo cria depois nasce com dono Administradores.
// Não elevado: o Windows recusa e a função devolve erro (sem efeito parcial).
func TestSetDefaultOwnerAdmins(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		if err := SetDefaultOwnerAdmins(); err == nil {
			t.Fatal("processo não elevado deveria ser recusado")
		}
		return
	}
	if err := SetDefaultOwnerAdmins(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "d")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "f")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir, f} {
		sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		owner, _, err := sd.Owner()
		if err != nil {
			t.Fatal(err)
		}
		if !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			t.Fatalf("%s: dono %s, esperava Administradores", p, owner)
		}
	}
}
