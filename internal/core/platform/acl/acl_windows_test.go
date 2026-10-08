//go:build windows

package acl

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func needAdmin(t *testing.T) {
	t.Helper()
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("exige processo elevado (o runner do CI é)")
	}
}

func secOf(t *testing.T, p string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, secInfoOwnerDACL)
	if err != nil {
		t.Fatal(err)
	}
	return sd.String()
}

func junction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
}

// Aplica dono e DACL, confere, e a segunda chamada não muda nada.
func TestWindowsEnsureDir(t *testing.T) {
	needAdmin(t)
	dir := filepath.Join(t.TempDir(), "VPNMonitor")
	changed, err := New().EnsureDir(dir)
	if err != nil || !changed {
		t.Fatalf("primeira aplicação: %v %v", changed, err)
	}
	if got := secOf(t, dir); !Matches(got) {
		t.Fatalf("segurança resultante %v", got)
	}
	if changed, err := New().EnsureDir(dir); err != nil || changed {
		t.Fatalf("segunda chamada deveria ser no-op: %v %v", changed, err)
	}
}

// Pasta pré-criada por outro: filho com DACL protegida + ACE para Todos e uma
// junção em logs são saneados/removidos; o alvo da junção fica intocado.
func TestWindowsEnsureDirSanitizesTree(t *testing.T) {
	needAdmin(t)
	base := t.TempDir()
	dir := filepath.Join(base, "VPNMonitor")
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "config.json")
	if err := os.WriteFile(child, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, _ := sd.DACL()
	if err := windows.SetNamedSecurityInfo(child, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(base, "alvo")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(target, "intocado.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	junction(t, filepath.Join(dir, "logs", "junc"), target)

	changed, err := New().EnsureDir(dir)
	if err != nil || !changed {
		t.Fatalf("EnsureDir: %v %v", changed, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "logs", "junc")); !os.IsNotExist(err) {
		t.Fatalf("junção deveria ter sido removida: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("alvo da junção foi tocado: %v", err)
	}
	if got := secOf(t, child); !ChildMatches(got) || strings.Contains(got, "WD") {
		t.Fatalf("filho não foi saneado: %v", got)
	}
	if changed, err := New().EnsureDir(dir); err != nil || changed {
		t.Fatalf("segunda chamada deveria ser no-op: %v %v", changed, err)
	}
}

// Raiz que é junção: posta de lado e recriada; o alvo fica intocado.
func TestWindowsEnsureDirRootJunction(t *testing.T) {
	needAdmin(t)
	base := t.TempDir()
	target := filepath.Join(base, "alvo")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	before := secOf(t, target)
	dir := filepath.Join(base, "VPNMonitor")
	junction(t, dir, target)

	changed, err := New().EnsureDir(dir)
	if !errors.Is(err, ErrQuarantined) || !changed {
		t.Fatalf("esperava ErrQuarantined com changed: %v %v", changed, err)
	}
	if got := secOf(t, dir); !Matches(got) {
		t.Fatalf("raiz recriada com segurança errada: %v", got)
	}
	if got := secOf(t, target); got != before {
		t.Fatalf("alvo da junção foi alterado: %v != %v", got, before)
	}
	if m, _ := filepath.Glob(dir + ".naoconfiavel-*"); len(m) != 1 {
		t.Fatalf("link deveria estar ao lado: %v", m)
	}
}

// Raiz cujo dono é outro usuário (aqui, o grupo Users): posta de lado.
func TestWindowsEnsureDirRootForeignOwner(t *testing.T) {
	needAdmin(t)
	dir := filepath.Join(t.TempDir(), "VPNMonitor")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	users, err := windows.StringToSid("BU")
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, users, nil, nil, nil); err != nil {
		t.Skipf("não foi possível trocar o dono neste ambiente: %v", err)
	}
	changed, err := New().EnsureDir(dir)
	if !errors.Is(err, ErrQuarantined) || !changed {
		t.Fatalf("esperava ErrQuarantined: %v %v", changed, err)
	}
	if got := secOf(t, dir); !Matches(got) {
		t.Fatalf("raiz recriada com segurança errada: %v", got)
	}
}
