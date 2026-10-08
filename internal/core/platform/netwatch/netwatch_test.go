package netwatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

func TestDecideNetwork(t *testing.T) {
	monitored := []string{"VPN Matriz", " Filial SP "}
	readErr := errors.New("acesso negado")
	cases := []struct {
		name    string
		routes  []Route
		want    bool
		wantErr bool
	}{
		{"sem rotas", nil, false, false},
		{"ethernet com padrão", []Route{{PrefixLen: 0, IfType: 6, OperUp: true}}, true, false},
		{"wifi com padrão", []Route{{PrefixLen: 0, IfType: 71, OperUp: true}}, true, false},
		{"ethernet caída", []Route{{PrefixLen: 0, IfType: 6}}, false, false},
		{"ethernet sem padrão", []Route{{PrefixLen: 24, IfType: 6, OperUp: true}}, false, false},
		{"só a VPN monitorada (PPP) tem padrão",
			[]Route{{PrefixLen: 0, IfType: 23, Alias: "VPN Matriz", OperUp: true}, {PrefixLen: 24, IfType: 6, OperUp: true}}, false, false},
		{"VPN monitorada com caixa e espaços diferentes",
			[]Route{{PrefixLen: 0, IfType: 23, Alias: "  vpn MATRIZ ", OperUp: true}, {PrefixLen: 0, IfType: 23, Alias: "filial sp", OperUp: true}}, false, false},
		{"PPPoE (PPP não monitorado) conta",
			[]Route{{PrefixLen: 0, IfType: 23, Alias: "Conexão de Banda Larga", OperUp: true}}, true, false},
		{"PPPoE caído não conta",
			[]Route{{PrefixLen: 0, IfType: 23, Alias: "Conexão de Banda Larga"}}, false, false},
		{"PPP sem alias conta", []Route{{PrefixLen: 0, IfType: 23, OperUp: true}}, true, false},
		{"túnel IKEv2 (131)", []Route{{PrefixLen: 0, IfType: 131, Alias: "Outra", OperUp: true}}, false, false},
		{"WireGuard (53)", []Route{{PrefixLen: 0, IfType: 53, Alias: "wg0", OperUp: true}}, false, false},
		{"loopback", []Route{{PrefixLen: 0, IfType: 24, OperUp: true}}, false, false},
		{"interface sumiu entre as leituras", []Route{{PrefixLen: 0, Err: ErrInterfaceGone}}, false, false},
		{"uma falha, outra física ok",
			[]Route{{PrefixLen: 0, Err: readErr}, {PrefixLen: 0, IfType: 6, OperUp: true}}, true, false},
		{"uma falha, outra sumiu: todas as legíveis falharam",
			[]Route{{PrefixLen: 0, Err: readErr}, {PrefixLen: 0, Err: ErrInterfaceGone}}, false, true},
		{"todas falham", []Route{{PrefixLen: 0, Err: readErr}, {PrefixLen: 0, Err: readErr}}, false, true},
		{"falha em rota não padrão não conta", []Route{{PrefixLen: 24, Err: readErr}}, false, false},
	}
	for _, c := range cases {
		got, err := DecideNetwork(c.routes, monitored)
		if got != c.want || (err != nil) != c.wantErr {
			t.Errorf("%s: %v %v, quer %v (erro=%v)", c.name, got, err, c.want, c.wantErr)
		}
		if c.wantErr && !errors.Is(err, readErr) {
			t.Errorf("%s: erro deve embrulhar a causa: %v", c.name, err)
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
