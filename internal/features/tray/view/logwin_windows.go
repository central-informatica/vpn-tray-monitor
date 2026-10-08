//go:build windows

package view

import (
	"strings"

	"github.com/tailscale/walk"
	d "github.com/tailscale/walk/declarative"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/viewmodel"
)

// logWin mostra o fim do log do serviço, pedido pelo pipe (o usuário não
// tem acesso à pasta de dados, §7).
type logWin struct {
	t    *Tray
	mw   *walk.MainWindow
	text *walk.TextEdit
}

func (t *Tray) openLog() {
	if t.logs != nil {
		_ = t.logs.mw.Activate()
		t.logs.load()
		return
	}
	w := &logWin{t: t}
	err := d.MainWindow{
		AssignTo: &w.mw,
		Title:    "VPN Monitor — log do serviço",
		MinSize:  d.Size{Width: 720, Height: 420},
		Layout:   d.VBox{},
		Children: []d.Widget{
			d.TextEdit{AssignTo: &w.text, ReadOnly: true, VScroll: true, Font: d.Font{Family: "Consolas", PointSize: 9}},
			d.Composite{Layout: d.HBox{MarginsZero: true}, Children: []d.Widget{
				d.HSpacer{},
				d.PushButton{Text: "Atualizar", OnClicked: w.load},
				d.PushButton{Text: "Fechar", OnClicked: func() { _ = w.mw.Close() }},
			}},
		},
	}.Create()
	if err != nil {
		walk.MsgBox(nil, "VPN Monitor", "Não foi possível abrir a janela de log: "+err.Error(), walk.MsgBoxIconError|walk.MsgBoxOK)
		return
	}
	// Sem isto, fechar a janela encerraria a bandeja inteira.
	w.mw.SetExitOnClose(false)
	w.mw.Closing().Attach(func(*bool, walk.CloseReason) { t.logs = nil })
	t.logs = w
	w.mw.Show()
	w.load()
}

func (w *logWin) load() {
	var tail ipc.LogTail
	_ = w.text.SetText("Carregando…")
	w.t.call(viewmodel.LogTail(), &tail, func(err error) {
		if w.mw.IsDisposed() {
			return
		}
		if err != nil {
			_ = w.text.SetText(viewmodel.ErrorText(err))
			return
		}
		// O controle de edição do Windows quer \r\n.
		_ = w.text.SetText(strings.ReplaceAll(strings.ReplaceAll(tail.Text, "\r\n", "\n"), "\n", "\r\n"))
		n := len([]rune(w.text.Text()))
		w.text.SetTextSelection(n, n)
		w.text.ScrollToCaret()
	})
}
