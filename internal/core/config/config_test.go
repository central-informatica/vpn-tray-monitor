package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minimal = `{"version":2,"vpns":[{"name":"Matriz","rasEntry":"VPN Matriz","check":{"kind":"ping","host":"10.254.1.172"}}]}`

func TestParseAppliesDefaults(t *testing.T) {
	c, err := Parse([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	want := VPN{
		Name: "Matriz", RasEntry: "VPN Matriz", Enabled: true,
		Check:           Check{Kind: CheckPing, Host: "10.254.1.172", TimeoutSeconds: 5},
		IntervalSeconds: 30, FailuresBeforeReconnect: 3, GraceAfterConnectSeconds: 15,
		ConnectTimeoutSeconds: 60, MaxBackoffSeconds: 300,
	}
	if len(c.VPNs) != 1 || c.VPNs[0] != want {
		t.Fatalf("VPN = %+v\nquer %+v", c.VPNs, want)
	}
	if !c.Notifications || c.LogLevel != "info" || c.Log != (LogConfig{1, 5}) {
		t.Fatalf("globais = %+v", c)
	}
}

func TestParseAcceptsBOMAndSpecExample(t *testing.T) {
	spec := `{
  "version": 2, "notifications": true, "logLevel": "info",
  "log": { "maxSizeMB": 1, "maxFiles": 5 },
  "vpns": [{ "name": "Matriz", "rasEntry": "VPN Matriz", "enabled": true,
    "check": { "kind": "ping", "host": "10.254.1.172", "timeoutSeconds": 5 },
    "intervalSeconds": 30, "failuresBeforeReconnect": 3, "graceAfterConnectSeconds": 15,
    "connectTimeoutSeconds": 60, "maxBackoffSeconds": 300 }]}`
	if _, err := Parse(append([]byte("\xEF\xBB\xBF"), spec...)); err != nil {
		t.Fatal(err)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"campo desconhecido":   `{"version":2,"vpns":[],"extra":1}`,
		"campo desconhecido 2": `{"version":2,"vpns":[{"name":"a","rasEntry":"a","check":{"kind":"link"},"connector":"x"}]}`,
		"lixo depois":          `{"version":2,"vpns":[]} {}`,
		"sem versão":           `{"vpns":[]}`,
		"versão 1":             `{"version":1,"vpns":[]}`,
		"malformado":           `{"version":2,`,
	}
	for name, in := range cases {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: esperava erro", name)
		}
	}
}

func vpn(mut func(*VPN)) Config {
	c := Empty()
	v := RawVPN{Name: "Matriz", RasEntry: "VPN Matriz", Check: &RawCheck{Kind: CheckPing, Host: "10.0.0.1"}}.Normalize()
	mut(&v)
	c.VPNs = []VPN{v}
	return c
}

func TestValidateFieldErrors(t *testing.T) {
	cases := []struct {
		field string
		mut   func(*VPN)
	}{
		{"vpns[0].name", func(v *VPN) { v.Name = "" }},
		{"vpns[0].name", func(v *VPN) { v.Name = strings.Repeat("é", 65) }},
		{"vpns[0].name", func(v *VPN) { v.Name = " Matriz" }},
		{"vpns[0].name", func(v *VPN) { v.Name = "a\nb" }},
		{"vpns[0].rasEntry", func(v *VPN) { v.RasEntry = "" }},
		{"vpns[0].check.kind", func(v *VPN) { v.Check.Kind = "icmp" }},
		{"vpns[0].check.host", func(v *VPN) { v.Check.Host = "" }},
		{"vpns[0].check.host", func(v *VPN) { v.Check.Host = "fe80::1" }},
		{"vpns[0].check.host", func(v *VPN) { v.Check.Host = "999.1.1.1" }},
		{"vpns[0].check.host", func(v *VPN) { v.Check.Host = "-x.example" }},
		{"vpns[0].check.port", func(v *VPN) { v.Check.Kind = CheckTCP }},
		{"vpns[0].check.port", func(v *VPN) { v.Check.Kind = CheckTCP; v.Check.Port = 70000 }},
		{"vpns[0].check.port", func(v *VPN) { v.Check.Port = 443 }},
		{"vpns[0].check.timeoutSeconds", func(v *VPN) { v.Check.TimeoutSeconds = 31 }},
		{"vpns[0].intervalSeconds", func(v *VPN) { v.IntervalSeconds = 4 }},
		{"vpns[0].failuresBeforeReconnect", func(v *VPN) { v.FailuresBeforeReconnect = 21 }},
		{"vpns[0].graceAfterConnectSeconds", func(v *VPN) { v.GraceAfterConnectSeconds = -1 }},
		{"vpns[0].connectTimeoutSeconds", func(v *VPN) { v.ConnectTimeoutSeconds = 9 }},
		{"vpns[0].maxBackoffSeconds", func(v *VPN) { v.IntervalSeconds = 600; v.MaxBackoffSeconds = 300 }},
		{"vpns[0].maxBackoffSeconds", func(v *VPN) { v.MaxBackoffSeconds = 3601 }},
	}
	for _, tc := range cases {
		err := Validate(vpn(tc.mut))
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: esperava ValidationError, veio %v", tc.field, err)
			continue
		}
		if ve.Problems[0].Field != tc.field {
			t.Errorf("campo = %q, quer %q (%v)", ve.Problems[0].Field, tc.field, ve)
		}
	}
	if err := Validate(vpn(func(v *VPN) { v.Check = Check{Kind: CheckLink, TimeoutSeconds: 5} })); err != nil {
		t.Errorf("link sem host deve valer: %v", err)
	}
	if err := Validate(vpn(func(v *VPN) { v.Check.Host = "vpn.empresa.local" })); err != nil {
		t.Errorf("nome DNS deve valer: %v", err)
	}
}

func TestValidateDuplicateNamesIgnoreCase(t *testing.T) {
	c := vpn(func(*VPN) {})
	dup := c.VPNs[0]
	dup.Name = "MATRIZ"
	c.VPNs = append(c.VPNs, dup)
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "vpns[1].name") {
		t.Fatalf("esperava nome repetido em vpns[1], veio %v", err)
	}
}

func TestSaveRoundTripAndRefusesInvalid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	c := vpn(func(*VPN) {})
	if _, err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.VPNs[0] != c.VPNs[0] {
		t.Fatalf("ida e volta: %+v", got.VPNs[0])
	}
	bad := vpn(func(v *VPN) { v.IntervalSeconds = 1 })
	if _, err := Save(p, bad); err == nil {
		t.Fatal("config inválida não pode ser gravada")
	}
	after, _ := os.ReadFile(p)
	if !strings.Contains(string(after), `"intervalSeconds": 30`) {
		t.Fatal("arquivo foi alterado por config inválida")
	}
}

var t0 = mustTime("2026-10-07T12:00:00Z")
