package main

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/acl"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/fake"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/svc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/credentials"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/service"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// testService é o serviço inteiro rodando no Linux com plataforma falsa e o
// pipe trocado por TCP local.
type testService struct {
	o      *service.Orchestrator
	ras    *fake.RAS
	events *logging.RecordingSink
	acl    *fake.ACL
	cancel context.CancelFunc
	done   chan error
}

// fakeACL devolve a fábrica de Platform.ACL para um fake.ACL.
func fakeACL(a acl.Securer) func(acl.Logf) acl.Securer {
	return func(acl.Logf) acl.Securer { return a }
}

// testPlatform monta a plataforma falsa com o seed da VPN Matriz.
func testPlatform(r *fake.RAS, events *logging.RecordingSink, a acl.Securer) Platform {
	pinger := fake.NewPinger()
	pinger.SetReachable("10.0.0.1", true)
	return Platform{
		RAS: r, Pinger: pinger, Net: fake.NewNet(), DPAPI: fake.DPAPI{}, ACL: fakeACL(a),
		Events: events,
		ReadSeed: func() (config.Seed, bool, error) {
			return config.Seed{VPNEntry: "VPN Matriz", VPNName: "Matriz", CheckHost: "10.0.0.1"}, true, nil
		},
	}
}

func startService(t *testing.T, te *testEnv) *testService {
	t.Helper()
	return startServiceRAS(t, te, fake.NewRAS("VPN Matriz", "VPN Filial"))
}

// startServiceRAS sobe o serviço com um RAS falso já roteirizado.
func startServiceRAS(t *testing.T, te *testEnv, r *fake.RAS) *testService {
	t.Helper()
	ts := &testService{ras: r, events: &logging.RecordingSink{}, acl: &fake.ACL{}, done: make(chan error, 1)}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := testPlatform(ts.ras, ts.events, ts.acl)
	p.Listen = func() (net.Listener, error) { return ln, nil }
	p.ReadFile = te.readFile
	te.platform = func() (Platform, error) { return p, nil }
	te.dial = func(ctx context.Context) (*ipc.Client, error) {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return nil, err
		}
		return ipc.Handshake(c, "teste")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ts.cancel = cancel
	ready := make(chan *service.Orchestrator, 1)
	go func() {
		ts.done <- serve(ctx, p, newLayout(te.dir), shared.RealClock{}, func(o *service.Orchestrator) { ready <- o })
	}()
	select {
	case ts.o = <-ready:
	case err := <-ts.done:
		t.Fatalf("serviço não subiu: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-ts.done:
			ts.done <- err
		case <-time.After(10 * time.Second):
		}
	})
	return ts
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condição não atingida em 5 s")
}

// stateVia consulta o estado da primeira VPN pelo pipe (TCP no teste).
func stateVia(te *testEnv) string {
	var snap ipc.Snapshot
	c, err := te.dial(context.Background())
	if err != nil {
		return ""
	}
	defer c.Close()
	if err := c.Call(ipc.TypeStatus, nil, &snap); err != nil || len(snap.VPNs) == 0 {
		return ""
	}
	return snap.VPNs[0].State
}

func hasEvent(ev []logging.RecordedEvent, level, substr string) bool {
	for _, e := range ev {
		if e.Level == level && strings.Contains(e.Msg, substr) {
			return true
		}
	}
	return false
}

func TestServeStartsAndStops(t *testing.T) {
	te := newTestEnv(t)
	ts := startService(t, te)

	// O seed gerou a config e a VPN conectou (discagem do fake).
	waitFor(t, func() bool { return stateVia(te) == "Conectada" })
	if c, err := config.Load(filepath.Join(te.dir, "config.json")); err != nil || c.VPNs[0].Name != "Matriz" {
		t.Fatalf("seed: %+v %v", c, err)
	}
	if len(ts.acl.Dirs) != 1 || ts.acl.Dirs[0] != te.dir {
		t.Fatalf("ACL da pasta de dados: %v", ts.acl.Dirs)
	}
	// A config gerada pelo seed é a do próprio serviço: mudar pelo pipe vale.
	if err := ts.o.SetEnabled("Matriz", true); err != nil {
		t.Fatalf("mudança após o seed: %v", err)
	}

	// Edição manual do config.json é recarregada.
	c := config.Empty()
	c.VPNs = []config.VPN{
		config.RawVPN{Name: "Matriz", RasEntry: "VPN Matriz", Check: &config.RawCheck{Kind: config.CheckLink}}.Normalize(),
		config.RawVPN{Name: "Filial", RasEntry: "VPN Filial", Check: &config.RawCheck{Kind: config.CheckLink}}.Normalize(),
	}
	data, _ := config.Marshal(c)
	_ = os.WriteFile(filepath.Join(te.dir, "config.json"), data, 0o600)
	waitFor(t, func() bool { return len(ts.o.Status().VPNs) == 2 })

	start := time.Now()
	ts.cancel()
	if err := <-ts.done; err != nil {
		t.Fatal(err)
	}
	ts.done <- nil
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("parada levou %s (máx. 10 s)", d)
	}
	if _, err := os.Stat(filepath.Join(te.dir, "state.json")); err != nil {
		t.Fatal("state.json deveria ser gravado na parada")
	}
	ev := ts.events.Snapshot()
	if len(ev) < 2 || !strings.Contains(ev[0].Msg, "iniciado") || !strings.Contains(ev[len(ev)-1].Msg, "parado") {
		t.Fatalf("Event Log: %+v", ev)
	}
	if !ts.ras.IsActive("VPN Matriz") {
		t.Fatal("parar o serviço não pode derrubar VPN conectada")
	}
	// Só a discagem da Filial ainda em curso pode ter sido desligada.
	for _, call := range ts.ras.Calls() {
		if call == "HangUp VPN Matriz" {
			t.Fatalf("parada desligou uma VPN: %v", ts.ras.Calls())
		}
	}
}

// Config já existente também é marcada como a do serviço (MarkWritten em
// toda partida, não só no seed): mudar pelo pipe funciona de cara.
func TestServeExistingConfigAcceptsPipeChanges(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	ts := startService(t, te)
	if err := ts.o.AddVPN(config.RawVPN{Name: "Filial", RasEntry: "VPN Filial", Check: &config.RawCheck{Kind: config.CheckLink}}); err != nil {
		t.Fatalf("mudança pelo pipe com config existente: %v", err)
	}
	if c, err := config.Load(filepath.Join(te.dir, "config.json")); err != nil || len(c.VPNs) != 2 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestServeSurvivesInvalidConfig(t *testing.T) {
	te := newTestEnv(t)
	_ = os.WriteFile(filepath.Join(te.dir, "config.json"), []byte(`{"version":9}`), 0o600)
	ts := startService(t, te)
	if n := len(ts.o.Status().VPNs); n != 0 {
		t.Fatalf("config inválida: sobe sem VPNs, veio %d", n)
	}
	if b, _ := os.ReadFile(filepath.Join(te.dir, "config.json")); string(b) != `{"version":9}` {
		t.Fatal("arquivo inválido não pode ser sobrescrito")
	}
	if ev := ts.events.Snapshot(); len(ev) == 0 || ev[0].Level != "error" {
		t.Fatalf("Event Log: %+v", ev)
	}
	err := ts.o.AddVPN(config.RawVPN{Name: "Nova", RasEntry: "VPN Filial", Check: &config.RawCheck{Kind: config.CheckLink}})
	if err == nil || !strings.Contains(err.Error(), "corrija o arquivo") {
		t.Fatalf("vpn add com config.json inválido deve ser recusado: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(te.dir, "config.json")); string(b) != `{"version":9}` {
		t.Fatal("arquivo inválido não pode ser sobrescrito por vpn add")
	}
	// Corrigido à mão, é recarregado.
	te.writeConfig(t, "Matriz")
	waitFor(t, func() bool { return len(ts.o.Status().VPNs) == 1 })
}

func TestServeExitsCleanlyWhenPipeIsTaken(t *testing.T) {
	te := newTestEnv(t)
	rasFake := fake.NewRAS("VPN Matriz")
	_ = os.WriteFile(filepath.Join(te.dir, "state.json"), []byte(`{"pauses":{"matriz":{"indefinite":true}}}`), 0o600)
	a := &fake.ACL{}
	events := &logging.RecordingSink{}
	p := testPlatform(rasFake, events, a)
	p.Listen = func() (net.Listener, error) { return nil, errors.New("Access is denied") }
	err := serve(context.Background(), p, newLayout(te.dir), shared.RealClock{}, nil)
	if err == nil || !strings.Contains(err.Error(), "outro VPN Monitor") {
		t.Fatalf("serve deve falhar cedo: %v", err)
	}
	if len(rasFake.Calls()) != 0 {
		t.Fatalf("segunda instância não pode discar: %v", rasFake.Calls())
	}
	if b, _ := os.ReadFile(filepath.Join(te.dir, "state.json")); string(b) != `{"pauses":{"matriz":{"indefinite":true}}}` {
		t.Fatalf("segunda instância não pode regravar state.json: %s", b)
	}
	if _, err := os.Stat(filepath.Join(te.dir, "config.json")); err == nil {
		t.Fatal("segunda instância não pode criar config.json")
	}
	// Nem mexer na pasta da instância que roda (ACL, quarentena, log).
	if len(a.Dirs) != 0 {
		t.Fatalf("segunda instância não pode endurecer a pasta: %v", a.Dirs)
	}
	if _, err := os.Stat(filepath.Join(te.dir, "logs")); err == nil {
		t.Fatal("segunda instância não pode abrir o log")
	}
	if ev := events.Snapshot(); len(ev) != 1 || ev[0].Level != "error" {
		t.Fatalf("Event Log: %+v", ev)
	}
}

// quarantineACL simula a pasta posta de lado e recriada, com uma linha de log.
type quarantineACL struct {
	logf acl.Logf
	err  error
}

func (q *quarantineACL) EnsureDir(string) (bool, error) {
	q.logf("pasta de dados não confiável movida de %s para %s: %s", "a", "b", "dono estranho")
	return q.err != nil, q.err
}

func TestServeQuarantineWarnsAndContinues(t *testing.T) {
	te := newTestEnv(t)
	q := &quarantineACL{err: acl.ErrQuarantined}
	events := &logging.RecordingSink{}
	p := testPlatform(fake.NewRAS("VPN Matriz"), events, nil)
	p.ACL = func(l acl.Logf) acl.Securer { q.logf = l; return q }
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p.Listen = func() (net.Listener, error) { return ln, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		done <- serve(ctx, p, newLayout(te.dir), shared.RealClock{}, func(*service.Orchestrator) { close(ready) })
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("quarentena não pode impedir a subida: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	ev := events.Snapshot()
	if !hasEvent(ev, "warning", acl.ErrQuarantined.Error()) || !hasEvent(ev, "warning", "dono estranho") {
		t.Fatalf("Event Log: %+v", ev)
	}
	log, _ := os.ReadFile(filepath.Join(te.dir, "logs", "vpnmon.log"))
	if !strings.Contains(string(log), "dono estranho") || !strings.Contains(string(log), acl.ErrQuarantined.Error()) {
		t.Fatalf("log: %s", log)
	}
}

func TestServeACLFailureAborts(t *testing.T) {
	te := newTestEnv(t)
	q := &quarantineACL{err: errors.New("acesso negado")}
	events := &logging.RecordingSink{}
	r := fake.NewRAS("VPN Matriz")
	p := testPlatform(r, events, nil)
	p.ACL = func(l acl.Logf) acl.Securer { q.logf = l; return q }
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p.Listen = func() (net.Listener, error) { return ln, nil }
	err = serve(context.Background(), p, newLayout(te.dir), shared.RealClock{}, nil)
	if err == nil || !strings.Contains(err.Error(), "acesso negado") {
		t.Fatalf("falha de ACL deve impedir a subida: %v", err)
	}
	if len(r.Calls()) != 0 {
		t.Fatal("sem pasta segura não disca")
	}
	if _, err := os.Stat(filepath.Join(te.dir, "config.json")); err == nil {
		t.Fatal("sem pasta segura não grava config.json")
	}
	if !hasEvent(events.Snapshot(), "error", "acesso negado") {
		t.Fatalf("Event Log: %+v", events.Snapshot())
	}
	if _, err := ln.Accept(); err == nil {
		t.Fatal("o pipe deveria ser fechado na falha")
	}
}

// Listener morto (fechado sem parada pedida): o serviço para de forma
// ordenada e devolve erro, para o SCM reiniciá-lo. Recriar o pipe deixaria o
// nome livre para outro processo (squatting).
func TestServeStopsWithErrorWhenPipeDies(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	r := fake.NewRAS("VPN Matriz")
	events := &logging.RecordingSink{}
	p := testPlatform(r, events, &fake.ACL{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listens := 0
	p.Listen = func() (net.Listener, error) { listens++; return ln, nil }
	te.dial = func(context.Context) (*ipc.Client, error) {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return nil, err
		}
		return ipc.Handshake(c, "teste")
	}
	done := make(chan error, 1)
	go func() { done <- serve(context.Background(), p, newLayout(te.dir), shared.RealClock{}, nil) }()
	waitFor(t, func() bool { return stateVia(te) == "Conectada" })
	ln.Close()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "pipe") {
			t.Fatalf("pipe morto deve encerrar com erro: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve não parou com o pipe morto")
	}
	if listens != 1 {
		t.Fatalf("o pipe não pode ser recriado: %d", listens)
	}
	if _, err := os.Stat(filepath.Join(te.dir, "state.json")); err != nil {
		t.Fatal("state.json deveria ser gravado")
	}
	if !r.IsActive("VPN Matriz") || slices.Contains(r.Calls(), "HangUp VPN Matriz") {
		t.Fatalf("parada não pode derrubar VPN: %v", r.Calls())
	}
	if !hasEvent(events.Snapshot(), "error", "pipe") {
		t.Fatalf("Event Log: %+v", events.Snapshot())
	}
}

// Parada pedida enquanto a partida ainda repete a leitura do config.json:
// sai limpo, sem o falso "config.json inválido" e sem subir o orquestrador.
func TestServeCancelledDuringStartupConfigRead(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	path := filepath.Join(te.dir, "config.json")
	r := fake.NewRAS("VPN Matriz")
	events := &logging.RecordingSink{}
	p := testPlatform(r, events, &fake.ACL{})
	var unreadable atomic.Bool
	unreadable.Store(true)
	p.ReadFile = sharingViolation(path, &unreadable)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p.Listen = func() (net.Listener, error) { return ln, nil }
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	readyCalled := false
	start := time.Now()
	err = serve(ctx, p, newLayout(te.dir), shared.RealClock{}, func(*service.Orchestrator) { readyCalled = true })
	if err != nil || readyCalled || time.Since(start) > 2*time.Second {
		t.Fatalf("err=%v ready=%v em %s", err, readyCalled, time.Since(start))
	}
	if len(r.Calls()) != 0 {
		t.Fatalf("não pode discar: %v", r.Calls())
	}
	for _, e := range events.Snapshot() {
		if e.Level == "error" {
			t.Fatalf("falso erro no Event Log: %+v", e)
		}
	}
	if _, err := os.Stat(filepath.Join(te.dir, "state.json")); err == nil {
		t.Fatal("sem orquestrador, state.json não é gravado")
	}
}

// config.json apagado: aviso específico na hora, sem as novas tentativas.
func TestServeConfigRemovedWarnsImmediately(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	ts := startService(t, te)
	time.Sleep(500 * time.Millisecond) // linha de base do observador
	_ = os.Remove(filepath.Join(te.dir, "config.json"))
	waitFor(t, func() bool { return hasEvent(ts.events.Snapshot(), "warning", "config.json removido") })
	b, _ := os.ReadFile(filepath.Join(te.dir, "logs", "vpnmon.log"))
	if strings.Contains(string(b), "nova tentativa") {
		t.Fatalf("arquivo removido não deve ser repetido: %s", b)
	}
	if n := len(ts.o.Status().VPNs); n != 1 {
		t.Fatalf("configuração em uso deve ser mantida: %d", n)
	}
}

// sharingViolation simula, de forma portátil, o arquivo segurado por outro
// processo (no Windows, chmod não o torna ilegível): enquanto on, ler path
// falha com um erro que não é "não existe".
func sharingViolation(path string, on *atomic.Bool) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		if on.Load() && filepath.Clean(name) == filepath.Clean(path) {
			return nil, &fs.PathError{Op: "open", Path: name, Err: errors.New("violação de compartilhamento (simulada)")}
		}
		return os.ReadFile(name)
	}
}

func TestServeRetriesUnreadableConfig(t *testing.T) {
	old := configRetryDelays
	configRetryDelays = []time.Duration{200 * time.Millisecond, 200 * time.Millisecond, 200 * time.Millisecond, 200 * time.Millisecond}
	t.Cleanup(func() { configRetryDelays = old })
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	path := filepath.Join(te.dir, "config.json")
	var unreadable atomic.Bool
	te.readFile = sharingViolation(path, &unreadable)
	ts := startService(t, te)
	logHas := func(s string) bool {
		b, _ := os.ReadFile(filepath.Join(te.dir, "logs", "vpnmon.log"))
		return strings.Contains(string(b), s)
	}
	time.Sleep(500 * time.Millisecond) // linha de base do observador já tomada

	// Ilegível por pouco tempo (violação de compartilhamento): a nova
	// tentativa recarrega sem aviso.
	c := config.Empty()
	c.VPNs = []config.VPN{
		config.RawVPN{Name: "Matriz", RasEntry: "VPN Matriz", Check: &config.RawCheck{Kind: config.CheckLink}}.Normalize(),
		config.RawVPN{Name: "Filial", RasEntry: "VPN Filial", Check: &config.RawCheck{Kind: config.CheckLink}}.Normalize(),
	}
	data, _ := config.Marshal(c)
	_ = os.WriteFile(path, data, 0o600)
	unreadable.Store(true)
	waitFor(t, func() bool { return logHas("nova tentativa") })
	unreadable.Store(false)
	waitFor(t, func() bool { return len(ts.o.Status().VPNs) == 2 })
	if hasEvent(ts.events.Snapshot(), "warning", "não foi possível ler config.json") {
		t.Fatalf("falha passageira não vai ao Event Log: %+v", ts.events.Snapshot())
	}

	// Ilegível de vez: Event Log e configStatus, config anterior mantida.
	time.Sleep(500 * time.Millisecond) // o observador amostra o arquivo de novo legível
	data, _ = config.Marshal(config.Empty())
	_ = os.WriteFile(path, data, 0o600)
	unreadable.Store(true)
	waitFor(t, func() bool { return hasEvent(ts.events.Snapshot(), "warning", "não foi possível ler config.json") })
	if n := len(ts.o.Status().VPNs); n != 2 {
		t.Fatalf("config anterior deveria valer: %d VPNs", n)
	}
}

// Credencial gravada no cofre com o serviço rodando destrava a VPN bloqueada.
func TestServeWatchesCredentials(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	r := fake.NewRAS("VPN Matriz")
	r.Script("VPN Matriz", fake.DialOutcome{Code: ras.ERROR_AUTHENTICATION_FAILURE})
	startServiceRAS(t, te, r)
	waitFor(t, func() bool { return stateVia(te) == "CredencialInvalida" })
	vault := credentials.Vault{Dir: filepath.Join(te.dir, "credentials"), DPAPI: fake.DPAPI{}}
	// Temporários ".*" são ignorados; o arquivo final avisa o orquestrador.
	_ = os.WriteFile(filepath.Join(vault.Dir, ".Matriz.bin.tmp-1"), []byte("x"), 0o600)
	if err := vault.Set("Matriz", "ana", shared.NewSecret("s3gredo-unico")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return stateVia(te) == "Conectada" })
}

// Nada de segredo no log nem no Event Log, mesmo em debug.
func TestServeNeverLogsSecrets(t *testing.T) {
	te := newTestEnv(t)
	c := config.Empty()
	c.LogLevel = "debug"
	c.VPNs = []config.VPN{config.RawVPN{Name: "Matriz", RasEntry: "VPN Matriz", Check: &config.RawCheck{Kind: config.CheckLink}}.Normalize()}
	if _, err := config.Save(filepath.Join(te.dir, "config.json"), c); err != nil {
		t.Fatal(err)
	}
	vault := credentials.Vault{Dir: filepath.Join(te.dir, "credentials"), DPAPI: fake.DPAPI{}}
	if err := vault.Set("Matriz", "ana", shared.NewSecret("s3gredo-unico")); err != nil {
		t.Fatal(err)
	}
	ts := startService(t, te)
	waitFor(t, func() bool { return stateVia(te) == "Conectada" })
	ts.cancel()
	<-ts.done
	ts.done <- nil
	b, _ := os.ReadFile(filepath.Join(te.dir, "logs", "vpnmon.log"))
	if len(b) == 0 || strings.Contains(string(b), "s3gredo-unico") {
		t.Fatalf("log vazio ou com segredo: %s", b)
	}
	for _, e := range ts.events.Snapshot() {
		if strings.Contains(e.Msg, "s3gredo-unico") {
			t.Fatalf("segredo no Event Log: %+v", e)
		}
	}
}

func TestNoArgsAsServiceRunsService(t *testing.T) {
	te := newTestEnv(t)
	te.isService = func() (bool, error) { return true, nil }
	called := false
	te.runService = func(h svc.Hooks) error { called = h.Run != nil && h.OnResume != nil; return nil }
	if code := te.run(); code != 0 || !called {
		t.Fatalf("%d %v", code, called)
	}
}

// O modo serviço sob o laço do SCM: retomada de energia (duas vezes, como o
// Windows manda) chega ao orquestrador sem travar o laço; parada pedida sai 0.
func TestServiceMainUnderSCMLoop(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	events := &logging.RecordingSink{}
	p := testPlatform(fake.NewRAS("VPN Matriz"), events, &fake.ACL{})
	p.Listen = func() (net.Listener, error) { return ln, nil }
	te.platform = func() (Platform, error) { return p, nil }
	te.isService = func() (bool, error) { return true, nil }
	var code uint32
	te.runService = func(h svc.Hooks) error {
		if h.StopTimeout != svc.DefaultStopTimeout {
			t.Errorf("StopTimeout %s", h.StopTimeout)
		}
		reqs := make(chan svc.Request)
		finished := make(chan uint32, 1)
		go func() { finished <- svc.Loop(h, reqs, func(svc.State) {}) }()
		waitFor(t, func() bool { return hasEvent(events.Snapshot(), "info", "iniciado") })
		reqs <- svc.Request{Cmd: svc.CmdPowerEvent, EventType: svc.PBT_APMRESUMEAUTOMATIC}
		reqs <- svc.Request{Cmd: svc.CmdPowerEvent, EventType: svc.PBT_APMRESUMESUSPEND}
		waitFor(t, func() bool {
			b, _ := os.ReadFile(filepath.Join(te.dir, "logs", "vpnmon.log"))
			return strings.Contains(string(b), "retomada de energia detectada")
		})
		reqs <- svc.Request{Cmd: svc.CmdStop}
		code = <-finished
		return nil
	}
	if rc := te.run(); rc != 0 || code != 0 {
		t.Fatalf("saída %d, laço %d", rc, code)
	}
	if !hasEvent(events.Snapshot(), "info", "parado") {
		t.Fatalf("Event Log: %+v", events.Snapshot())
	}
}

// Erro do próprio Run (aqui, o pipe ocupado) sai ≠ 0 para a recuperação do SCM agir.
func TestServiceMainRunErrorExitsNonZero(t *testing.T) {
	te := newTestEnv(t)
	p := testPlatform(fake.NewRAS("VPN Matriz"), &logging.RecordingSink{}, &fake.ACL{})
	p.Listen = func() (net.Listener, error) { return nil, errors.New("Access is denied") }
	te.platform = func() (Platform, error) { return p, nil }
	te.isService = func() (bool, error) { return true, nil }
	var code uint32
	te.runService = func(h svc.Hooks) error {
		h.OnResume() // antes de subir: não pode entrar em pânico
		code = svc.Loop(h, make(chan svc.Request), func(svc.State) {})
		return nil
	}
	te.run()
	if code == 0 {
		t.Fatal("erro do Run deveria sair ≠ 0")
	}
}

// `run` usa a mesma montagem e termina com Ctrl+C. O Ctrl+C é injetado: no
// Windows um processo não consegue mandar os.Interrupt a si mesmo.
func TestCmdRunStopsOnInterrupt(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	events := &logging.RecordingSink{}
	p := testPlatform(fake.NewRAS("VPN Matriz"), events, &fake.ACL{})
	p.Listen = func() (net.Listener, error) { return ln, nil }
	te.platform = func() (Platform, error) { return p, nil }
	ctrlC := make(chan struct{})
	te.interrupt = func(parent context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		go func() {
			select {
			case <-ctrlC:
				cancel()
			case <-ctx.Done():
			}
		}()
		return ctx, cancel
	}
	done := make(chan int, 1)
	go func() { done <- runCLI([]string{"run"}, te.env) }()
	var once sync.Once
	press := func() { once.Do(func() { close(ctrlC) }) }
	// Mesmo se o teste falhar no meio, o run para (e fecha o log) antes de o
	// TempDir ser apagado: no Windows o log aberto impede a remoção.
	t.Cleanup(func() {
		press()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("run não terminou na limpeza")
		}
	})
	waitFor(t, func() bool { return hasEvent(events.Snapshot(), "info", "iniciado") })
	press()
	var code int
	select {
	case code = <-done:
		done <- code // a limpeza também espera por ele
	case <-time.After(10 * time.Second):
		t.Fatal("run não terminou com Ctrl+C")
	}
	if code != 0 {
		t.Fatalf("código %d: %s", code, te.errb)
	}
	if !strings.Contains(te.out.String(), "Ctrl+C") || !hasEvent(events.Snapshot(), "info", "parado") {
		t.Fatalf("%q %+v", te.out, events.Snapshot())
	}
	// O run fechou o log ao sair: dá para apagá-lo (no Windows, aberto falharia).
	if err := os.Remove(filepath.Join(te.dir, "logs", "vpnmon.log")); err != nil {
		t.Fatalf("log ainda aberto ou ausente: %v", err)
	}
}
