package viewmodel

import (
	"errors"
	"slices"
	"testing"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

func sampleVPN() config.VPN {
	return config.VPN{Name: "Matriz", RasEntry: "VPN Matriz", Enabled: true,
		Check:           config.Check{Kind: config.CheckTCP, Host: "10.0.0.1", Port: 443, TimeoutSeconds: 5},
		IntervalSeconds: 30, FailuresBeforeReconnect: 3, GraceAfterConnectSeconds: 15, ConnectTimeoutSeconds: 60, MaxBackoffSeconds: 300}
}

func TestFormRoundTripUpdate(t *testing.T) {
	f := FormFrom(sampleVPN())
	if f.IsNew || f.Name != "Matriz" || f.Port != "443" || f.Kind != "tcp" || !f.PortEnabled() || !f.HostEnabled() {
		t.Fatalf("form: %+v", f)
	}
	f.Interval = " 60 "
	f.Enabled = false
	c, errs := f.Command()
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	// Objeto completo, inclusive enabled (§5.2: UpdateVPN preserva enabled
	// omitido, mas a janela sempre manda o que o usuário vê).
	want := `updateVpn {"name":"Matriz","config":{"name":"Matriz","rasEntry":"VPN Matriz","enabled":false,` +
		`"check":{"kind":"tcp","host":"10.0.0.1","port":443,"timeoutSeconds":5},"intervalSeconds":60,` +
		`"failuresBeforeReconnect":3,"graceAfterConnectSeconds":15,"connectTimeoutSeconds":60,"maxBackoffSeconds":300}}`
	if got := wire(t, c); got != want {
		t.Fatalf("\n%s\nesperava\n%s", got, want)
	}
}

func TestNewFormDefaultsAreValid(t *testing.T) {
	f := NewForm()
	if !f.IsNew || f.Kind != "ping" || !f.Enabled || f.Interval != "30" || f.MaxBackoff != "300" || f.PortEnabled() {
		t.Fatalf("padrões: %+v", f)
	}
	f.Name, f.RasEntry, f.Host = " Filial ", "VPN Filial", "10.0.0.2"
	c, errs := f.Command()
	if len(errs) != 0 || c.Type != ipc.TypeAddVPN {
		t.Fatalf("%v %v", c, errs)
	}
	raw := c.Payload.(ipc.AddVPNRequest).Config
	if raw.Name != "Filial" {
		t.Fatalf("nome: %q", raw.Name)
	}
	if probs := config.ValidateVPN(raw.Normalize()); len(probs) != 0 {
		t.Fatalf("padrões inválidos para o serviço: %v", probs)
	}
}

// Trocar tcp → ping/link não leva a porta (o serviço recusaria "só vale para
// tcp"); link também não leva host.
func TestFormDropsFieldsOfOtherKinds(t *testing.T) {
	f := FormFrom(sampleVPN())
	f.Kind = "ping"
	c, _ := f.Command()
	if raw := c.Payload.(ipc.UpdateVPNRequest).Config; raw.Check.Port != 0 || raw.Check.Host != "10.0.0.1" {
		t.Fatalf("ping: %+v", raw.Check)
	}
	f.Kind = "link"
	if f.HostEnabled() {
		t.Fatal("link não tem host")
	}
	c, _ = f.Command()
	if raw := c.Payload.(ipc.UpdateVPNRequest).Config; raw.Check.Port != 0 || raw.Check.Host != "" {
		t.Fatalf("link: %+v", raw.Check)
	}
}

func TestFormLocalErrors(t *testing.T) {
	f := NewForm()
	f.Name, f.RasEntry, f.Host = "  ", "E", "h"
	f.Port, f.Kind = "abc", "tcp"
	f.Interval, f.Timeout = "", "1.5"
	f.Grace = "-0"
	c, errs := f.Command()
	want := FieldErrors{
		FieldName:     "obrigatório",
		FieldPort:     "use um número inteiro",
		FieldInterval: "obrigatório",
		FieldTimeout:  "use um número inteiro",
	}
	if c.Type != "" || len(errs) != len(want) {
		t.Fatalf("%v %v", c, errs)
	}
	for k, v := range want {
		if errs[k] != v {
			t.Errorf("%s: %q", k, errs[k])
		}
	}
	f = NewForm()
	f.Kind = "icmp"
	if _, errs := f.Command(); errs[FieldKind] == "" {
		t.Fatal("tipo desconhecido")
	}
}

func TestServiceErrors(t *testing.T) {
	err := &ipc.Error{Code: ipc.CodeInvalidConfig, Message: "config inválida: ...", Fields: []config.FieldError{
		{Field: "check.port", Message: "obrigatória em tcp, entre 1 e 65535"},
		{Field: "vpns[2].intervalSeconds", Message: "deve estar entre 5 e 3600"},
		{Field: "vpns[2].intervalSeconds", Message: "outro"},
		{Field: "logLevel", Message: "use debug, info, warn ou error"},
	}}
	fields, general := ServiceErrors(err)
	if fields[FieldPort] != "obrigatória em tcp, entre 1 e 65535" || fields[FieldInterval] != "deve estar entre 5 e 3600; outro" || len(fields) != 2 {
		t.Fatalf("campos: %v", fields)
	}
	if general != "logLevel: use debug, info, warn ou error" {
		t.Fatalf("geral: %q", general)
	}
	// Só campos do formulário: aviso genérico apontando para eles.
	_, general = ServiceErrors(&ipc.Error{Code: ipc.CodeInvalidConfig, Fields: []config.FieldError{{Field: "name", Message: "já existe"}}})
	if general != "Corrija os campos marcados." {
		t.Fatalf("geral só com campos: %q", general)
	}
	// Recusa sem campos (edição manual pendente) vai inteira para o aviso.
	disk := &ipc.Error{Code: ipc.CodeInvalidConfig, Message: "config.json foi alterado no disco; aguarde a recarga e tente de novo"}
	if fields, general := ServiceErrors(disk); len(fields) != 0 || general != disk.Message {
		t.Fatalf("disco: %v %q", fields, general)
	}
	if fields, general := ServiceErrors(errors.New("x")); len(fields) != 0 || general != "x" {
		t.Fatalf("outro erro: %v %q", fields, general)
	}
	if fields, general := ServiceErrors(nil); fields != nil || general != "" {
		t.Fatal("nil")
	}
}

func TestGlobals(t *testing.T) {
	c := config.Empty()
	c.Notifications, c.LogLevel = false, "debug"
	g := GlobalsFrom(c)
	if g.Notifications || g.LogLevel != "debug" {
		t.Fatalf("%+v", g)
	}
	g.Notifications = true
	if got := wire(t, g.Command()); got != `setGlobal {"notifications":true,"logLevel":"debug"}` {
		t.Fatalf("%s", got)
	}
	if len(LogLevels) != 4 || len(CheckKinds) != 3 {
		t.Fatal("listas")
	}
}

func TestConfigHelpers(t *testing.T) {
	c := config.Empty()
	c.VPNs = []config.VPN{sampleVPN(), {Name: "Filial"}}
	if got := VPNNames(c); len(got) != 2 || got[1] != "Filial" {
		t.Fatalf("%v", got)
	}
	if v, ok := FindVPN(c, "MATRIZ"); !ok || v.RasEntry != "VPN Matriz" {
		t.Fatal("FindVPN sem diferenciar maiúsculas")
	}
	if _, ok := FindVPN(c, "x"); ok {
		t.Fatal("inexistente")
	}
	for _, f := range FormFields {
		if FieldLabels[f] == "" {
			t.Errorf("sem rótulo: %s", f)
		}
	}
}

// A janela só copia valores: ler e escrever o formulário mora aqui.
func TestFormFieldAccess(t *testing.T) {
	f := FormFrom(sampleVPN())
	for _, field := range append(slices.Clone(TextFields), FieldRasEntry) {
		g := f.WithText(field, "novo-"+field)
		if g.Text(field) != "novo-"+field {
			t.Errorf("%s: %q", field, g.Text(field))
		}
		if f.Text(field) == "novo-"+field {
			t.Errorf("%s: WithText alterou o original", field)
		}
	}
	if f.Text(FieldEnabled) != "" || f.WithText(FieldKind, "x") != f {
		t.Fatal("campos que não são texto")
	}
	if f.Text(FieldPort) != "443" || f.Text(FieldRasEntry) != "VPN Matriz" {
		t.Fatalf("valores: %+v", f)
	}
	if f.KindIndex() != 1 || f.WithKindIndex(2).Kind != "link" || f.WithKindIndex(-1).Kind != "tcp" || f.WithKindIndex(9).Kind != "tcp" {
		t.Fatal("tipo por posição")
	}
	if !f.NameReadOnly() || NewForm().NameReadOnly() {
		t.Fatal("nome somente leitura só em VPN existente")
	}
	g := Globals{LogLevel: "warn"}
	if g.LevelIndex() != 2 || g.WithLevelIndex(0).LogLevel != "debug" || g.WithLevelIndex(7).LogLevel != "warn" {
		t.Fatal("nível por posição")
	}
}

func TestSelectIndex(t *testing.T) {
	names := []string{"Matriz", "Filial"}
	cases := map[string]int{"Filial": 1, " filial ": 1, "Removida": 0, "": 0}
	for name, want := range cases {
		if got := SelectIndex(names, name); got != want {
			t.Errorf("%q: %d", name, got)
		}
	}
	if SelectIndex(nil, "x") != -1 {
		t.Fatal("lista vazia")
	}
	if got := EntryNames([]ipc.RasEntry{{Name: "A", Monitored: true}, {Name: "B"}}); len(got) != 2 || got[1] != "B" {
		t.Fatalf("%v", got)
	}
}
