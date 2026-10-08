package viewmodel

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/client"
)

// Limites do Windows, em unidades UTF-16 (szTip[128], szInfo[256], com NUL).
const (
	maxToolTip = 127
	maxBalloon = 255
	maxNotice  = 96

	// balloonWindow é quanto o aviso pendente mais antigo espera antes de sair:
	// avisos que chegam juntos (a rede caiu e levou várias VPNs) viram um balão.
	balloonWindow = 1500 * time.Millisecond
)

// BalloonKind escolhe o ícone do balão.
type BalloonKind int

const (
	BalloonInfo BalloonKind = iota
	BalloonWarning
	BalloonError
)

// Balloon é um aviso a mostrar com NotifyIcon.ShowMessage (toast no 10/11).
type Balloon struct {
	Title string
	Text  string
	Kind  BalloonKind
}

// AddEntry é uma entrada RAS ainda não monitorada (submenu "Adicionar VPN").
type AddEntry struct {
	Entry string // nome real da entrada
	Label string // escapado para menu
}

// Model é o modelo de tela. Cada chamada a VM.Model devolve um valor novo,
// que não muda depois (o VM não guarda referências a ele).
type Model struct {
	Icon    Icon
	ToolTip string
	// Header é a primeira linha do menu (informativa); Notice, se não vazia,
	// a segunda (motivo da desconexão ou config inválida).
	Header string
	Notice string
	// Connected habilita os itens que dependem do serviço.
	Connected  bool
	VPNs       []VPNItem
	AddEntries []AddEntry
	AddNote    string
}

// VM acumula o estado recebido do cliente. Não é seguro para uso
// concorrente: a view o usa só na thread da interface.
type VM struct {
	appVersion    string
	conn          client.Conn
	hasSnapshot   bool // depois de Connected, até o snapshot, ainda "conectando"
	vpns          []ipc.VPNView
	notifications bool
	cfg           ipc.ConfigStatus
	ras           []ipc.RasEntry
	rasLoaded     bool
	pending       []ipc.NoticeEvent
	pendingSince  time.Time // chegada do aviso pendente mais antigo
	clock         func() time.Time
}

// New cria o VM no estado "conectando".
func New(appVersion string) *VM {
	return &VM{appVersion: appVersion, conn: client.Conn{State: client.Connecting}, cfg: ipc.ConfigStatus{OK: true}, clock: time.Now}
}

// Apply incorpora um evento do cliente.
func (vm *VM) Apply(ev client.Event) {
	switch ev.Kind {
	case client.EvConn:
		vm.conn = ev.Conn
		// Ao conectar (de novo) e ao perder a conexão, a lista de VPNs deixa
		// de valer; o snapshot que segue o Connected repõe. O estado da config
		// fica: o snapshot o traz de novo (Snapshot.Config) e, se não trouxer,
		// o último aviso conhecido continua valendo.
		vm.hasSnapshot = false
		vm.vpns, vm.ras, vm.rasLoaded = nil, nil, false
		vm.pending = nil
	case client.EvSnapshot:
		vm.hasSnapshot = true
		vm.vpns = slices.Clone(ev.Snapshot.VPNs)
		vm.notifications = ev.Snapshot.Notifications
		if !vm.notifications {
			vm.pending = nil
		}
		if ev.Snapshot.Config != nil {
			vm.cfg = *ev.Snapshot.Config
		}
	case client.EvVPNState:
		i := slices.IndexFunc(vm.vpns, func(v ipc.VPNView) bool { return v.Name == ev.VPN.Name })
		if i < 0 {
			vm.vpns = append(vm.vpns, ev.VPN)
		} else {
			vm.vpns[i] = ev.VPN
		}
	case client.EvNotice:
		if vm.notifications {
			if len(vm.pending) == 0 {
				vm.pendingSince = vm.clock()
			}
			vm.pending = append(vm.pending, ev.Notice)
		}
	case client.EvConfigStatus:
		vm.cfg = ev.ConfigStatus
	}
}

func balloonKind(noticeKind string) BalloonKind {
	switch noticeKind {
	case ipc.NoticeDown:
		return BalloonWarning
	case ipc.NoticeCredential, ipc.NoticeConfig:
		return BalloonError
	}
	return BalloonInfo
}

// noticeGroups é a ordem e os textos dos grupos de um balão agregado: frase
// com várias VPNs, frase com uma, e rótulo quando há tipos misturados. O
// último grupo recebe os tipos que esta bandeja não conhece.
var noticeGroups = []struct{ kind, many, one, label string }{
	{ipc.NoticeDown, "%d VPNs caíram: %s", "VPN %s caiu", "Caíram"},
	{ipc.NoticeCredential, "%d VPNs com credencial rejeitada: %s", "VPN %s: credencial rejeitada", "Credencial rejeitada"},
	{ipc.NoticeConfig, "%d VPNs com erro de configuração: %s", "VPN %s: erro de configuração", "Erro de configuração"},
	{ipc.NoticeUp, "%d VPNs voltaram: %s", "VPN %s voltou", "Voltaram"},
	{"", "%d VPNs com avisos: %s", "Aviso da VPN %s", "Outros avisos"},
}

func noticeGroup(kind string) int {
	for i, g := range noticeGroups[:len(noticeGroups)-1] {
		if g.kind == kind {
			return i
		}
	}
	return len(noticeGroups) - 1
}

// TakeBalloon junta os avisos pendentes num balão só e esvazia a fila.
// Só entrega quando o aviso mais antigo já espera balloonWindow (ok=false se
// não há ou ainda é cedo): a view o chama a cada tique de 1 s, não após cada
// Apply, e é isso que permite agregar uma rajada. Um aviso sai com o texto do serviço; vários (ex.: a
// rede caiu e levou três VPNs) viram um resumo com o ícone do mais grave, em
// vez de uma rajada de toasts que o Windows enfileiraria.
func (vm *VM) TakeBalloon(now time.Time) (Balloon, bool) {
	if len(vm.pending) == 0 || now.Sub(vm.pendingSince) < balloonWindow {
		return Balloon{}, false
	}
	p := vm.pending
	vm.pending = nil
	switch len(p) {
	case 0:
		return Balloon{}, false
	case 1:
		return Balloon{Title: "VPN Monitor", Text: truncateUTF16(p[0].Text, maxBalloon), Kind: balloonKind(p[0].Kind)}, true
	}
	b := Balloon{Title: fmt.Sprintf("VPN Monitor — %d avisos", len(p))}
	names := make([][]string, len(noticeGroups))
	for _, n := range p {
		i := noticeGroup(n.Kind)
		if !slices.Contains(names[i], n.VPN) {
			names[i] = append(names[i], n.VPN)
		}
		b.Kind = max(b.Kind, balloonKind(n.Kind))
	}
	var used []int
	for i := range names {
		if len(names[i]) > 0 {
			used = append(used, i)
		}
	}
	if len(used) == 1 {
		g, list := noticeGroups[used[0]], names[used[0]]
		if len(list) == 1 {
			b.Text = fmt.Sprintf(g.one, list[0])
		} else {
			b.Text = fmt.Sprintf(g.many, len(list), strings.Join(list, ", "))
		}
	} else {
		lines := make([]string, len(used))
		for k, i := range used {
			lines[k] = noticeGroups[i].label + ": " + strings.Join(names[i], ", ")
		}
		b.Text = strings.Join(lines, "\n")
	}
	b.Text = truncateUTF16(b.Text, maxBalloon)
	return b, true
}

// SetRasEntries guarda a resposta de listRasEntries.
func (vm *VM) SetRasEntries(entries []ipc.RasEntry) {
	vm.ras, vm.rasLoaded = slices.Clone(entries), true
}

// Names são os nomes das VPNs conhecidas, na ordem do serviço.
func (vm *VM) Names() []string {
	out := make([]string, len(vm.vpns))
	for i, v := range vm.vpns {
		out[i] = v.Name
	}
	return out
}

// About é o texto de "Sobre / versão". stats são as contagens do cliente:
// mensagens de um serviço mais novo que esta bandeja não entendeu inteiras.
func (vm *VM) About(stats client.Stats) string {
	svc := "não conectado"
	if vm.conn.State == client.Connected {
		svc = vm.conn.ServerVersion
	}
	s := fmt.Sprintf("VPN Monitor\nBandeja: %s\nServiço: %s", vm.appVersion, svc)
	if stats.DroppedEvents > 0 || stats.UnknownFields > 0 {
		s += fmt.Sprintf("\n\nMensagens do serviço não reconhecidas: %d evento(s) descartado(s), %d com campos novos ignorados.\n"+
			"Atualize a bandeja para a versão do serviço.", stats.DroppedEvents, stats.UnknownFields)
	}
	return s
}

// Model monta o modelo de tela para o instante now (tique de 1 s).
func (vm *VM) Model(now time.Time) Model {
	if vm.conn.State != client.Connected || !vm.hasSnapshot {
		return vm.disconnected()
	}
	m := Model{Connected: true, Icon: IconGray}
	total, up := 0, 0
	tip := []string{}
	for _, v := range vm.vpns {
		m.VPNs = append(m.VPNs, vpnItem(v, now))
		tip = append(tip, v.Name+": "+shortState(v, now))
		if v.State != ipc.StateDesativada {
			total++
		}
		if v.State == ipc.StateConectada || v.State == ipc.StateDegradada {
			up++
		}
		if !inactive(v.State) && severity(v.State) > m.Icon {
			m.Icon = severity(v.State)
		}
	}
	switch {
	case len(vm.vpns) == 0:
		m.Header = "Nenhuma VPN configurada"
		m.ToolTip = "VPN Monitor — nenhuma VPN configurada"
	case total == 0:
		m.Header = "Todas as VPNs estão desativadas"
		m.ToolTip = "VPN Monitor — todas as VPNs desativadas"
	default:
		if total == 1 {
			m.Header = fmt.Sprintf("● %d de 1 VPN conectada", up)
		} else {
			m.Header = fmt.Sprintf("● %d de %d VPNs conectadas", up, total)
		}
		head := fmt.Sprintf("VPN Monitor — %d de %d conectadas", up, total)
		m.ToolTip = truncateUTF16(strings.Join(append([]string{head}, tip...), "\n"), maxToolTip)
	}
	if !vm.cfg.OK {
		m.Notice = MenuEscape("⚠ " + Truncate(vm.cfg.Message, maxNotice))
	}
	m.AddEntries, m.AddNote = vm.addEntries()
	return m
}

func (vm *VM) disconnected() Model {
	m := Model{Icon: IconGray, Notice: MenuEscape(Truncate(vm.conn.Message, maxNotice))}
	switch vm.conn.State {
	case client.Connecting, client.Connected: // Connected sem snapshot ainda
		m.Header, m.ToolTip = "Conectando ao serviço VPN Monitor…", "VPN Monitor — conectando ao serviço"
	case client.Incompatible:
		m.Header, m.ToolTip = "Atualize o VPN Monitor", "VPN Monitor — atualize o VPN Monitor"
	case client.NotService:
		m.Header, m.ToolTip = "Conexão recusada: o pipe não é do serviço", "VPN Monitor — conexão recusada"
	default: // Unavailable, Stopping
		m.Header, m.ToolTip = "Serviço VPN Monitor parado", "VPN Monitor — serviço parado"
	}
	return m
}

func (vm *VM) addEntries() ([]AddEntry, string) {
	if !vm.rasLoaded {
		return nil, "Carregando entradas RAS…"
	}
	var out []AddEntry
	for _, e := range vm.ras {
		if !e.Monitored {
			out = append(out, AddEntry{Entry: e.Name, Label: MenuEscape(Truncate(e.Name, maxDetail))})
		}
	}
	if len(out) == 0 {
		return nil, "Nenhuma entrada RAS nova no catálogo de todos os usuários"
	}
	return out, "Cria com verificação só de enlace; ajuste em Configurações…"
}
