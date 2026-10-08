package viewmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/client"
)

// wire é o payload como vai no pipe.
func wire(t *testing.T, c Command) string {
	t.Helper()
	m, err := ipc.NewMessage("1", c.Type, c.Payload)
	if err != nil {
		t.Fatal(err)
	}
	return c.Type + " " + string(m.Payload)
}

func TestSimpleCommands(t *testing.T) {
	cases := map[string]Command{
		`checkNow {"vpn":"Matriz"}`:                   CheckNow("Matriz"),
		`reconnect {"vpn":"Matriz"}`:                  Reconnect("Matriz"),
		`resume {"vpn":"Matriz"}`:                     Resume("Matriz"),
		`setEnabled {"vpn":"Matriz","enabled":false}`: SetEnabled("Matriz", false),
		`removeVpn {"name":"Matriz"}`:                 Remove("Matriz"),
		`listRasEntries`:                              ListRasEntries(),
		`getConfig`:                                   GetConfig(),
		`logTail {"maxBytes":60000}`:                  LogTail(),
	}
	for want, c := range cases {
		if got := strings.TrimSuffix(wire(t, c), " "); got != want {
			t.Errorf("%q, esperava %q", got, want)
		}
	}
}

func TestToggleCommand(t *testing.T) {
	on := vpnItem(ipc.VPNView{Name: "Matriz", State: ipc.StateConectada, Enabled: true}, now)
	off := vpnItem(ipc.VPNView{Name: "Matriz", State: ipc.StateDesativada}, now)
	if got := wire(t, on.ToggleCommand()); got != `setEnabled {"vpn":"Matriz","enabled":false}` || on.ToggleLabel != "Desativar" {
		t.Fatalf("ativa: %s", got)
	}
	if got := wire(t, off.ToggleCommand()); got != `setEnabled {"vpn":"Matriz","enabled":true}` || off.ToggleLabel != "Ativar" {
		t.Fatalf("desativada: %s", got)
	}
}

func TestPause(t *testing.T) {
	if len(PauseChoices) != 3 || PauseChoices[0].Label != "15 min" || PauseChoices[1].Label != "1 h" || PauseChoices[2].Label != "Até retomar" {
		t.Fatalf("opções: %+v", PauseChoices)
	}
	cases := map[PauseChoice]string{
		Pause15Min:       fmt.Sprintf(`pause {"vpn":"M","untilUnix":%d}`, now.Add(15*time.Minute).Unix()),
		Pause1Hour:       fmt.Sprintf(`pause {"vpn":"M","untilUnix":%d}`, now.Add(time.Hour).Unix()),
		PauseUntilResume: `pause {"vpn":"M","untilUnix":null}`,
	}
	for c, want := range cases {
		if got := wire(t, Pause("M", c, now)); got != want {
			t.Errorf("%v: %q, esperava %q", c, got, want)
		}
	}
}

func TestAddFromEntry(t *testing.T) {
	c := AddFromEntry("  VPN Filial ", []string{"Matriz"})
	if got := wire(t, c); got != `addVpn {"config":{"name":"VPN Filial","rasEntry":"  VPN Filial ","check":{"kind":"link"}}}` {
		t.Fatalf("%s", got)
	}
	// O serviço aceita o pedido: validação local igual à dele.
	req := c.Payload.(ipc.AddVPNRequest)
	if probs := config.ValidateVPN(req.Config.Normalize()); len(probs) != 0 {
		t.Fatalf("inválido: %v", probs)
	}
	// Nome já usado (sem diferenciar maiúsculas) ganha sufixo.
	c = AddFromEntry("matriz", []string{"Matriz", "MATRIZ (2)"})
	if n := c.Payload.(ipc.AddVPNRequest).Config.Name; n != "matriz (3)" {
		t.Fatalf("duplicado: %q", n)
	}
	// Nome de entrada maior que 64 runas é cortado, inclusive com sufixo.
	long := strings.Repeat("ç", 100)
	c = AddFromEntry(long, nil)
	if n := c.Payload.(ipc.AddVPNRequest).Config.Name; utf8.RuneCountInString(n) != 64 {
		t.Fatalf("longo: %d runas", utf8.RuneCountInString(n))
	}
	c = AddFromEntry(long, []string{strings.Repeat("ç", 64)})
	n := c.Payload.(ipc.AddVPNRequest).Config.Name
	if utf8.RuneCountInString(n) != 64 || !strings.HasSuffix(n, " (2)") {
		t.Fatalf("longo duplicado: %q", n)
	}
	// Espaço na 64ª runa não fica no fim do nome.
	sp := strings.Repeat("a", 63) + " b"
	if n := AddFromEntry(sp, nil).Payload.(ipc.AddVPNRequest).Config.Name; n != strings.Repeat("a", 63) {
		t.Fatalf("espaço no corte: %q", n)
	}
	if probs := config.ValidateVPN(c.Payload.(ipc.AddVPNRequest).Config.Normalize()); len(probs) != 0 {
		t.Fatalf("inválido: %v", probs)
	}
}

func TestErrorText(t *testing.T) {
	cases := map[string]error{
		"Sem conexão com o serviço VPN Monitor.":       client.ErrNotConnected,
		"O serviço VPN Monitor não respondeu a tempo.": fmt.Errorf("x: %w", context.DeadlineExceeded),
		"VPN pausada":             &ipc.Error{Code: ipc.CodePaused, Message: "VPN pausada"},
		"erro interno do serviço": &ipc.Error{Code: ipc.CodeInternal},
		"erro do serviço (xyz)":   &ipc.Error{Code: "xyz"},
		"disco cheio":             errors.New("disco cheio"),
		"credencial já rejeitada; tente novamente em 9 min": fmt.Errorf("w: %w", &ipc.Error{Code: ipc.CodeCredentialRejected, Message: "credencial já rejeitada; tente novamente em 9 min"}),
	}
	for want, err := range cases {
		if got := ErrorText(err); got != want {
			t.Errorf("%v: %q", err, got)
		}
	}
	if ErrorText(nil) != "" {
		t.Fatal("nil")
	}
}

func TestRemoveConfirm(t *testing.T) {
	got := RemoveConfirm("Matriz")
	if !strings.Contains(got, `"Matriz"`) || !strings.Contains(got, "credential clear") {
		t.Fatalf("%q", got)
	}
}

func TestRemoveConfirmQuoting(t *testing.T) {
	got := RemoveConfirm(`A"B\`)
	if !strings.Contains(got, `credential clear "A\"B\\"`) {
		t.Fatalf("%q", got)
	}
}

// Command é serializável (o payload vai direto ao pipe).
func TestCommandPayloadsMarshal(t *testing.T) {
	for _, c := range []Command{AddFromEntry("X", nil), Pause("M", Pause15Min, now), SetEnabled("M", true)} {
		if _, err := json.Marshal(c.Payload); err != nil {
			t.Fatal(err)
		}
	}
}
