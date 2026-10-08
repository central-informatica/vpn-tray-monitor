package service

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/adapters"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
)

func intp(n int) *int { return &n }

func TestAddUpdateRemoveVPN(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitView("Matriz", domain.Conectada)

	// "Adicionar VPN" da bandeja: só nome, entrada e verificação link.
	if err := h.o.AddVPN(config.RawVPN{Name: "Filial", RasEntry: "VPN Filial", Check: &config.RawCheck{Kind: config.CheckLink}}); err != nil {
		t.Fatal(err)
	}
	h.waitView("Filial", domain.Conectada)
	saved, err := config.Load(h.paths.ConfigFile)
	if err != nil || len(saved.VPNs) != 2 || saved.VPNs[1].IntervalSeconds != 30 {
		t.Fatalf("gravado: %+v %v", saved, err)
	}

	var e *ipc.Error
	err = h.o.AddVPN(config.RawVPN{Name: "FILIAL", RasEntry: "x", Check: &config.RawCheck{Kind: config.CheckLink}})
	if !asIPC(err, &e) || e.Code != ipc.CodeInvalidConfig || e.Fields[0].Field != "name" {
		t.Fatalf("nome repetido: %v", err)
	}
	err = h.o.AddVPN(config.RawVPN{Name: "X", RasEntry: "x", Check: &config.RawCheck{Kind: config.CheckTCP, Host: "h"}})
	if !asIPC(err, &e) || e.Fields[0].Field != "check.port" {
		t.Fatalf("erro junto ao campo: %v", err)
	}

	err = h.o.UpdateVPN("filial", config.RawVPN{RasEntry: "VPN Filial", IntervalSeconds: intp(60), Check: &config.RawCheck{Kind: config.CheckLink}})
	if err != nil {
		t.Fatal(err)
	}
	if c := h.o.GetConfig(); c.VPNs[1].Name != "Filial" || c.VPNs[1].IntervalSeconds != 60 {
		t.Fatalf("update: %+v", c.VPNs[1])
	}
	err = h.o.UpdateVPN("Filial", config.RawVPN{Name: "Outra", RasEntry: "x"})
	if !asIPC(err, &e) || e.Code != ipc.CodeInvalidConfig || !strings.Contains(e.Message, "não pode ser alterado") {
		t.Fatalf("renomear: %v", err)
	}
	if err := h.o.UpdateVPN("Nenhuma", config.RawVPN{RasEntry: "x"}); !asIPC(err, &e) || e.Code != ipc.CodeNotFound {
		t.Fatalf("inexistente: %v", err)
	}

	if err := h.o.SetEnabled("Filial", false); err != nil {
		t.Fatal(err)
	}
	h.waitView("Filial", domain.Desativada)
	if err := h.o.RemoveVPN("Filial"); err != nil {
		t.Fatal(err)
	}
	if n := len(h.o.Status().VPNs); n != 1 {
		t.Fatalf("restaram %d", n)
	}
}

func TestSetGlobalCallsOnGlobals(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(), config.State{})
	var got config.Config
	h.o.opts.OnGlobals = func(c config.Config) { got = c }
	lvl, off := "debug", false
	if err := h.o.SetGlobal(ipc.SetGlobalRequest{LogLevel: &lvl, Notifications: &off}); err != nil {
		t.Fatal(err)
	}
	if got.LogLevel != "debug" || got.Notifications {
		t.Fatalf("%+v", got)
	}
	bad := "verbose"
	var e *ipc.Error
	if err := h.o.SetGlobal(ipc.SetGlobalRequest{LogLevel: &bad}); !asIPC(err, &e) || e.Fields[0].Field != "logLevel" {
		t.Fatalf("nível inválido: %v", err)
	}
	if c, _ := config.Load(h.paths.ConfigFile); c.LogLevel != "debug" {
		t.Fatal("config inválida não pode ser gravada")
	}
}

func TestReloadFromDisk(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitView("Matriz", domain.Conectada)
	events, cancel := h.o.Subscribe()
	defer cancel()
	<-events // snapshot

	// Gravação própria (mutate) não recarrega: o hash bate.
	_ = h.o.SetEnabled("Matriz", true)
	sup := h.supOf("Matriz")
	for range drain(events) { // snapshot e configStatus da própria mutação
	}
	h.o.ReloadFromDisk()
	if h.supOf("Matriz") != sup {
		t.Fatal("a própria gravação não deve recarregar")
	}
	for m := range drain(events) {
		if m.Type == ipc.TypeSnapshot || m.Type == ipc.TypeConfigStatus {
			t.Fatalf("recarga da própria gravação publicou %s", m.Type)
		}
	}

	// Edição manual inválida: mantém a anterior e publica configStatus.
	_ = os.WriteFile(h.paths.ConfigFile, []byte(`{"version":2,"vpns":[{"name":"Matriz","rasEntry":"VPN Matriz","check":{"kind":"ping"}}]}`), 0o600)
	h.o.ReloadFromDisk()
	if h.supOf("Matriz") != sup {
		t.Fatal("config inválida não pode derrubar a anterior")
	}
	var st ipc.ConfigStatus
	for m := range drain(events) {
		if m.Type == ipc.TypeConfigStatus {
			_ = ipc.DecodePayload(m.Payload, &st)
		}
	}
	if st.OK || len(st.Fields) == 0 || st.Fields[0].Field != "vpns[0].check.host" {
		t.Fatalf("configStatus: %+v", st)
	}
	if ev := h.events.Snapshot(); len(ev) == 0 || ev[len(ev)-1].Level != "warning" {
		t.Fatalf("Event Log: %+v", ev)
	}

	// Enquanto o arquivo em disco estiver inválido, mudanças pelo pipe são
	// recusadas: gravar agora apagaria a edição manual.
	var e *ipc.Error
	err := h.o.AddVPN(config.RawVPN{Name: "Nova", RasEntry: "x", Check: &config.RawCheck{Kind: config.CheckLink}})
	if !asIPC(err, &e) || e.Code != ipc.CodeInvalidConfig || !strings.Contains(e.Message, "corrija o arquivo") {
		t.Fatalf("mudança com arquivo inválido: %v", err)
	}
	if b, _ := os.ReadFile(h.paths.ConfigFile); !strings.Contains(string(b), `"kind":"ping"}}]}`) {
		t.Fatalf("arquivo inválido foi sobrescrito: %s", b)
	}

	// Arquivo momentaneamente vazio (editor gravando em dois passos): mantém.
	_ = os.WriteFile(h.paths.ConfigFile, nil, 0o600)
	h.o.ReloadFromDisk()
	if h.supOf("Matriz") != sup {
		t.Fatal("arquivo vazio não pode derrubar a config anterior")
	}

	// Edição manual válida: aplica.
	c := cfgWith(vpnNamed("Matriz"), vpnNamed("Filial"))
	data, _ := config.Marshal(c)
	_ = os.WriteFile(h.paths.ConfigFile, data, 0o600)
	h.o.ReloadFromDisk()
	h.waitView("Filial", domain.Conectada)
	if err := h.o.SetEnabled("Filial", true); err != nil {
		t.Fatalf("após recarga válida as mudanças voltam a valer: %v", err)
	}
}

func TestListRasEntriesMarksMonitored(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	got, err := h.o.ListRasEntries()
	if err != nil || len(got) != 3 || !got[0].Monitored || got[1].Monitored {
		t.Fatalf("%+v %v", got, err)
	}
}

// Voltar o arquivo inválido ao conteúdo que o próprio serviço gravou (mesmo
// hash) tem de liberar as mudanças pelo pipe de novo.
func TestReloadRevertToOwnWriteClearsInvalid(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitView("Matriz", domain.Conectada)
	if err := h.o.SetEnabled("Matriz", true); err != nil {
		t.Fatal(err)
	}
	own, _ := os.ReadFile(h.paths.ConfigFile)
	_ = os.WriteFile(h.paths.ConfigFile, []byte("{"), 0o600)
	h.o.ReloadFromDisk()
	var e *ipc.Error
	if err := h.o.SetEnabled("Matriz", true); !asIPC(err, &e) || e.Code != ipc.CodeInvalidConfig {
		t.Fatalf("arquivo inválido: %v", err)
	}
	_ = os.WriteFile(h.paths.ConfigFile, own, 0o600)
	h.o.ReloadFromDisk()
	if err := h.o.SetEnabled("Matriz", true); err != nil {
		t.Fatalf("arquivo restaurado deveria liberar as mudanças: %v", err)
	}
}

func TestConfigOpsAfterStop(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitView("Matriz", domain.Conectada)
	h.o.Stop()
	before, _ := os.ReadFile(h.paths.ConfigFile)
	var e *ipc.Error
	if err := h.o.SetEnabled("Matriz", false); !asIPC(err, &e) || e.Code != ipc.CodeInternal {
		t.Fatalf("mudança após Stop: %v", err)
	}
	if after, _ := os.ReadFile(h.paths.ConfigFile); string(after) != string(before) {
		t.Fatal("mudança após Stop não pode gravar")
	}
	data, _ := config.Marshal(cfgWith(vpnNamed("Matriz"), vpnNamed("Filial")))
	_ = os.WriteFile(h.paths.ConfigFile, data, 0o600)
	h.o.ReloadFromDisk()
	if n := len(h.o.Status().VPNs); n != 0 {
		t.Fatalf("recarga após Stop relançou %d", n)
	}
}

// (a) Mudar só os limites com a mesma entrada RAS: o prazo de backoff
// calculado com os limites antigos não segura a discagem.
func TestUpdateVPNWithLowerBackoffCapsNextAttempt(t *testing.T) {
	w := &stubWorld{network: true, outcomes: []adapters.DialOutcome{{Err: &domain.DialError{Code: 809}}}}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitViewWhere("Matriz", func(v ipc.VPNView) bool { return v.NextAttemptUnix == t0.Add(30*time.Second).Unix() })
	err := h.o.UpdateVPN("Matriz", config.RawVPN{RasEntry: "VPN Matriz", IntervalSeconds: intp(5), MaxBackoffSeconds: intp(5),
		Check: &config.RawCheck{Kind: config.CheckPing, Host: "10.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	// O prazo calculado com o teto antigo (30 s) cai para o teto novo (5 s).
	h.waitViewWhere("Matriz", func(v ipc.VPNView) bool { return v.NextAttemptUnix == t0.Add(5*time.Second).Unix() })
	if n := len(w.get().dials); n != 1 {
		t.Fatalf("não pode discar antes do prazo: %d discagens", n)
	}
	h.clk.Advance(5 * time.Second)
	h.waitView("Matriz", domain.Conectada)
	if n := len(w.get().dials); n != 2 {
		t.Fatalf("discagens = %d", n)
	}
}

// (a) Mudar a verificação de uma VPN bloqueada por credencial não a desbloqueia.
func TestUpdateVPNKeepsCredentialBlock(t *testing.T) {
	w := &stubWorld{network: true, outcomes: rejected()}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitView("Matriz", domain.CredencialInvalida)
	err := h.o.UpdateVPN("Matriz", config.RawVPN{RasEntry: "VPN Matriz", IntervalSeconds: intp(60),
		Check: &config.RawCheck{Kind: config.CheckLink}})
	if err != nil {
		t.Fatal(err)
	}
	h.waitViewWhere("Matriz", func(v ipc.VPNView) bool {
		return v.State == string(domain.CredencialInvalida) && v.CheckKind == "link"
	})
	for range 30 {
		h.clk.Advance(time.Minute)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(w.get().dials); n != 1 {
		t.Fatalf("mudança de config discou com a credencial rejeitada: %d discagens", n)
	}
}

// (b) Uma rejeição que chega enquanto a recarga para o supervisor (já fora
// de o.sups) não é publicada, mas vira a base do supervisor seguinte.
func TestUpdateFromDrainingSupervisorBecomesBase(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitView("Matriz", domain.Conectada)
	events, cancel := h.o.Subscribe()
	defer cancel()
	<-events

	h.o.mu.Lock()
	r := h.o.sups["matriz"]
	sup := r.sup
	delete(h.o.sups, "matriz") // como apply faz antes de parar o supervisor
	h.o.mu.Unlock()
	blocked := domain.Status{State: domain.CredencialInvalida, Since: t0, BlockedFP: "fp1", RejectedAt: t0,
		LastErr: &domain.DialError{Class: ras.ClassCredencial, Code: 691, Message: "senha"}}
	h.o.onUpdate(r, sup, Update{Name: "Matriz", Status: blocked})
	for m := range drain(events) {
		if m.Type == ipc.TypeVPNState {
			t.Fatalf("supervisor fora de o.sups não pode publicar: %+v", m)
		}
	}
	h.o.mu.Lock()
	got := r.base
	h.o.sups["matriz"] = r // devolve, para a recarga partir dele
	h.o.mu.Unlock()
	if got.State != domain.CredencialInvalida {
		t.Fatalf("base não atualizada: %+v", got)
	}
	changed := vpnNamed("Matriz")
	changed.IntervalSeconds = 60
	h.o.applyMu.Lock()
	err := h.o.apply(cfgWith(changed))
	h.o.applyMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	h.waitViewWhere("Matriz", func(v ipc.VPNView) bool {
		return v.State == string(domain.CredencialInvalida) && v.LastError != nil && v.LastError.Code == 691
	})
}

// (c) Disjuntor aberto no bloqueio por credencial: o reconnect manual recria
// e a resposta é a recusa da credencial já rejeitada, sem discar.
func TestReconnectAfterTripInCredentialBlockIsRejected(t *testing.T) {
	w := &stubWorld{network: true, outcomes: rejected()}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitView("Matriz", domain.CredencialInvalida)
	w.set(func(w *stubWorld) { w.probePanics = 3 })
	h.checkNowEventually("Matriz")
	h.waitEvents(1)
	h.clk.Advance(5 * time.Second)
	h.checkNowEventually("Matriz")
	h.waitEvents(2)
	h.clk.Advance(10 * time.Second)
	h.checkNowEventually("Matriz")
	h.waitViewWhere("Matriz", func(v ipc.VPNView) bool {
		return v.LastError != nil && v.LastError.Message == trippedText
	})
	var e *ipc.Error
	if err := h.o.Reconnect("Matriz"); !asIPC(err, &e) || e.Code != ipc.CodeCredentialRejected {
		t.Fatalf("reconnect: %v", err)
	}
	if n := len(w.get().dials); n != 1 {
		t.Fatalf("discagens = %d", n)
	}
}

// O cofre é por nome de VPN: trocar a entrada RAS de uma VPN bloqueada por
// credencial não libera uma nova discagem com a mesma credencial.
func TestUpdateVPNNewEntryKeepsCredentialBlock(t *testing.T) {
	w := &stubWorld{network: true, outcomes: rejected()}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitView("Matriz", domain.CredencialInvalida)
	err := h.o.UpdateVPN("Matriz", config.RawVPN{RasEntry: "VPN Backup",
		Check: &config.RawCheck{Kind: config.CheckPing, Host: "10.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	h.waitViewWhere("Matriz", func(v ipc.VPNView) bool {
		return v.State == string(domain.CredencialInvalida) && v.Entry == "VPN Backup"
	})
	for range 30 {
		h.clk.Advance(time.Minute)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(w.get().dials); n != 1 {
		t.Fatalf("troca de entrada discou com a credencial rejeitada: %d discagens", n)
	}
}

func TestUpdateVPNKeepsEnabledWhenOmitted(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	if err := h.o.SetEnabled("Matriz", false); err != nil {
		t.Fatal(err)
	}
	err := h.o.UpdateVPN("Matriz", config.RawVPN{RasEntry: "VPN Matriz", IntervalSeconds: intp(60),
		Check: &config.RawCheck{Kind: config.CheckPing, Host: "10.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if c := h.o.GetConfig(); c.VPNs[0].Enabled || c.VPNs[0].IntervalSeconds != 60 {
		t.Fatalf("Enabled omitido deveria ficar como estava: %+v", c.VPNs[0])
	}
	on := true
	if err := h.o.UpdateVPN("Matriz", config.RawVPN{RasEntry: "VPN Matriz", Enabled: &on,
		Check: &config.RawCheck{Kind: config.CheckPing, Host: "10.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	if !h.o.GetConfig().VPNs[0].Enabled {
		t.Fatal("Enabled explícito deveria valer")
	}
}

// Edição manual ainda não recarregada (dentro da espera do observador): a
// mudança pelo pipe é recusada e o arquivo fica intacto.
func TestMutateRefusesUnreloadedManualEdit(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	manual, _ := config.Marshal(cfgWith(vpnNamed("Matriz"), vpnNamed("Filial")))
	_ = os.WriteFile(h.paths.ConfigFile, manual, 0o600)
	var e *ipc.Error
	err := h.o.SetEnabled("Matriz", false)
	if !asIPC(err, &e) || e.Code != ipc.CodeInvalidConfig || !strings.Contains(e.Message, "alterado no disco") {
		t.Fatalf("mudança com edição pendente: %v", err)
	}
	if b, _ := os.ReadFile(h.paths.ConfigFile); string(b) != string(manual) {
		t.Fatal("a edição manual foi sobrescrita")
	}
	h.o.ReloadFromDisk()
	if err := h.o.SetEnabled("Matriz", false); err != nil {
		t.Fatalf("após a recarga a mudança vale: %v", err)
	}
	// Sem arquivo (apagado), a mutação recria.
	_ = os.Remove(h.paths.ConfigFile)
	if err := h.o.SetEnabled("Matriz", true); err != nil {
		t.Fatalf("sem arquivo: %v", err)
	}
	if c, err := config.Load(h.paths.ConfigFile); err != nil || len(c.VPNs) != 2 {
		t.Fatalf("recriado: %+v %v", c, err)
	}
}
