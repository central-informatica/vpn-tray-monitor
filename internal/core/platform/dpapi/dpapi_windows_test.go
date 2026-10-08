//go:build windows

package dpapi

import (
	"bytes"
	"testing"
)

// Só no job Windows: ida e volta real e entropia errada recusada.
func TestWindowsRoundTrip(t *testing.T) {
	p := New()
	blob, err := p.Protect([]byte("segredo"), []byte("entropia"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("segredo")) {
		t.Fatal("blob contém o texto em claro")
	}
	got, err := p.Unprotect(blob, []byte("entropia"))
	if err != nil || string(got) != "segredo" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := p.Unprotect(blob, []byte("outra")); err == nil {
		t.Fatal("entropia errada deveria falhar")
	}
}
