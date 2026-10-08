package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppVersion(t *testing.T) {
	defer func(v, c, d string) { version, commit, date = v, c, d }(version, commit, date)
	cases := []struct{ v, c, d, want string }{
		{"dev", "", "", "dev"},
		{"v2.1.0", "abc1234", "", "v2.1.0 (abc1234)"},
		{"v2.1.0", "abc1234", "2026-10-08T12:00:00Z", "v2.1.0 (abc1234, 2026-10-08T12:00:00Z)"},
		// Fora de tag o git describe já é o commit: não repete.
		{"abc1234-dirty", "abc1234", "2026-10-08T12:00:00Z", "abc1234-dirty (2026-10-08T12:00:00Z)"},
		{"abc1234", "abc1234", "", "abc1234"},
		{"v2.1.0-3-gabc1234", "abc1234", "", "v2.1.0-3-gabc1234 (abc1234)"},
	}
	for _, c := range cases {
		version, commit, date = c.v, c.c, c.d
		if got := appVersion(); got != c.want {
			t.Errorf("%q, esperava %q", got, c.want)
		}
	}
}

// O log da bandeja cria a pasta, grava em texto e mascara segredos como o
// log do serviço.
func TestOpenTrayLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "VPNMonitor")
	log, closeLog, err := openTrayLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	log.Info("bandeja iniciada", "versao", "v2.1.0", "senha", "segredo")
	log.Debug("não aparece no nível info")
	closeLog()
	b, err := os.ReadFile(filepath.Join(dir, "vpnmon-tray.log"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "bandeja iniciada") || !strings.Contains(s, "versao=v2.1.0") {
		t.Fatalf("log: %q", s)
	}
	if strings.Contains(s, "segredo") || strings.Contains(s, "não aparece") {
		t.Fatalf("segredo ou debug no log: %q", s)
	}
	// Pasta que não pode ser criada (um arquivo no caminho): erro, sem pânico.
	blocker := filepath.Join(t.TempDir(), "arquivo")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openTrayLog(filepath.Join(blocker, "VPNMonitor")); err == nil {
		t.Fatal("esperava erro com a pasta bloqueada")
	}
}
