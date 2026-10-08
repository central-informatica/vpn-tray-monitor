package logging

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotationKeepsMaxFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vpnmon.log")
	w, err := OpenRotating(p, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, s := range []string{"aaaaaaaa\n", "bbbbbbbb\n", "cccccccc\n", "dddddddd\n"} {
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "<ausente>"
		}
		return string(b)
	}
	if read("vpnmon.log") != "dddddddd\n" || read("vpnmon.1.log") != "cccccccc\n" || read("vpnmon.2.log") != "bbbbbbbb\n" {
		t.Fatalf("rotação errada: %q %q %q", read("vpnmon.log"), read("vpnmon.1.log"), read("vpnmon.2.log"))
	}
	if read("vpnmon.3.log") != "<ausente>" {
		t.Fatal("arquivo além de maxFiles não deveria existir")
	}
}

func TestRotationAppendsToExisting(t *testing.T) {
	p := filepath.Join(t.TempDir(), "vpnmon.log")
	_ = os.WriteFile(p, []byte("12345"), 0o600)
	w, _ := OpenRotating(p, 8, 1)
	_, _ = w.Write([]byte("678"))
	_, _ = w.Write([]byte("9"))
	w.Close()
	if b, _ := os.ReadFile(p); string(b) != "9" {
		t.Fatalf("tamanho existente ignorado: %q", b)
	}
}

func TestLoggerRedactsAndLevels(t *testing.T) {
	var buf bytes.Buffer
	lv := new(slog.LevelVar)
	l := ForVPN(New(&buf, lv), "Matriz")
	l.Info("discando", "senha", "hunter2", "Password", "x1", "user", "ana")
	l.Debug("invisível")
	lv.Set(slog.LevelDebug)
	l.Debug("visível")
	out := buf.String()
	for _, bad := range []string{"hunter2", "x1", "invisível"} {
		if strings.Contains(out, bad) {
			t.Fatalf("%q não deveria aparecer em:\n%s", bad, out)
		}
	}
	for _, good := range []string{"vpn=Matriz", "user=ana", "visível", "senha=***"} {
		if !strings.Contains(out, good) {
			t.Fatalf("%q deveria aparecer em:\n%s", good, out)
		}
	}
}

func TestParseLevel(t *testing.T) {
	if l, err := ParseLevel("WARN"); err != nil || l != slog.LevelWarn {
		t.Fatal(l, err)
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestTailStartsAtLineBoundary(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.log")
	_ = os.WriteFile(p, []byte("linha um\nlinha dois\nlinha três\n"), 0o600)
	got, err := Tail(p, 16)
	if err != nil {
		t.Fatal(err)
	}
	if got != "linha três\n" {
		t.Fatalf("Tail = %q", got)
	}
	all, _ := Tail(p, 1<<20)
	if !strings.HasPrefix(all, "linha um") {
		t.Fatalf("Tail inteiro = %q", all)
	}
}

func TestRecordingSink(t *testing.T) {
	var s RecordingSink
	var sink EventSink = &s
	sink.Error("panic recuperado")
	if ev := s.Snapshot(); len(ev) != 1 || ev[0].Level != "error" {
		t.Fatalf("%+v", ev)
	}
}
