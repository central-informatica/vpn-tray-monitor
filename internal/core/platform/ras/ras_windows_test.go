//go:build windows

package ras

import (
	"context"
	"os"
	"testing"
	"time"
)

// Roda só no job Windows do CI: confirma que a API aceita os tamanhos de
// estrutura calculados (632 aqui significa layout errado).
func TestWindowsActiveAcceptsLayout(t *testing.T) {
	c, err := NewClient()
	if err != nil {
		t.Skipf("RAS indisponível: %v", err)
	}
	if _, err := c.Active(); err != nil {
		var re *Error
		if asError(err, &re) && re.Code == ERROR_RASMAN_CANNOT_INITIALIZE {
			t.Skip("serviço RasMan indisponível neste runner (o e2e do Marco C sonda isso explicitamente)")
		}
		t.Fatalf("RasEnumConnectionsW (632 = layout errado): %v", err)
	}
	if _, err := c.Entries(); err != nil {
		t.Fatalf("lendo o catálogo: %v", err)
	}
}

func TestWindowsSavedMissingEntryIs623(t *testing.T) {
	c, err := NewClient()
	if err != nil {
		t.Skipf("RAS indisponível: %v", err)
	}
	_, err = c.Saved("vpnmon-entrada-que-nao-existe")
	var re *Error
	if !asError(err, &re) || re.Code != ERROR_CANNOT_FIND_PHONEBOOK_ENTRY {
		t.Fatalf("esperava 623, veio %v", err)
	}
	if c.ErrorText(691) == "" {
		t.Fatal("ErrorText vazio")
	}
}

// Opcional, com VPN real: VPNMON_REAL_ENTRY=<entrada> disca e desliga.
func TestWindowsRealDialAndHangUp(t *testing.T) {
	entry := os.Getenv("VPNMON_REAL_ENTRY")
	if entry == "" {
		t.Skip("defina VPNMON_REAL_ENTRY para discar de verdade")
	}
	c, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := c.Saved(entry)
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.StartDial(DialRequest{Entry: entry, Saved: saved})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.HangUp(h) }) // também após falha: o handle segue válido até o HangUp
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for {
		if ctx.Err() != nil {
			t.Fatal("tempo esgotado sem conectar")
		}
		st, err := c.Status(h)
		if err != nil {
			t.Fatal(err)
		}
		if st.State == StateConnected {
			break
		}
		if st.State == StateDisconnected {
			t.Fatalf("discagem falhou: %d %s", st.Code, c.ErrorText(st.Code))
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err := c.HangUp(h); err != nil {
		t.Fatal(err)
	}
}
