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

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/token"
)

// needAdmin exige processo elevado e faz o processo criar objetos com dono
// Administradores, como a CLI elevada faz em produção: com a política padrão
// do Windows ("Criador do objeto"), o que um administrador elevado cria tem
// dono = a conta do usuário, que a regra não aceita. Efeito no processo de
// teste inteiro, idempotente.
func needAdmin(t *testing.T) {
	t.Helper()
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("exige processo elevado (o runner do CI é)")
	}
	if err := token.SetDefaultOwnerAdmins(); err != nil {
		t.Fatal(err)
	}
}

// setOwnerToUser troca o dono de p para a conta do usuário do processo (o
// que a política "Criador do objeto" faria sem o ajuste do token).
func setOwnerToUser(t *testing.T, p string) {
	t.Helper()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, u.User.Sid, nil, nil, nil); err != nil {
		t.Fatal(err)
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

// quarantined confere que a raiz foi posta de lado uma vez, pelo motivo
// esperado (para o teste não passar por outro desvio, como o dono), e
// recriada certa.
func quarantined(t *testing.T, dir, reason string, changed bool, err error) string {
	t.Helper()
	if !errors.Is(err, ErrQuarantined) || !changed {
		t.Fatalf("esperava ErrQuarantined com changed: %v %v", changed, err)
	}
	if !strings.HasSuffix(err.Error(), "("+reason+")") {
		t.Fatalf("motivo errado: quer %q em %v", reason, err)
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

// Árvore boa criada pelo próprio EnsureDir, com arquivos dentro (criados com
// dono Administradores, como a CLI elevada faz): no-op depois.
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

// Raiz vazia pré-criada com dono Administradores e a DACL herdada: o
// descritor é reaplicado.
func TestWindowsEnsureDirReappliesEmptyRoot(t *testing.T) {
	needAdmin(t)
	dir := filepath.Join(t.TempDir(), "VPNMonitor")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	changed, err := New().EnsureDir(dir)
	if err != nil || !changed || !Matches(secOf(t, dir)) {
		t.Fatalf("changed=%v err=%v segurança %v", changed, err, secOf(t, dir))
	}
	if m, _ := filepath.Glob(dir + ".naoconfiavel-*"); len(m) != 0 {
		t.Fatalf("não deveria pôr de lado: %v", m)
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
	side := quarantined(t, dir, "config.json: DACL protegida", changed, err)
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
	quarantined(t, dir, filepath.Join("logs", "junc")+": ponto de reparse", changed, err)
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
	quarantined(t, dir, "raiz é ponto de reparse", changed, err)
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
	quarantined(t, dir, "DACL da raiz diverge e há conteúdo", changed, err)
}

// Raiz com DACL que nega tudo a Todos e contém um arquivo: o rename de
// quarentena precisa funcionar mesmo assim (SeBackup/SeRestore, por handle).
func TestWindowsEnsureDirRootDeniesEveryone(t *testing.T) {
	needAdmin(t)
	dir := filepath.Join(t.TempDir(), "VPNMonitor")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "antigo.txt"), []byte("antigo"), 0o600); err != nil {
		t.Fatal(err)
	}
	setDACL(t, dir, "D:P(D;OICI;FA;;;WD)", true)

	changed, err := New().EnsureDir(dir)
	// Administradores pode ler o descritor (dono) mas não listar a raiz.
	side := quarantined(t, dir, ".: ilegível", changed, err)
	// Devolve acesso ao diretório de lado para ler o conteúdo e permitir a limpeza.
	t.Cleanup(func() { setDACL(t, side, "D:P(A;OICI;FA;;;BA)", true) })
	setDACL(t, side, "D:P(A;OICI;FA;;;BA)", true)
	if b, err := os.ReadFile(filepath.Join(side, "antigo.txt")); err != nil || string(b) != "antigo" {
		t.Fatalf("conteúdo antigo deveria estar preservado: %q %v", b, err)
	}
}

// Filho com dono = conta do usuário (o que um editor que salva por arquivo
// temporário + rename produz): a raiz inteira vai para o lado.
func TestWindowsEnsureDirChildOwnedByUser(t *testing.T) {
	needAdmin(t)
	dir := filepath.Join(t.TempDir(), "VPNMonitor")
	if _, err := New().EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "config.json")
	if err := os.WriteFile(child, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := New().EnsureDir(dir); err != nil || changed {
		t.Fatalf("com dono Administradores confere: %v %v", changed, err)
	}
	setOwnerToUser(t, child)

	changed, err := New().EnsureDir(dir)
	side := quarantined(t, dir, "config.json: dono não confiável", changed, err)
	if b, err := os.ReadFile(filepath.Join(side, "config.json")); err != nil || string(b) != "{}" {
		t.Fatalf("conteúdo antigo deveria estar preservado: %q %v", b, err)
	}
}

// Raiz vazia com dono = conta do usuário: não é confiável (o dono mantém
// WRITE_DAC), então vai para o lado em vez de ter o descritor reaplicado.
func TestWindowsEnsureDirEmptyRootOwnedByUser(t *testing.T) {
	needAdmin(t)
	dir := filepath.Join(t.TempDir(), "VPNMonitor")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	setOwnerToUser(t, dir)

	changed, err := New().EnsureDir(dir)
	quarantined(t, dir, "dono da raiz não confiável", changed, err)
}
