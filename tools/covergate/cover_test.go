package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `mode: set
github.com/guibsu/vpn-tray-monitor/internal/a/a.go:1.1,2.2 3 1
github.com/guibsu/vpn-tray-monitor/internal/a/a.go:3.1,4.2 1 0
github.com/guibsu/vpn-tray-monitor/internal/b/b.go:1.1,2.2 4 0
github.com/guibsu/vpn-tray-monitor/internal/b/b.go:1.1,2.2 4 2
github.com/guibsu/vpn-tray-monitor/internal/b/c.go:1.1,2.2 2 0
`

func TestParse(t *testing.T) {
	pkgs, total, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	// a: 3 de 4; b: bloco repetido conta uma vez e coberto (4) + 0 de 2 = 4 de 6.
	want := []PkgCover{
		{"github.com/guibsu/vpn-tray-monitor/internal/a", 3, 4},
		{"github.com/guibsu/vpn-tray-monitor/internal/b", 4, 6},
	}
	if len(pkgs) != 2 || pkgs[0] != want[0] || pkgs[1] != want[1] {
		t.Fatalf("%+v", pkgs)
	}
	if total.Covered != 7 || total.Total != 10 || total.Percent() != 70 {
		t.Fatalf("total %+v", total)
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{
		"",
		"github.com/x/a.go:1.1,2.2 1 1\n",
		"mode: set\nlixo\n",
		"mode: set\na.go:1.1,2.2 x 1\n",
		"mode: set\na.go:1.1,2.2 1 -1\n",
	} {
		if _, _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%q aceito", in)
		}
	}
}

func TestPercentWithoutStatements(t *testing.T) {
	if p := (PkgCover{}).Percent(); p != 100 {
		t.Fatal(p)
	}
}

func TestRunGate(t *testing.T) {
	dir := t.TempDir()
	prof := filepath.Join(dir, "c.out")
	if err := os.WriteFile(prof, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := filepath.Join(dir, "summary.md")
	var out, errb bytes.Buffer
	if code := run([]string{"-min", "70", "-profile", prof, "-summary", sum}, &out, &errb); code != 0 {
		t.Fatalf("70 %%: %d %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "| `internal/a` | 75.0 % |") || !strings.Contains(out.String(), "**70.0 %**") {
		t.Fatalf("tabela: %q", out.String())
	}
	if b, err := os.ReadFile(sum); err != nil || !strings.Contains(string(b), "internal/b") {
		t.Fatalf("resumo: %q %v", b, err)
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"-min", "80", "-profile", prof}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "abaixo do mínimo") {
		t.Fatalf("80 %%: %d %q", code, errb.String())
	}
	if code := run([]string{"-profile", filepath.Join(dir, "nada")}, &out, &errb); code != 1 {
		t.Fatalf("perfil ausente: %d", code)
	}
	if code := run([]string{"extra"}, &out, &errb); code != 2 {
		t.Fatalf("argumento extra: %d", code)
	}
}
