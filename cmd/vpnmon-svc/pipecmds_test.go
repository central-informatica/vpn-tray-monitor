package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/fake"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/svc"
)

func TestCLIAgainstRunningService(t *testing.T) {
	te := newTestEnv(t)
	startService(t, te)
	status := func() string { te.run("status"); return te.out.String() }
	waitFor(t, func() bool { return strings.Contains(status(), "Conectada") })

	if code := te.run("vpn", "add", "--name", "Filial", "--entry", "VPN Filial", "--check", "link"); code != 0 {
		t.Fatalf("vpn add: %q", te.errb)
	}
	te.run("vpn", "list")
	if !strings.Contains(te.out.String(), "Filial") || !strings.Contains(te.out.String(), "ping 10.0.0.1") {
		t.Fatalf("vpn list: %q", te.out)
	}
	if code := te.run("vpn", "add", "--name", "X", "--entry", "E", "--check", "tcp", "--host", "h"); code != 1 || !strings.Contains(te.errb.String(), "--port") {
		t.Fatalf("erro por campo: %d %q", code, te.errb)
	}
	waitFor(t, func() bool { return strings.Count(status(), "Conectada") == 2 })
	if code := te.run("vpn", "remove", "Filial"); code != 0 {
		t.Fatalf("vpn remove: %q", te.errb)
	}
	if code := te.run("vpn", "remove", "Filial"); code != 1 || !strings.Contains(te.errb.String(), "não existe") {
		t.Fatalf("remover de novo: %d %q", code, te.errb)
	}
	// A mensagem do servidor chega ao usuário sem o código técnico.
	if strings.Contains(te.errb.String(), ipc.CodeNotFound) {
		t.Fatalf("código técnico na saída: %q", te.errb)
	}
}

func TestStatusWithoutServiceFailsClearly(t *testing.T) {
	te := newTestEnv(t)
	if code := te.run("status"); code != 1 || !strings.Contains(te.errb.String(), "inacessível") {
		t.Fatalf("%d %q", code, te.errb)
	}
}

func TestParseVPNAdd(t *testing.T) {
	raw, err := parseVPNAdd([]string{"--name", "Matriz", "--entry", "VPN Matriz", "--host", "10.0.0.1", "--interval", "20"})
	if err != nil {
		t.Fatal(err)
	}
	v := raw.Normalize()
	if v.Check.Kind != config.CheckPing || v.IntervalSeconds != 20 || v.FailuresBeforeReconnect != 3 {
		t.Fatalf("%+v", v)
	}
	raw, _ = parseVPNAdd([]string{"--name", "B", "--entry", "E", "--disabled"})
	if v := raw.Normalize(); v.Check.Kind != config.CheckLink || v.Enabled {
		t.Fatalf("sem host vira link; --disabled: %+v", v)
	}
	if _, err := parseVPNAdd([]string{"--name", "B"}); err == nil {
		t.Fatal("--entry é obrigatório")
	}
	// Omitidos ficam nil: o serviço aplica os padrões da §5.2.
	raw, _ = parseVPNAdd([]string{"--name", "C", "--entry", "E", "--check", "tcp", "--host", "h", "--port", "443"})
	if raw.Enabled != nil || raw.IntervalSeconds != nil || raw.Check.TimeoutSeconds != nil || raw.Check.Port != 443 {
		t.Fatalf("omitidos devem ficar nil: %+v %+v", raw, raw.Check)
	}
	if v := raw.Normalize(); v.Enabled != config.DefaultVPNEnabled || v.IntervalSeconds != config.DefaultInterval {
		t.Fatalf("padrões: %+v", v)
	}
}

func TestFormatStatus(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	snap := ipc.Snapshot{VPNs: []ipc.VPNView{
		{Name: "Matriz", State: "Conectada", CheckKind: "ping", SinceUnix: now.Add(-3 * time.Hour).Unix(),
			LastCheckUnix: now.Add(-10 * time.Second).Unix(), LatencyMs: 12, Reconnects24h: 2},
		{Name: "Filial", State: "Reconectando", Attempt: 3, NextAttemptUnix: now.Add(40 * time.Second).Unix(),
			LastError: &ipc.ErrorInfo{Code: 809, Message: "sem resposta"}},
		{Name: "Backup", State: "Pausada", PausedUntilUnix: time.Date(2026, 10, 7, 15, 30, 0, 0, time.Local).Unix()},
	}}
	out := formatStatus(snap, now)
	for _, want := range []string{"há 3 h", "ping 12ms", "2 reconexão(ões) em 24 h", "tentativa 3", "próxima em 40 s", "erro 809: sem resposta", "até 15:30"} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %q em:\n%s", want, out)
		}
	}
	if !strings.Contains(formatStatus(ipc.Snapshot{}, now), "nenhuma VPN") {
		t.Error("vazio")
	}
}

// "próxima em" nunca negativo: prazo vencido (relógio do cliente à frente do
// serviço) é omitido.
func TestFormatStatusNextAttemptNeverNegative(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	snap := ipc.Snapshot{VPNs: []ipc.VPNView{{Name: "Filial", State: "Reconectando", Attempt: 2,
		NextAttemptUnix: now.Add(-30 * time.Second).Unix()}}}
	out := formatStatus(snap, now)
	if strings.Contains(out, "próxima em") || strings.Contains(out, "-") {
		t.Fatalf("prazo vencido não deve aparecer: %s", out)
	}
	snap.VPNs[0].NextAttemptUnix = now.Unix()
	if out := formatStatus(snap, now); strings.Contains(out, "próxima em") {
		t.Fatalf("prazo no instante atual não deve aparecer: %s", out)
	}
}

func TestExplainShowsServerMessage(t *testing.T) {
	msg := "config.json foi alterado no disco; aguarde a recarga e tente de novo"
	err := explain(&ipc.Error{Code: ipc.CodeInvalidConfig, Message: msg})
	if err == nil || err.Error() != msg {
		t.Fatalf("mensagem do servidor: %v", err)
	}
	err = explain(&ipc.Error{Code: ipc.CodeInvalidConfig, Message: "x",
		Fields: []config.FieldError{{Field: "check.port", Message: "obrigatória"}, {Field: "outro", Message: "m"}}})
	if err == nil || err.Error() != "--port: obrigatória; outro: m" {
		t.Fatalf("por campo: %v", err)
	}
	// Sem mensagem, o código aparece (nunca "erro: " vazio).
	if err := explain(&ipc.Error{Code: ipc.CodeBusy}); err == nil || err.Error() != ipc.CodeBusy {
		t.Fatalf("sem mensagem: %v", err)
	}
	plain := errors.New("serviço VPN Monitor inacessível")
	if explain(plain) != plain {
		t.Fatal("erro comum passa intacto")
	}
	// Pela CLI: código 1 e só a mensagem.
	te := newTestEnv(t)
	te.dial = func(context.Context) (*ipc.Client, error) {
		return nil, &ipc.Error{Code: ipc.CodeBusy, Message: "serviço ocupado"}
	}
	if code := te.run("status"); code != 1 || strings.TrimSpace(te.errb.String()) != "erro: serviço ocupado" {
		t.Fatalf("%d %q", code, te.errb)
	}
}

func TestCheckLocalDoesNotDial(t *testing.T) {
	te := newTestEnv(t)
	c := config.Empty()
	c.VPNs = []config.VPN{
		config.RawVPN{Name: "Matriz", RasEntry: "VPN Matriz", Check: &config.RawCheck{Kind: config.CheckPing, Host: "10.0.0.1"}}.Normalize(),
		config.RawVPN{Name: "Filial", RasEntry: "VPN Filial", Check: &config.RawCheck{Kind: config.CheckLink}}.Normalize(),
	}
	if _, err := config.Save(filepath.Join(te.dir, "config.json"), c); err != nil {
		t.Fatal(err)
	}
	r := fake.NewRAS("VPN Matriz", "VPN Filial")
	pinger := fake.NewPinger()
	nw := fake.NewNet()
	te.platform = func() (Platform, error) { return Platform{RAS: r, Pinger: pinger, Net: nw}, nil }

	// Saída 0 só com tudo OK; enlace caído, sem rede ou alvo falhando = 1,
	// com o diagnóstico na saída padrão e nada de "erro:" extra.
	if code := te.run("check", "matriz"); code != 1 || !strings.Contains(te.out.String(), "enlace caído") || te.errb.Len() != 0 {
		t.Fatalf("caído: %d %q %q", code, te.out, te.errb)
	}
	nw.SetPhysical(false)
	if code := te.run("check", "Matriz"); code != 1 || !strings.Contains(te.out.String(), "sem rede com rota padrão") {
		t.Fatalf("sem rede: %d %q", code, te.out)
	}
	// Outra VPN da config discada (PPP) não é rede; um PPPoE é.
	nw.SetPPP("vpn filial")
	if code := te.run("check", "Matriz"); code != 1 || !strings.Contains(te.out.String(), "sem rede com rota padrão") {
		t.Fatalf("VPN monitorada contou como rede: %d %q", code, te.out)
	}
	nw.SetPPP("Banda Larga")
	if code := te.run("check", "Matriz"); code != 1 || !strings.Contains(te.out.String(), "enlace caído") {
		t.Fatalf("PPPoE deve contar como rede: %d %q", code, te.out)
	}
	nw.SetPPP()
	nw.SetPhysical(true)
	r.SetActive("VPN Matriz")
	if code := te.run("check", "Matriz"); code != 1 || !strings.Contains(te.out.String(), "sem resposta") {
		t.Fatalf("sem eco: %d %q", code, te.out)
	}
	pinger.SetReachable("10.0.0.1", true)
	if code := te.run("check", "Matriz"); code != 0 || !strings.Contains(te.out.String(), "respondeu em 12ms") {
		t.Fatalf("eco: %d %q", code, te.out)
	}
	// Mesmo arredondamento do serviço: um eco abaixo de 1 ms não vira "0ms".
	pinger.RTT = 300 * time.Microsecond
	if code := te.run("check", "Matriz"); code != 0 || !strings.Contains(te.out.String(), "respondeu em 1ms") {
		t.Fatalf("eco rápido: %d %q", code, te.out)
	}
	r.SetActive("VPN Filial")
	if code := te.run("check", "Filial"); code != 0 || !strings.Contains(te.out.String(), "verificação link") {
		t.Fatalf("link: %d %q", code, te.out)
	}
	// Verificador que falha (o ping nem pôde ser feito).
	r.SetActive("VPN Matriz")
	pinger.SetError("10.0.0.1", errors.New("IcmpSendEcho2: acesso negado"))
	if code := te.run("check", "Matriz"); code != 1 || !strings.Contains(te.out.String(), "falhou") {
		t.Fatalf("verificador com erro: %d %q %q", code, te.out, te.errb)
	}
	if !strings.Contains(usage, "check <vpn>") || !strings.Contains(usage, "sai com 1") {
		t.Fatalf("usage deve documentar o código de saída do check:\n%s", usage)
	}
	if code := te.run("check", "Nenhuma"); code != 1 || !strings.Contains(te.errb.String(), "não existe") {
		t.Fatalf("inexistente: %d %q", code, te.errb)
	}
	if code := te.run("check"); code != 2 {
		t.Fatalf("sem argumento: %d", code)
	}
	for _, call := range r.Calls() {
		if strings.HasPrefix(call, "StartDial") {
			t.Fatalf("check discou: %v", r.Calls())
		}
	}
}

// Cada comando conhecido passa pela checagem de elevação (é reconhecido) e
// é despachado; a lista vive num só lugar.
func TestCommandTableIsSingleSource(t *testing.T) {
	for _, name := range []string{"run", "status", "check", "vpn", "install", "uninstall", "credential", "config"} {
		if _, ok := commands[name]; !ok {
			t.Errorf("comando %q fora da tabela", name)
		}
	}
	te := newTestEnv(t)
	te.elevated = func() bool { return false }
	for name := range commands {
		if code := te.run(name); code != 1 || !strings.Contains(te.errb.String(), "administrador") {
			t.Errorf("%s sem elevação: %d %q", name, code, te.errb)
		}
	}
	te.elevated = func() bool { return true }
	if code := te.run("status", "x"); code != 2 {
		t.Errorf("status com argumento: %d", code)
	}
	te.runService = func(svc.Hooks) error { return nil }
	for name := range commands {
		te.run(name)
		if strings.Contains(te.errb.String(), "comando desconhecido") {
			t.Errorf("%s não despachado: %q", name, te.errb)
		}
	}
}

// status --json devolve o snapshot do protocolo, legível por scripts (e2e).
func TestStatusJSON(t *testing.T) {
	te := newTestEnv(t)
	startService(t, te)
	waitFor(t, func() bool { return stateVia(te) == ipc.StateConectada })
	if code := te.run("status", "--json"); code != 0 {
		t.Fatalf("status --json: %d %q", code, te.errb)
	}
	var snap ipc.Snapshot
	if err := json.Unmarshal(te.out.Bytes(), &snap); err != nil {
		t.Fatalf("saída não é JSON: %v\n%s", err, te.out)
	}
	if len(snap.VPNs) != 1 || snap.VPNs[0].Name != "Matriz" || snap.VPNs[0].State != ipc.StateConectada {
		t.Fatalf("snapshot: %+v", snap)
	}
	if code := te.run("status", "--json", "x"); code != 2 {
		t.Fatalf("argumento extra: %d", code)
	}
	if code := te.run("status", "--yaml"); code != 2 {
		t.Fatalf("flag desconhecida: %d", code)
	}
}

// O texto do status mostra config inválida (com a Message real do serviço, sem
// prefixo próprio nem campos repetidos) e o prazo do bloqueio de credencial.
func TestFormatStatusConfigAndBlocked(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	problems := []config.FieldError{{Field: "vpns[0].check.port", Message: "obrigatória"}}
	ve := &config.ValidationError{Problems: problems}
	snap := ipc.Snapshot{
		Config: &ipc.ConfigStatus{OK: false, Message: ve.Error(), Fields: problems},
		VPNs:   []ipc.VPNView{{Name: "Matriz", State: "CredencialInvalida", BlockedUntilUnix: now.Add(time.Hour).Unix()}},
	}
	out := formatStatus(snap, now)
	if !strings.HasPrefix(out, "aviso: "+ve.Error()+"\n") || strings.Count(out, "vpns[0].check.port: obrigatória") != 1 {
		t.Errorf("mensagem real sem duplicar campos:\n%s", out)
	}
	if want := "bloqueada até " + time.Unix(now.Add(time.Hour).Unix(), 0).Format("15:04"); !strings.Contains(out, want) {
		t.Errorf("faltou %q em:\n%s", want, out)
	}
	// Campos que a mensagem não contém são listados.
	snap.Config = &ipc.ConfigStatus{Message: "config.json removido; mantendo a configuração em uso", Fields: problems}
	if out := formatStatus(snap, now); !strings.HasPrefix(out, "aviso: config.json removido; mantendo a configuração em uso\n") ||
		strings.Contains(out, "inválido") || !strings.Contains(out, "  vpns[0].check.port: obrigatória") {
		t.Errorf("caso removido:\n%s", out)
	}
	snap.Config = &ipc.ConfigStatus{OK: true}
	snap.VPNs[0].BlockedUntilUnix = now.Add(-time.Minute).Unix()
	if out := formatStatus(snap, now); strings.Contains(out, "aviso") || strings.Contains(out, "bloqueada") {
		t.Fatalf("config ok/prazo vencido não devem aparecer: %s", out)
	}
	if out := formatStatus(ipc.Snapshot{Config: &ipc.ConfigStatus{Message: "x"}}, now); !strings.Contains(out, "aviso: x") || !strings.Contains(out, "nenhuma VPN") {
		t.Fatalf("sem VPNs: %s", out)
	}
}

// Contrato do --json para scripts: chaves do protocolo, stderr limpo no
// sucesso; com o serviço fora, stdout vazio e erro em stderr (código 1).
func TestStatusJSONContract(t *testing.T) {
	te := newTestEnv(t)
	te.run("status", "--json")
	if te.out.Len() != 0 || !strings.HasPrefix(te.errb.String(), "erro: ") {
		t.Fatalf("dial falhando: out=%q err=%q", te.out, te.errb)
	}
	if code := te.run("status", "--json"); code != 1 {
		t.Fatalf("dial falhando: código %d", code)
	}
	te = newTestEnv(t)
	startService(t, te)
	waitFor(t, func() bool { return stateVia(te) == ipc.StateConectada })
	if code := te.run("status", "--json"); code != 0 || te.errb.Len() != 0 {
		t.Fatalf("sucesso: %d stderr=%q", code, te.errb)
	}
	var m map[string]any
	if err := json.Unmarshal(te.out.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	vpns, _ := m["vpns"].([]any)
	if len(vpns) != 1 {
		t.Fatalf("vpns: %v", m)
	}
	v, _ := vpns[0].(map[string]any)
	for _, k := range []string{"name", "state", "attempt"} {
		if _, ok := v[k]; !ok {
			t.Errorf("vpns[0] sem %q: %v", k, v)
		}
	}
	if _, ok := m["config"]; !ok {
		t.Errorf("snapshot sem \"config\": %v", m)
	}
}
