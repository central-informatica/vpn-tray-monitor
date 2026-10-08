package fake

import (
	"errors"
	"slices"
	"testing"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

func TestFakeRASDialScript(t *testing.T) {
	f := NewRAS("Matriz")
	f.Script("Matriz", DialOutcome{Code: 691, Polls: 2}, DialOutcome{Immediate: true, Code: 623})

	h, err := f.StartDial(ras.DialRequest{Entry: "Matriz"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if st, _ := f.Status(h); st.State != ras.StateConnecting {
			t.Fatalf("consulta %d: %+v", i, st)
		}
	}
	if st, _ := f.Status(h); st != (ras.Status{State: ras.StateDisconnected, Code: 691}) {
		t.Fatalf("final: %+v", st)
	}
	if _, err := f.StartDial(ras.DialRequest{Entry: "Matriz"}); err == nil {
		t.Fatal("esperava falha imediata")
	}
	h, _ = f.StartDial(ras.DialRequest{Entry: "Matriz"}) // sem roteiro: conecta
	f.Status(h)
	if st, _ := f.Status(h); st.State != ras.StateConnected || !f.IsActive("Matriz") {
		t.Fatalf("deveria conectar: %+v", st)
	}
	f.Drop("Matriz")
	if f.IsActive("Matriz") {
		t.Fatal("Drop não derrubou")
	}
}

func TestFakeRASSaved(t *testing.T) {
	f := NewRAS("Matriz")
	if _, err := f.Saved("Outra"); err == nil {
		t.Fatal("entrada inexistente deve dar 623")
	}
	s, _ := f.Saved("Matriz")
	if s.Fingerprint() != "" {
		t.Fatal("sem senha salva")
	}
	f.SetSaved("Matriz", "ana", "m1")
	a, _ := f.Saved("Matriz")
	f.SetSaved("Matriz", "ana", "m2")
	b, _ := f.Saved("Matriz")
	if a.Fingerprint() == "" || a.Fingerprint() == b.Fingerprint() {
		t.Fatal("impressão digital deve refletir o marcador")
	}
}

func TestFakeRASHangUpAfterFailedDial(t *testing.T) {
	f := NewRAS("Matriz")
	f.Script("Matriz", DialOutcome{Code: 691})
	h, err := f.StartDial(ras.DialRequest{Entry: "Matriz"})
	if err != nil {
		t.Fatal(err)
	}
	want := ras.Status{State: ras.StateDisconnected, Code: 691}
	for i := 0; i < 2; i++ { // o handle segue válido após a falha
		if st, _ := f.Status(h); st != want && i > 0 {
			t.Fatalf("status %d: %+v", i, st)
		}
	}
	if err := f.HangUp(h); err != nil {
		t.Fatal(err)
	}
	calls := f.Calls()
	if len(calls) != 2 || calls[1] != "HangUp Matriz" {
		t.Fatalf("chamadas: %q", calls)
	}
}

func TestFakeRASActiveSortedAndErr(t *testing.T) {
	f := NewRAS("B", "A")
	f.SetActive("B")
	f.SetActive("A")
	a, _ := f.Active()
	if len(a) != 2 || a[0].Entry != "A" || a[1].Entry != "B" {
		t.Fatalf("ordem: %+v", a)
	}
	f.SetActiveErr(errors.New("x"))
	if _, err := f.Active(); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestFakeRASInjectedFailures(t *testing.T) {
	r := NewRAS("A")
	h, _ := r.StartDial(ras.DialRequest{Entry: "A"})
	r.SetStatusErr(errors.New("boom"))
	if _, err := r.Status(h); err == nil {
		t.Fatal("Status deve falhar")
	}
	r.SetStatusErr(nil)
	r.SetHangUpErr(errors.New("preso"))
	if err := r.HangUp(h); err == nil || !slices.Contains(r.Calls(), "HangUp A") {
		t.Fatalf("HangUp deve falhar e ser registrado: %v", r.Calls())
	}
	if _, err := r.Status(h); err != nil {
		t.Fatalf("handle mantido após HangUp falho: %v", err)
	}
	r.SetHangUpErr(nil)
	if err := r.HangUp(h); err != nil {
		t.Fatal(err)
	}
}
