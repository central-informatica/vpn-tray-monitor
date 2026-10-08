//go:build windows

package view

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tailscale/walk"

	"github.com/guibsu/vpn-tray-monitor/assets"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/client"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/viewmodel"
)

// Caller envia pedidos ao serviço (client.Client serve).
type Caller interface {
	Call(ctx context.Context, typ string, payload any, out any) error
}

// Options liga a view ao cliente.
type Options struct {
	Events <-chan client.Event
	Caller Caller
	// Stats dá as contagens da decodificação tolerante para "Sobre"
	// ((*client.Client).Stats); nil = nenhuma.
	Stats      func() client.Stats
	AppVersion string
	Log        *slog.Logger
}

// callTimeout limita cada pedido do menu e das janelas.
const callTimeout = 15 * time.Second

// Tray é o estado da interface; só é tocado na thread da interface (os
// eventos chegam por app.Synchronize).
type Tray struct {
	o     Options
	app   *walk.Application
	ni    *walk.NotifyIcon
	vm    *viewmodel.VM
	icons map[viewmodel.Icon]*walk.Icon
	dpi   int // DPI em que os ícones foram gerados
	last  viewmodel.Model
	// iconOK/tipOK dizem se last.Icon/last.ToolTip já foram aplicados com
	// sucesso: sem isso, um modelo igual ao valor zero de last nunca
	// dispararia a primeira aplicação nem a nova tentativa após falha.
	iconOK, tipOK bool
	settings      *settingsWin
	logs          *logWin
}

var iconNames = map[viewmodel.Icon]string{
	viewmodel.IconGray: assets.Inativa, viewmodel.IconGreen: assets.Conectada,
	viewmodel.IconAmber: assets.Conectando, viewmodel.IconRed: assets.Desconectada,
}

// Run cria o ícone e roda o laço de mensagens até "Sair da bandeja".
// Precisa ser chamado da goroutine principal (walk trava a thread no init).
func Run(o Options) (int, error) {
	if o.Caller == nil {
		return 1, errors.New("view: Options.Caller é obrigatório")
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Stats == nil {
		o.Stats = func() client.Stats { return client.Stats{} }
	}
	app, err := walk.InitApp()
	if err != nil {
		return 1, fmt.Errorf("iniciando a interface: %w", err)
	}
	ni, err := walk.NewNotifyIcon()
	if err != nil {
		return 1, fmt.Errorf("criando o ícone da bandeja: %w", err)
	}
	t := &Tray{o: o, app: app, ni: ni, vm: viewmodel.New(o.AppVersion)}
	// Remove o ícone da bandeja antes de liberar as imagens que ele usa.
	defer func() {
		ni.Dispose()
		disposeIcons(t.icons)
	}()
	icons, dpi, err := t.loadIcons()
	if err != nil {
		return 1, err
	}
	t.icons, t.dpi = icons, dpi
	// O menu é montado na hora de abrir: um menu aberto não se redesenha
	// sozinho, e assim os tempos relativos saem sempre atuais.
	ni.ShowingContextMenu().Attach(func() bool {
		t.buildMenu(t.vm.Model(time.Now()))
		return true
	})
	m := t.vm.Model(time.Now())
	t.buildMenu(m)
	t.render(m, true)
	if err := ni.SetVisible(true); err != nil {
		return 1, fmt.Errorf("mostrando o ícone da bandeja: %w", err)
	}

	stop := make(chan struct{})
	go t.pump(stop)
	go t.tick(stop)
	code := app.Run()
	close(stop)
	return code, nil
}

// trayDPI é o DPI atual da área de notificação (96 se desconhecido).
func (t *Tray) trayDPI() int {
	if dpi := t.ni.DPI(); dpi > 0 {
		return dpi
	}
	return 96
}

// loadIcons gera os quatro ícones a partir do PNG do tamanho certo para o
// DPI atual (16 px a 96 DPI), em vez de deixar o Windows esticar um menor.
// Não troca os ícones em uso: quem chama decide quando liberar os antigos.
// Em erro, libera os que já tinha criado.
func (t *Tray) loadIcons() (map[viewmodel.Icon]*walk.Icon, int, error) {
	dpi := t.trayDPI()
	icons := map[viewmodel.Icon]*walk.Icon{}
	for k, name := range iconNames {
		img, err := assets.Image(name, 16*dpi/96)
		if err != nil {
			disposeIcons(icons)
			return nil, 0, err
		}
		ic, err := walk.NewIconFromImageForDPI(img, dpi)
		if err != nil {
			disposeIcons(icons)
			return nil, 0, fmt.Errorf("ícone %s: %w", name, err)
		}
		icons[k] = ic
	}
	return icons, dpi, nil
}

func disposeIcons(icons map[viewmodel.Icon]*walk.Icon) {
	for _, ic := range icons {
		ic.Dispose()
	}
}

// pump leva os eventos do cliente para a thread da interface, em ordem.
func (t *Tray) pump(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case ev, ok := <-t.o.Events:
			if !ok {
				return
			}
			t.app.Synchronize(func() { t.apply(ev) })
		}
	}
}

// tick atualiza tooltip e ícone a cada segundo (tempos relativos, §7) e
// entrega o balão pendente: o VM só o solta depois da janela de agregação,
// então é o tique, não a chegada do aviso, que o mostra.
func (t *Tray) tick(stop <-chan struct{}) {
	tk := time.NewTicker(time.Second)
	defer tk.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tk.C:
			t.app.Synchronize(func() {
				now := time.Now()
				if b, ok := t.vm.TakeBalloon(now); ok {
					t.showBalloon(b)
				}
				t.render(t.vm.Model(now), false)
			})
		}
	}
}

func (t *Tray) apply(ev client.Event) {
	t.vm.Apply(ev)
	t.render(t.vm.Model(time.Now()), false)
	if ev.Kind == client.EvSnapshot {
		t.refreshRasEntries()
	}
}

// render aplica ícone e tooltip quando mudam. Também é onde a troca de DPI
// é percebida (a cada tique de 1 s): o NotifyIcon do walk trata o
// WM_DPICHANGED só redesenhando o mesmo ícone, sem gancho público; então os
// ícones são gerados de novo no tamanho do DPI novo.
//
// Os ícones antigos só são liberados depois que o novo foi aplicado, e
// t.last só guarda o que deu certo: um erro é tentado de novo no próximo tique.
func (t *Tray) render(m viewmodel.Model, force bool) {
	if cur := t.trayDPI(); cur != t.dpi {
		icons, dpi, err := t.loadIcons()
		if err != nil {
			dpi = cur
			t.o.Log.Warn("gerando ícones para o DPI novo", "dpi", dpi, "erro", err)
		} else if err := t.ni.SetIcon(icons[m.Icon]); err != nil {
			disposeIcons(icons)
			t.o.Log.Warn("trocando o ícone para o DPI novo", "dpi", dpi, "erro", err)
		} else {
			old := t.icons
			t.icons, t.dpi = icons, dpi
			t.last.Icon, t.iconOK = m.Icon, true
			disposeIcons(old)
			force = true
		}
	}
	if force || !t.iconOK || m.Icon != t.last.Icon {
		if err := t.ni.SetIcon(t.icons[m.Icon]); err != nil {
			t.o.Log.Warn("trocando o ícone", "erro", err)
		} else {
			t.last.Icon, t.iconOK = m.Icon, true
		}
	}
	if force || !t.tipOK || m.ToolTip != t.last.ToolTip {
		if err := t.ni.SetToolTip(m.ToolTip); err != nil {
			t.o.Log.Warn("trocando o tooltip", "erro", err)
		} else {
			t.last.ToolTip, t.tipOK = m.ToolTip, true
		}
	}
}

func (t *Tray) showBalloon(b viewmodel.Balloon) {
	var err error
	switch b.Kind {
	case viewmodel.BalloonWarning:
		err = t.ni.ShowWarning(b.Title, b.Text)
	case viewmodel.BalloonError:
		err = t.ni.ShowError(b.Title, b.Text)
	default:
		err = t.ni.ShowInfo(b.Title, b.Text)
	}
	if err != nil {
		t.o.Log.Warn("mostrando balão", "erro", err)
	}
}

// call faz o pedido fora da thread da interface e devolve o resultado nela.
func (t *Tray) call(c viewmodel.Command, out any, done func(error)) {
	go func() {
		// Cancelado no Exit: "Sair da bandeja" não espera pedidos pendentes.
		ctx, cancel := context.WithTimeout(t.app.Context(), callTimeout)
		defer cancel()
		err := t.o.Caller.Call(ctx, c.Type, c.Payload, out)
		t.app.Synchronize(func() { done(err) })
	}()
}

// do faz um pedido do menu; erro vira caixa de aviso.
func (t *Tray) do(c viewmodel.Command) {
	t.call(c, nil, func(err error) {
		if err != nil {
			walk.MsgBox(nil, "VPN Monitor", viewmodel.ErrorText(err), walk.MsgBoxIconError|walk.MsgBoxOK)
		}
	})
}

func (t *Tray) refreshRasEntries() {
	var r ipc.RasEntries
	t.call(viewmodel.ListRasEntries(), &r, func(err error) {
		if err != nil {
			t.o.Log.Warn("listando entradas RAS", "erro", err)
			return
		}
		t.vm.SetRasEntries(r.Entries)
	})
}
