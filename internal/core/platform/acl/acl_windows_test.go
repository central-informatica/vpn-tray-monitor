//go:build windows

package acl

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// Só no job Windows (runner é administrador): aplica dono e DACL, confere,
// e a segunda chamada não muda nada.
func TestWindowsEnsureDir(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("exige processo elevado (o runner do CI é)")
	}
	dir := filepath.Join(t.TempDir(), "VPNMonitor")
	changed, err := New().EnsureDir(dir)
	if err != nil || !changed {
		t.Fatalf("primeira aplicação: %v %v", changed, err)
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || !Matches(sd.String()) {
		t.Fatalf("segurança resultante %v: %v", sd, err)
	}
	if changed, err := New().EnsureDir(dir); err != nil || changed {
		t.Fatalf("segunda chamada deveria ser no-op: %v %v", changed, err)
	}
}
