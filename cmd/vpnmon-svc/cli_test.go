package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/fake"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/svc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/credentials"
)

type testEnv struct {
	env
	out, errb *bytes.Buffer
	dir       string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	te := &testEnv{out: &bytes.Buffer{}, errb: &bytes.Buffer{}, dir: dir}
	te.env = env{
		stdin: strings.NewReader(""), stdout: te.out, stderr: te.errb,
		dataDir:      func() (string, error) { return dir, nil },
		elevated:     func() bool { return true },
		dpapi:        fake.DPAPI{},
		readPassword: func(io.Reader) (string, error) { return "digitada", nil },
		dial: func(context.Context) (*ipc.Client, error) {
			return nil, errors.New("serviço VPN Monitor inacessível")
		},
		platform:   func() (Platform, error) { return Platform{}, errors.New("sem plataforma") },
		install:    func(string) error { return nil },
		uninstall:  func() error { return nil },
		isService:  func() (bool, error) { return false, nil },
		runService: func(svc.Hooks) error { return nil },
		now:        func() time.Time { return time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC) },
	}
	return te
}

func (te *testEnv) run(args ...string) int {
	te.out.Reset()
	te.errb.Reset()
	return runCLI(args, te.env)
}

func (te *testEnv) writeConfig(t *testing.T, vpns ...string) {
	t.Helper()
	c := config.Empty()
	for _, n := range vpns {
		c.VPNs = append(c.VPNs, config.RawVPN{Name: n, RasEntry: "VPN " + n, Check: &config.RawCheck{Kind: config.CheckLink}}.Normalize())
	}
	if _, err := config.Save(filepath.Join(te.dir, "config.json"), c); err != nil {
		t.Fatal(err)
	}
}

func TestVersionWorksWithoutElevation(t *testing.T) {
	te := newTestEnv(t)
	te.elevated = func() bool { return false }
	if code := te.run("version"); code != 0 || !strings.HasPrefix(te.out.String(), "vpnmon-svc dev") {
		t.Fatalf("%d %q", code, te.out)
	}
	if code := te.run("status"); code != 1 || !strings.Contains(te.errb.String(), "administrador") {
		t.Fatalf("sem elevação: %d %q", code, te.errb)
	}
}

func TestUsageErrors(t *testing.T) {
	te := newTestEnv(t)
	for _, args := range [][]string{{}, {"formatar"}, {"credential"}, {"credential", "set", "Matriz"}, {"vpn", "add", "--name", "x"}} {
		if code := te.run(args...); code != 2 {
			t.Errorf("%v: código %d", args, code)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	if code := te.run("config", "validate"); code != 0 || !strings.Contains(te.out.String(), "válido (1 VPN(s))") {
		t.Fatalf("%d %q %q", code, te.out, te.errb)
	}
	bad := filepath.Join(te.dir, "ruim.json")
	_ = os.WriteFile(bad, []byte(`{"version":2,"vpns":[{"name":"","rasEntry":"x","check":{"kind":"link"}}]}`), 0o600)
	if code := te.run("config", "validate", bad); code != 1 || !strings.Contains(te.out.String(), "vpns[0].name") {
		t.Fatalf("%d %q", code, te.out)
	}
}

func TestCredentialSetClearList(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz", "Filial")
	// PowerShell manda CRLF; espaços fazem parte da senha.
	te.stdin = strings.NewReader(" s3nha \r\n")
	if code := te.run("credential", "set", "Matriz", "--user", "ana", "--password-stdin"); code != 0 {
		t.Fatalf("set: %q", te.errb)
	}
	vault := credentials.Vault{Dir: filepath.Join(te.dir, "credentials"), DPAPI: fake.DPAPI{}}
	if u, pw, ok, _ := vault.Get("Matriz"); !ok || u != "ana" || pw.Reveal() != " s3nha " {
		t.Fatalf("cofre: %q %q %v", u, pw.Reveal(), ok)
	}
	if strings.Contains(te.out.String()+te.errb.String(), "s3nha") {
		t.Fatal("senha ecoada na saída")
	}
	if code := te.run("credential", "set", "--user", "bia", "Filial"); code != 0 { // sem stdin: leitura sem eco
		t.Fatalf("set interativo: %q", te.errb)
	}
	if _, pw, _, _ := vault.Get("Filial"); pw.Reveal() != "digitada" {
		t.Fatal("senha digitada")
	}
	if code := te.run("credential", "set", "Inexistente", "--user", "x", "--password-stdin"); code != 1 {
		t.Fatal("VPN inexistente deve falhar")
	}
	te.run("credential", "list")
	if !strings.Contains(te.out.String(), "Matriz\tcofre") || !strings.Contains(te.out.String(), "Filial\tcofre") {
		t.Fatalf("list: %q", te.out)
	}
	if code := te.run("credential", "clear", "Matriz"); code != 0 || vault.Has("Matriz") {
		t.Fatal("clear")
	}
	te.run("credential", "list")
	if !strings.Contains(te.out.String(), "Matriz\t—") {
		t.Fatalf("list após clear: %q", te.out)
	}
}
