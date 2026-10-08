package viewmodel

import (
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/client"
)

// late é depois da janela de agregação dos balões; o relógio do VM é fixo em now.
var late = now.Add(2 * balloonWindow)

func connected(vm *VM, vpns ...ipc.VPNView) {
	vm.clock = func() time.Time { return now }
	vm.Apply(client.Event{Kind: client.EvConn, Conn: client.Conn{State: client.Connected, ServerVersion: "2.0.0"}})
	vm.Apply(client.Event{Kind: client.EvSnapshot, Snapshot: ipc.Snapshot{VPNs: vpns, Notifications: true}})
}

func TestModelConnectionStates(t *testing.T) {
	cases := []struct {
		conn             client.Conn
		header, tip, msg string
	}{
		{client.Conn{State: client.Connecting}, "Conectando ao serviço VPN Monitor…", "VPN Monitor — conectando ao serviço", ""},
		{client.Conn{State: client.Unavailable, Message: "serviço VPN Monitor inacessível: pipe inexistente"},
			"Serviço VPN Monitor parado", "VPN Monitor — serviço parado", "serviço VPN Monitor inacessível: pipe inexistente"},
		{client.Conn{State: client.Stopping, Message: "o serviço VPN Monitor está parando"},
			"Serviço VPN Monitor parado", "VPN Monitor — serviço parado", "o serviço VPN Monitor está parando"},
		{client.Conn{State: client.Incompatible, Message: "protocolo 2; atualize o VPN Monitor"},
			"Atualize o VPN Monitor", "VPN Monitor — atualize o VPN Monitor", "protocolo 2; atualize o VPN Monitor"},
		{client.Conn{State: client.NotService, Message: "pipe servido pelo PID 4242"},
			"Conexão recusada: o pipe não é do serviço", "VPN Monitor — conexão recusada", "pipe servido pelo PID 4242"},
	}
	for _, c := range cases {
		vm := New("2.0.0")
		connected(vm, ipc.VPNView{Name: "Matriz", State: ipc.StateConectada})
		vm.Apply(client.Event{Kind: client.EvConn, Conn: c.conn})
		m := vm.Model(now)
		if m.Icon != IconGray || m.Header != c.header || m.ToolTip != c.tip || m.Notice != c.msg || m.Connected || len(m.VPNs) != 0 {
			t.Errorf("%v: %+v", c.conn.State, m)
		}
	}
}

func TestModelInitialIsConnecting(t *testing.T) {
	m := New("2.0.0").Model(now)
	if m.Connected || m.Icon != IconGray || !strings.HasPrefix(m.Header, "Conectando") {
		t.Fatalf("%+v", m)
	}
}

func TestModelHeaderAndIcon(t *testing.T) {
	v := func(name, st string) ipc.VPNView {
		return ipc.VPNView{Name: name, State: st, Enabled: st != ipc.StateDesativada}
	}
	cases := []struct {
		name   string
		vpns   []ipc.VPNView
		header string
		icon   Icon
	}{
		{"nenhuma", nil, "Nenhuma VPN configurada", IconGray},
		{"todas desativadas", []ipc.VPNView{v("A", ipc.StateDesativada)}, "Todas as VPNs estão desativadas", IconGray},
		{"uma ok", []ipc.VPNView{v("A", ipc.StateConectada)}, "● 1 de 1 VPN conectada", IconGreen},
		{"exemplo do spec", []ipc.VPNView{v("Matriz", ipc.StateConectada), v("Filial", ipc.StateReconectando), v("Backup", ipc.StatePausada)},
			"● 1 de 3 VPNs conectadas", IconAmber},
		{"degradada conta como conectada", []ipc.VPNView{v("A", ipc.StateConectada), v("B", ipc.StateDegradada)},
			"● 2 de 2 VPNs conectadas", IconAmber},
		{"vermelho vence", []ipc.VPNView{v("A", ipc.StateReconectando), v("B", ipc.StateCredencialInvalida)},
			"● 0 de 2 VPNs conectadas", IconRed},
		{"pausada não pesa", []ipc.VPNView{v("A", ipc.StateConectada), v("B", ipc.StatePausada), v("C", ipc.StateDesativada)},
			"● 1 de 2 VPNs conectadas", IconGreen},
		{"só pausadas", []ipc.VPNView{v("A", ipc.StatePausada)}, "● 0 de 1 VPN conectada", IconGray},
	}
	for _, c := range cases {
		vm := New("2.0.0")
		connected(vm, c.vpns...)
		m := vm.Model(now)
		if m.Header != c.header || m.Icon != c.icon || !m.Connected || len(m.VPNs) != len(c.vpns) {
			t.Errorf("%s: %q %v (%d itens)", c.name, m.Header, m.Icon, len(m.VPNs))
		}
	}
}

func TestModelVPNStateUpdatesAndOrder(t *testing.T) {
	vm := New("2.0.0")
	connected(vm, ipc.VPNView{Name: "Matriz", State: ipc.StateConectada}, ipc.VPNView{Name: "Filial", State: ipc.StateConectada})
	vm.Apply(client.Event{Kind: client.EvVPNState, VPN: ipc.VPNView{Name: "Filial", State: ipc.StateDesconectada}})
	vm.Apply(client.Event{Kind: client.EvVPNState, VPN: ipc.VPNView{Name: "Nova", State: ipc.StateDesconhecido}})
	m := vm.Model(now)
	var names []string
	for _, it := range m.VPNs {
		names = append(names, it.Name)
	}
	if strings.Join(names, ",") != "Matriz,Filial,Nova" || m.Icon != IconRed || !strings.Contains(m.VPNs[1].Label, "Desconectada") {
		t.Fatalf("%v %v %q", names, m.Icon, m.VPNs[1].Label)
	}
	// Snapshot novo (VPN removida) substitui a lista inteira.
	vm.Apply(client.Event{Kind: client.EvSnapshot, Snapshot: ipc.Snapshot{VPNs: []ipc.VPNView{{Name: "Matriz", State: ipc.StateConectada}}}})
	if m := vm.Model(now); len(m.VPNs) != 1 {
		t.Fatalf("snapshot: %d", len(m.VPNs))
	}
	if got := vm.Names(); len(got) != 1 || got[0] != "Matriz" {
		t.Fatalf("Names: %v", got)
	}
}

// O modelo devolvido não muda quando o VM recebe eventos depois.
func TestModelIsImmutable(t *testing.T) {
	vm := New("2.0.0")
	connected(vm, ipc.VPNView{Name: "Matriz", State: ipc.StateConectada})
	m := vm.Model(now)
	vm.Apply(client.Event{Kind: client.EvVPNState, VPN: ipc.VPNView{Name: "Matriz", State: ipc.StateDesconectada}})
	if !strings.Contains(m.VPNs[0].Label, "Conectada") || m.Icon != IconGreen {
		t.Fatalf("modelo antigo mudou: %+v", m)
	}
}

// Tempos relativos andam com o tique de 1 s, sem evento novo.
func TestModelTick(t *testing.T) {
	vm := New("2.0.0")
	connected(vm, ipc.VPNView{Name: "M", State: ipc.StateReconectando, Attempt: 1, NextAttemptUnix: ahead(3 * time.Second)})
	a := vm.Model(now).VPNs[0].Label
	b := vm.Model(now.Add(time.Second)).VPNs[0].Label
	c := vm.Model(now.Add(5 * time.Second)).VPNs[0].Label
	if a != "M  ● Reconectando (tent. 1, próxima em 3 s)" || b != "M  ● Reconectando (tent. 1, próxima em 2 s)" || c != "M  ● Reconectando (tent. 1)" {
		t.Fatalf("%q / %q / %q", a, b, c)
	}
}

func TestModelToolTip(t *testing.T) {
	vm := New("2.0.0")
	connected(vm, ipc.VPNView{Name: "Matriz", State: ipc.StateConectada}, ipc.VPNView{Name: "P&D", State: ipc.StatePausada, PausedIndefinite: true})
	if got := vm.Model(now).ToolTip; got != "VPN Monitor — 1 de 2 conectadas\nMatriz: Conectada\nP&D: Pausada até retomar" {
		t.Fatalf("tooltip: %q", got)
	}
	var many []ipc.VPNView
	for i := range 20 {
		many = append(many, ipc.VPNView{Name: strings.Repeat("😀", 5) + string(rune('A'+i)), State: ipc.StateConectada})
	}
	connected(vm, many...)
	tip := vm.Model(now).ToolTip
	if n := len(utf16.Encode([]rune(tip))); n > maxToolTip || !strings.HasSuffix(tip, "…") {
		t.Fatalf("tooltip com %d unidades: %q", n, tip)
	}
	connected(vm)
	if got := vm.Model(now).ToolTip; got != "VPN Monitor — nenhuma VPN configurada" {
		t.Fatalf("vazio: %q", got)
	}
}

func TestModelConfigStatus(t *testing.T) {
	vm := New("2.0.0")
	connected(vm, ipc.VPNView{Name: "M", State: ipc.StateConectada})
	vm.Apply(client.Event{Kind: client.EvConfigStatus, ConfigStatus: ipc.ConfigStatus{OK: false, Message: "config.json inválido: vpns[0].check.port: obrigatória em tcp"}})
	if got := vm.Model(now).Notice; got != "⚠ config.json inválido: vpns[0].check.port: obrigatória em tcp" {
		t.Fatalf("aviso: %q", got)
	}
	vm.Apply(client.Event{Kind: client.EvConfigStatus, ConfigStatus: ipc.ConfigStatus{OK: true}})
	if got := vm.Model(now).Notice; got != "" {
		t.Fatalf("corrigido: %q", got)
	}
}

// A bandeja aberta com o config.json já inválido (o serviço só publica
// configStatus quando muda) sabe pelo snapshot; reconectar não esquece o
// aviso se o snapshot não trouxer o estado, e o snapshot com OK o limpa.
func TestModelConfigStatusFromSnapshot(t *testing.T) {
	vm := New("2.0.0")
	vm.Apply(client.Event{Kind: client.EvConn, Conn: client.Conn{State: client.Connected}})
	vm.Apply(client.Event{Kind: client.EvSnapshot, Snapshot: ipc.Snapshot{
		Config: &ipc.ConfigStatus{OK: false, Message: "JSON inválido: unexpected EOF"}}})
	if got := vm.Model(now).Notice; got != "⚠ JSON inválido: unexpected EOF" {
		t.Fatalf("snapshot: %q", got)
	}
	vm.Apply(client.Event{Kind: client.EvConn, Conn: client.Conn{State: client.Unavailable}})
	vm.Apply(client.Event{Kind: client.EvConn, Conn: client.Conn{State: client.Connected}})
	vm.Apply(client.Event{Kind: client.EvSnapshot, Snapshot: ipc.Snapshot{}})
	if got := vm.Model(now).Notice; got != "⚠ JSON inválido: unexpected EOF" {
		t.Fatalf("reconexão sem estado no snapshot: %q", got)
	}
	vm.Apply(client.Event{Kind: client.EvSnapshot, Snapshot: ipc.Snapshot{Config: &ipc.ConfigStatus{OK: true}}})
	if got := vm.Model(now).Notice; got != "" {
		t.Fatalf("corrigido no snapshot: %q", got)
	}
}

// Entre o Connected e o primeiro snapshot ainda não se sabe nada das VPNs:
// "conectando", não "Nenhuma VPN configurada".
func TestModelBeforeSnapshot(t *testing.T) {
	vm := New("2.0.0")
	vm.Apply(client.Event{Kind: client.EvConn, Conn: client.Conn{State: client.Connected, ServerVersion: "2.0.0"}})
	m := vm.Model(now)
	if m.Connected || m.Icon != IconGray || m.Header != "Conectando ao serviço VPN Monitor…" || len(m.VPNs) != 0 {
		t.Fatalf("antes do snapshot: %+v", m)
	}
	vm.Apply(client.Event{Kind: client.EvSnapshot, Snapshot: ipc.Snapshot{}})
	if m := vm.Model(now); !m.Connected || m.Header != "Nenhuma VPN configurada" {
		t.Fatalf("depois do snapshot: %+v", m)
	}
}

func notice(vpn, kind string) client.Event {
	return client.Event{Kind: client.EvNotice, Notice: ipc.NoticeEvent{VPN: vpn, Kind: kind, Text: "VPN " + vpn + " " + kind}}
}

func TestBalloons(t *testing.T) {
	vm := New("2.0.0")
	connected(vm, ipc.VPNView{Name: "Matriz", State: ipc.StateConectada})
	if _, ok := vm.TakeBalloon(late); ok {
		t.Fatal("sem avisos, sem balão")
	}
	// Um aviso: o texto do serviço, com o ícone do tipo.
	single := []struct {
		kind string
		want BalloonKind
	}{{"down", BalloonWarning}, {"up", BalloonInfo}, {"credential", BalloonError}, {"config", BalloonError}, {"novo", BalloonInfo}}
	for _, c := range single {
		vm.Apply(notice("Matriz", c.kind))
		b, ok := vm.TakeBalloon(late)
		if !ok || b.Kind != c.want || b.Title != "VPN Monitor" || b.Text != "VPN Matriz "+c.kind {
			t.Errorf("%s: %+v", c.kind, b)
		}
	}
	vm.Apply(client.Event{Kind: client.EvNotice, Notice: ipc.NoticeEvent{VPN: "M", Kind: "down", Text: strings.Repeat("😀", 200)}})
	if b, _ := vm.TakeBalloon(late); len(utf16.Encode([]rune(b.Text))) > maxBalloon || !strings.HasSuffix(b.Text, "…") {
		t.Fatalf("texto longo: %d unidades", len(utf16.Encode([]rune(b.Text))))
	}
	if _, ok := vm.TakeBalloon(late); ok {
		t.Fatal("TakeBalloon esvazia a fila")
	}
	// notifications=false no snapshot: só log, sem balão.
	vm.Apply(client.Event{Kind: client.EvSnapshot, Snapshot: ipc.Snapshot{Notifications: false}})
	vm.Apply(notice("Matriz", "down"))
	if _, ok := vm.TakeBalloon(late); ok {
		t.Fatal("avisos desligados")
	}
}

// Vários avisos de uma vez (ex.: a rede caiu e levou três VPNs) viram um
// balão só, com o ícone do mais grave.
func TestBalloonsAggregate(t *testing.T) {
	cases := []struct {
		name  string
		in    []client.Event
		title string
		text  string
		kind  BalloonKind
	}{
		{"três caíram", []client.Event{notice("A", "down"), notice("B", "down"), notice("C", "down")},
			"VPN Monitor — 3 avisos", "3 VPNs caíram: A, B, C", BalloonWarning},
		{"mesma VPN duas vezes", []client.Event{notice("A", "down"), notice("A", "down")},
			"VPN Monitor — 2 avisos", "VPN A caiu", BalloonWarning},
		{"voltaram", []client.Event{notice("A", "up"), notice("B", "up")},
			"VPN Monitor — 2 avisos", "2 VPNs voltaram: A, B", BalloonInfo},
		{"mistura", []client.Event{notice("A", "down"), notice("B", "up"), notice("C", "credential"), notice("A", "up"), notice("D", "novo")},
			"VPN Monitor — 5 avisos", "Caíram: A\nCredencial rejeitada: C\nVoltaram: B, A\nOutros avisos: D", BalloonError},
	}
	for _, c := range cases {
		vm := New("2.0.0")
		connected(vm)
		for _, ev := range c.in {
			vm.Apply(ev)
		}
		b, ok := vm.TakeBalloon(late)
		if !ok || b.Title != c.title || b.Text != c.text || b.Kind != c.kind {
			t.Errorf("%s: %+v", c.name, b)
		}
	}
}

// O ciclo real da view: Apply conforme os eventos chegam, TakeBalloon no tique.
func TestBalloonWindow(t *testing.T) {
	vm := New("2.0.0")
	connected(vm)
	vm.Apply(notice("A", "down"))
	if _, ok := vm.TakeBalloon(now); ok {
		t.Fatal("cedo demais")
	}
	vm.Apply(notice("B", "down"))
	vm.Apply(notice("C", "down"))
	if _, ok := vm.TakeBalloon(now.Add(balloonWindow - time.Millisecond)); ok {
		t.Fatal("ainda dentro da janela")
	}
	b, ok := vm.TakeBalloon(now.Add(balloonWindow))
	if !ok || b.Text != "3 VPNs caíram: A, B, C" {
		t.Fatalf("%+v", b)
	}
}

func TestPendingDroppedOnDisableAndDisconnect(t *testing.T) {
	vm := New("2.0.0")
	connected(vm)
	vm.Apply(notice("A", "down"))
	vm.Apply(client.Event{Kind: client.EvSnapshot, Snapshot: ipc.Snapshot{Notifications: false}})
	if _, ok := vm.TakeBalloon(late); ok {
		t.Fatal("avisos desligados limpam a fila")
	}
	connected(vm)
	vm.Apply(notice("A", "down"))
	vm.Apply(client.Event{Kind: client.EvConn, Conn: client.Conn{State: client.Unavailable}})
	if _, ok := vm.TakeBalloon(late); ok {
		t.Fatal("queda da conexão limpa a fila")
	}
}

func TestAddEntries(t *testing.T) {
	vm := New("2.0.0")
	connected(vm)
	if m := vm.Model(now); len(m.AddEntries) != 0 || m.AddNote != "Carregando entradas RAS…" {
		t.Fatalf("antes de carregar: %+v", m)
	}
	vm.SetRasEntries([]ipc.RasEntry{{Name: "VPN Matriz", Monitored: true}, {Name: "P&D", Monitored: false}, {Name: "Filial"}})
	m := vm.Model(now)
	if len(m.AddEntries) != 2 || m.AddEntries[0] != (AddEntry{Entry: "P&D", Label: "P&&D"}) || m.AddEntries[1].Entry != "Filial" ||
		!strings.Contains(m.AddNote, "só de enlace") {
		t.Fatalf("entradas: %+v", m)
	}
	vm.SetRasEntries([]ipc.RasEntry{{Name: "VPN Matriz", Monitored: true}})
	if m := vm.Model(now); len(m.AddEntries) != 0 || !strings.HasPrefix(m.AddNote, "Nenhuma entrada RAS nova") {
		t.Fatalf("todas monitoradas: %+v", m)
	}
	// Desconectou: lista esquecida.
	vm.Apply(client.Event{Kind: client.EvConn, Conn: client.Conn{State: client.Unavailable}})
	connected(vm)
	if m := vm.Model(now); m.AddNote != "Carregando entradas RAS…" {
		t.Fatalf("após reconectar: %+v", m)
	}
}

func TestAbout(t *testing.T) {
	vm := New("2.1.0")
	if got := vm.About(client.Stats{}); got != "VPN Monitor\nBandeja: 2.1.0\nServiço: não conectado" {
		t.Fatalf("%q", got)
	}
	connected(vm)
	if got := vm.About(client.Stats{}); !strings.Contains(got, "Serviço: 2.0.0") || strings.Contains(got, "reconhecidas") {
		t.Fatalf("%q", got)
	}
	// Serviço mais novo: o que a decodificação tolerante deixou passar aparece aqui.
	got := vm.About(client.Stats{DroppedEvents: 2, UnknownFields: 5})
	if !strings.Contains(got, "2 evento(s) descartado(s), 5 com campos novos ignorados") {
		t.Fatalf("%q", got)
	}
}
