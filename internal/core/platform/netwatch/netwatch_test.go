package netwatch

import (
	"context"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

func TestHasPhysicalDefault(t *testing.T) {
	cases := []struct {
		name   string
		routes []Route
		want   bool
	}{
		{"sem rotas", nil, false},
		{"ethernet com padrão", []Route{{0, 6, true}}, true},
		{"wifi com padrão", []Route{{0, 71, true}}, true},
		{"só a VPN (PPP) tem padrão", []Route{{0, 23, true}, {24, 6, true}}, false},
		{"túnel IKEv2", []Route{{0, 131, true}}, false},
		{"ethernet caída", []Route{{0, 6, false}}, false},
		{"ethernet sem padrão", []Route{{24, 6, true}}, false},
	}
	for _, c := range cases {
		if got := HasPhysicalDefault(c.routes); got != c.want {
			t.Errorf("%s: %v, quer %v", c.name, got, c.want)
		}
	}
}

func TestDebounce(t *testing.T) {
	clk := shared.NewFakeClock(time.Unix(0, 0))
	in := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := Debounce(ctx, clk, in, 2*time.Second)

	in <- struct{}{}
	if !clk.WaitForDeadline(2*time.Second, time.Second) {
		t.Fatal("espera de 2s não foi armada")
	}
	clk.Advance(1500 * time.Millisecond)
	in <- struct{}{} // rajada: reinicia a espera
	if !clk.WaitForDeadline(2*time.Second, time.Second) {
		t.Fatal("espera não foi reiniciada")
	}
	clk.Advance(1500 * time.Millisecond)
	select {
	case <-out:
		t.Fatal("aviso antes de 2s de silêncio")
	case <-time.After(20 * time.Millisecond):
	}
	clk.Advance(600 * time.Millisecond)
	select {
	case <-out:
	case <-time.After(time.Second):
		t.Fatal("aviso não chegou")
	}
	// A rajada inteira vale um único aviso.
	select {
	case <-out:
		t.Fatal("segundo aviso para a mesma rajada")
	case <-time.After(50 * time.Millisecond):
	}
}
