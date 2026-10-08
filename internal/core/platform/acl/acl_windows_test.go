//go:build windows

package acl

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

func setDACL(t *testing.T, p, sddl string, protected bool) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, _ := sd.DACL()
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
	if protected {
		info |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	if err := windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

// quarantined confere que a raiz foi posta de lado uma vez e recriada certa.
func quarantined(t *testing.T, dir string, changed bool, err error) string {
	t.Helper()
	if !errors.Is(err, ErrQuarantined) || !changed {
		t.Fatalf("esperava ErrQuarantined com changed: %v %v", changed, err)
	}
	if got := secOf(t, dir); !Matches(got) {
		t.Fatalf("raiz recriada com segurança errada: %v", got)
	}
	m, _ := filepath.Glob(dir + ".naoconfiavel-*")
	if len(m) != 1 {
		t.Fatalf("esperava um diretório posto de lado: %v", m)
	}
	return m[0]
}

// Árvore boa criada pelo próprio EnsureDir, com arquivos dentro: no-op depois.
func TestWindowsEnsureDirGoodTree(t *testing.T) {
	needAdmin(t)
	dir := filepath.Join(t.TempDir(), "pai", "VPNMonitor")
	changed, err := New().EnsureDir(dir)
	if err != nil || !changed {
		t.Fatalf("primeira aplicação: %v %v", changed, err)
	}
	if got := secOf(t, dir); !Matches(got) {
		t.Fatalf("segurança resultante %v", got)
	}
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "logs", "a.log"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := New().EnsureDir(dir); err != nil || changed {
		t.Fatalf("segunda chamada deveria ser no-op: %v %v", changed, err)
	}
}

// Raiz vazia pré-criada com a DACL herdada: o descritor é reaplicado.
func TestWindowsEnsureDirReappliesEmptyRoot(t *testing.T) {
	needAdmin(t)
	dir := filepath.Join(t.TempDir(), "VPNMonitor")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	changed, err := New().EnsureDir(dir)
	if err != nil && !errors.Is(err, ErrQuarantined) {
		t.Fatal(err)
	}
	if !changed || !Matches(secOf(t, dir)) {
		t.Fatalf("changed=%v segurança %v", changed, secOf(t, dir))
	}
}

// Filho com DACL protegida e ACE explícita para Todos: raiz posta de lado,
// conteúdo antigo intocado.
func TestWindowsEnsureDirBadChild(t *testing.T) {
	needAdmin(t)
	dir := filepath.Join(t.TempDir(), "VPNMonitor")
	if _, err := New().EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "config.json")
	if err := os.WriteFile(child, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	setDACL(t, child, "D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;WD)", true)
	before := secOf(t, child)

	changed, err := New().EnsureDir(dir)
	side := quarantined(t, dir, changed, err)
	moved := filepath.Join(side, "config.json")
	if b, err := os.ReadFile(moved); err != nil || string(b) != "{}" {
		t.Fatalf("conteúdo antigo deveria estar intocado: %q %v", b, err)
	}
	if got := secOf(t, moved); got != before {
		t.Fatalf("segurança do filho antigo mudou: %v != %v", got, before)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("raiz nova deveria estar vazia: %v", entries)
	}
}

// Junção em logs: raiz posta de lado; o descritor do alvo fica inalterado.
func TestWindowsEnsureDirChildJunction(t *testing.T) {
	needAdmin(t)
	base := t.TempDir()
	dir := filepath.Join(base, "VPNMonitor")
	if _, err := New().EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "logs"), 0o700); err != nil {
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
	before := secOf(t, target)
	junction(t, filepath.Join(dir, "logs", "junc"), target)

	changed, err := New().EnsureDir(dir)
	quarantined(t, dir, changed, err)
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("alvo da junção foi tocado: %v", err)
	}
	if got := secOf(t, target); got != before {
		t.Fatalf("descritor do alvo mudou: %v != %v", got, before)
	}
}

// Raiz que é junção: posta de lado; o alvo fica intocado.
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
	quarantined(t, dir, changed, err)
	if got := secOf(t, target); got != before {
		t.Fatalf("alvo da junção foi alterado: %v != %v", got, before)
	}
}

// Raiz com conteúdo e DACL que nega SYSTEM: posta de lado.
func TestWindowsEnsureDirRootDeniesSystem(t *testing.T) {
	needAdmin(t)
	dir := filepath.Join(t.TempDir(), "VPNMonitor")
	if _, err := New().EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	setDACL(t, dir, "D:P(D;OICI;FA;;;SY)(A;OICI;FA;;;BA)", true)

	changed, err := New().EnsureDir(dir)
	quarantined(t, dir, changed, err)
}
