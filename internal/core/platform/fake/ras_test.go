package fake

import (
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
