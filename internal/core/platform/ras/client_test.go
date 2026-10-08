package ras

import (
	"testing"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

func TestStatusFromRaw(t *testing.T) {
	cases := []struct {
		in   ConnStatus
		want Status
	}{
		{ConnStatus{State: RASCS_Connected}, Status{State: StateConnected}},
		{ConnStatus{State: RASCS_Disconnected, Error: 691}, Status{StateDisconnected, 691}},
		{ConnStatus{State: 3}, Status{State: StateConnecting}},
		{ConnStatus{State: 3, Error: 809}, Status{StateDisconnected, 809}},
	}
	for _, c := range cases {
		if got := StatusFromRaw(c.in); got != c.want {
			t.Errorf("%+v → %+v, quer %+v", c.in, got, c.want)
		}
	}
}

func TestBuildDialParamsFromScratch(t *testing.T) {
	p, err := BuildDialParams(DialRequest{Entry: "Matriz", User: "ana", Password: shared.NewSecret("pw")}, 2112)
	if err != nil {
		t.Fatal(err)
	}
	if p.Size() != 2112 || p.Entry() != "Matriz" || p.User() != "ana" {
		t.Fatalf("%d %q %q", p.Size(), p.Entry(), p.User())
	}
}

func TestBuildDialParamsKeepsSavedMarker(t *testing.T) {
	base := NewDialParams(2108)
	_ = base.SetUser("dominio\\ana")
	_ = base.SetPassword("<marcador-opaco>")
	saved := NewSaved(base, true)

	p, err := BuildDialParams(DialRequest{Entry: "Matriz", Saved: saved}, 2120)
	if err != nil {
		t.Fatal(err)
	}
	if p.Size() != 2108 {
		t.Fatalf("deve manter o tamanho aceito na leitura, veio %d", p.Size())
	}
	if p.User() != "dominio\\ana" || p.PasswordFingerprint() != saved.Fingerprint() {
		t.Fatal("marcador da senha salva deve passar intacto")
	}
	p.Wipe()
	if saved.Fingerprint() != base.PasswordFingerprint() {
		t.Fatal("Wipe da cópia não pode afetar o Saved")
	}
	if NewSaved(base, false).Fingerprint() != "" {
		t.Fatal("sem senha salva a impressão é vazia")
	}
}
