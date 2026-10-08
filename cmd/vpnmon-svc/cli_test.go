package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/acl"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/fake"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/svc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/credentials"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

type testEnv struct {
	env
	out, errb *bytes.Buffer
	dir       string
	// readFile, se não nil, substitui a leitura de arquivos do serviço
	// (Platform.ReadFile) em startService.
	readFile func(string) ([]byte, error)
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	te := &testEnv{out: &bytes.Buffer{}, errb: &bytes.Buffer{}, dir: dir}
	te.env = env{
		stdin: strings.NewReader(""), stdout: te.out, stderr: te.errb,
		dataDir:      func() (string, error) { return dir, nil },
		elevated:     func() bool { return true },
		adminOwner:   func() error { return nil },
		dpapi:        fake.DPAPI{},
		securer:      &fake.ACL{},
		stdinConsole: func() bool { return false },
		readPassword: func(io.Reader) (string, error) { return "digitada", nil },
		dial: func(context.Context) (*ipc.Client, error) {
			return nil, errors.New("serviço VPN Monitor inacessível")
		},
		platform:     func() (Platform, error) { return Platform{}, errors.New("sem plataforma") },
		install:      func(string) error { return nil },
		uninstall:    func() error { return nil },
		isService:    func() (bool, error) { return false, nil },
		runService:   func(svc.Hooks) error { return nil },
		ensurePolicy: func() error { return nil },
		now:          func() time.Time { return time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC) },
		interrupt:    func(ctx context.Context) (context.Context, context.CancelFunc) { return context.WithCancel(ctx) },
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

// orderACL registra a ordem EnsureDir x gravação no cofre.
type orderACL struct {
	dirs      []string
	err       error
	changed   bool
	credsSeen func() bool // cofre já tem arquivos no momento do EnsureDir?
	sawCreds  bool
}

func (o *orderACL) EnsureDir(p string) (bool, error) {
	o.dirs = append(o.dirs, p)
	if o.credsSeen != nil && o.credsSeen() {
		o.sawCreds = true
	}
	return o.changed, o.err
}

func TestCredentialSetHardensDirFirst(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	vault := credentials.Vault{Dir: filepath.Join(te.dir, "credentials"), DPAPI: fake.DPAPI{}}
	o := &orderACL{credsSeen: func() bool { return vault.Has("Matriz") }}
	te.securer = o
	te.stdin = strings.NewReader("x\n")
	if code := te.run("credential", "set", "Matriz", "--user", "a", "--password-stdin"); code != 0 {
		t.Fatal(te.errb)
	}
	if len(o.dirs) != 1 || o.dirs[0] != te.dir || o.sawCreds || !vault.Has("Matriz") {
		t.Fatalf("ordem/pasta: %+v", o)
	}
}

func TestCredentialACLFailureBlocks(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	vault := credentials.Vault{Dir: filepath.Join(te.dir, "credentials"), DPAPI: fake.DPAPI{}}
	if err := vault.Set("Matriz", "velho", shared.NewSecret("antiga")); err != nil {
		t.Fatal(err)
	}
	te.securer = &orderACL{err: errors.New("acesso negado")}
	te.stdin = strings.NewReader("nova\n")
	if code := te.run("credential", "set", "Matriz", "--user", "novo", "--password-stdin"); code != 1 {
		t.Fatalf("set: %d", code)
	}
	if u, pw, _, _ := vault.Get("Matriz"); u != "velho" || pw.Reveal() != "antiga" {
		t.Fatal("cofre alterado")
	}
	if code := te.run("credential", "clear", "Matriz"); code != 1 || !vault.Has("Matriz") {
		t.Fatalf("clear: %d", code)
	}
}

func TestCredentialQuarantineWarnsAndWrites(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	te.securer = &orderACL{changed: true, err: acl.ErrQuarantined}
	te.stdin = strings.NewReader("pw\n")
	if code := te.run("credential", "set", "Matriz", "--user", "a", "--password-stdin"); code != 0 {
		t.Fatalf("%d %q", code, te.errb)
	}
	vault := credentials.Vault{Dir: filepath.Join(te.dir, "credentials"), DPAPI: fake.DPAPI{}}
	if !strings.Contains(te.errb.String(), "aviso") || !vault.Has("Matriz") {
		t.Fatalf("%q", te.errb)
	}
}

func TestCredentialSetConfigHandling(t *testing.T) {
	te := newTestEnv(t)
	vault := credentials.Vault{Dir: filepath.Join(te.dir, "credentials"), DPAPI: fake.DPAPI{}}
	te.stdin = strings.NewReader("pw\n")
	if code := te.run("credential", "set", "Matriz", "--user", "a", "--password-stdin"); code != 0 ||
		!strings.Contains(te.errb.String(), "config.json ainda não existe; nome não validado") || !vault.Has("Matriz") {
		t.Fatalf("sem config: %d %q", code, te.errb)
	}
	// config inválida: bloqueia e não grava.
	if err := os.WriteFile(filepath.Join(te.dir, "config.json"), []byte(`{"version":2,"vpns":[{"name":""}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Clear("Matriz"); err != nil {
		t.Fatal(err)
	}
	te.stdin = strings.NewReader("pw\n")
	if code := te.run("credential", "set", "Matriz", "--user", "a", "--password-stdin"); code != 1 || vault.Has("Matriz") {
		t.Fatalf("config inválida: %d", code)
	}
}

func TestMinorCLIRules(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	vault := credentials.Vault{Dir: filepath.Join(te.dir, "credentials"), DPAPI: fake.DPAPI{}}

	te.elevated = func() bool { return false }
	te.stdin = strings.NewReader("pw\n")
	if code := te.run("credential", "set", "Matriz", "--user", "a", "--password-stdin"); code != 1 || vault.Has("Matriz") {
		t.Fatalf("não elevado: %d", code)
	}
	if code := te.run("formatar"); code != 2 {
		t.Fatalf("desconhecido sem elevação: %d", code)
	}
	te.elevated = func() bool { return true }

	te.stdinConsole = func() bool { return true }
	te.stdin = strings.NewReader("pw\n")
	if code := te.run("credential", "set", "Matriz", "--user", "a", "--password-stdin"); code != 1 || vault.Has("Matriz") {
		t.Fatalf("stdin console: %d", code)
	}
	te.stdinConsole = func() bool { return false }

	if code := te.run("config", "validate", "a", "b"); code != 2 {
		t.Fatalf("validate a b: %d", code)
	}
	if code := te.run("uninstall"); code != 0 || !strings.Contains(te.out.String(), "removido") {
		t.Fatalf("uninstall: %d %q", code, te.out)
	}
	te.readPassword = func(io.Reader) (string, error) { return "", errCancelled }
	if code := te.run("credential", "set", "Matriz", "--user", "a"); code != 130 || vault.Has("Matriz") {
		t.Fatalf("cancelado: %d", code)
	}
}

// Todo comando elevado define o dono padrão Administradores antes de gravar;
// se não conseguir, sai com 1 sem tocar na pasta de dados.
func TestAdminOwnerBeforeWriting(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	vault := credentials.Vault{Dir: filepath.Join(te.dir, "credentials"), DPAPI: fake.DPAPI{}}
	securer := &fake.ACL{}
	te.securer = securer
	var calls []string
	te.adminOwner = func() error {
		calls = append(calls, fmt.Sprintf("dono (acl=%d)", len(securer.Dirs)))
		return errors.New("ERROR_INVALID_OWNER")
	}

	te.stdin = strings.NewReader("pw\n")
	if code := te.run("credential", "set", "Matriz", "--user", "a", "--password-stdin"); code != 1 {
		t.Fatalf("falha no dono: código %d", code)
	}
	if !strings.Contains(te.errb.String(), "nada foi gravado") || !strings.Contains(te.errb.String(), "ERROR_INVALID_OWNER") {
		t.Fatalf("mensagem: %q", te.errb)
	}
	if vault.Has("Matriz") || len(securer.Dirs) != 0 {
		t.Fatalf("não deveria gravar nem endurecer: cofre=%v acl=%v", vault.Has("Matriz"), securer.Dirs)
	}
	te.platform = func() (Platform, error) { t.Fatal("run não deveria montar a plataforma"); return Platform{}, nil }
	if code := te.run("run"); code != 1 {
		t.Fatalf("run com falha no dono: código %d", code)
	}
	if _, err := os.Stat(filepath.Join(te.dir, "logs")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("run não deveria criar logs: %v", err)
	}

	// Sucesso: chamado uma vez por comando elevado, antes do endurecimento.
	calls = nil
	te.adminOwner = func() error {
		calls = append(calls, fmt.Sprintf("dono (acl=%d)", len(securer.Dirs)))
		return nil
	}
	te.stdin = strings.NewReader("pw\n")
	if code := te.run("credential", "set", "Matriz", "--user", "a", "--password-stdin"); code != 0 || !vault.Has("Matriz") {
		t.Fatalf("set: %d %q", code, te.errb)
	}
	if len(calls) != 1 || calls[0] != "dono (acl=0)" || len(securer.Dirs) != 1 {
		t.Fatalf("ordem: %v acl=%v", calls, securer.Dirs)
	}

	// Sem elevação, version e uso inválido não mexem no token.
	calls = nil
	te.run("version")
	te.run("formatar")
	te.elevated = func() bool { return false }
	te.run("status")
	if len(calls) != 0 {
		t.Fatalf("não deveria ajustar o token: %v", calls)
	}
}
