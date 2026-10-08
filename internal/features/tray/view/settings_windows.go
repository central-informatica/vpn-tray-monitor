//go:build windows

package view

import (
	"github.com/tailscale/walk"
	d "github.com/tailscale/walk/declarative"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/viewmodel"
)

// settingsWin é a janela "Configurações…" (§7): lista de VPNs à esquerda,
// formulário da selecionada à direita, opções gerais embaixo. Só copia
// valores entre os controles e o viewmodel.Form/Globals; regras e textos
// moram no view-model.
type settingsWin struct {
	t           *Tray
	mw          *walk.MainWindow
	list        *walk.ListBox
	edits       map[string]*walk.LineEdit // viewmodel.TextFields
	entry       *walk.ComboBox            // entrada RAS (editável, com sugestões)
	kind        *walk.ComboBox
	enabled     *walk.CheckBox
	errs        map[string]*walk.Label
	general     *walk.Label
	save        *walk.PushButton
	saveGlobals *walk.PushButton
	notif       *walk.CheckBox
	level       *walk.ComboBox

	cfg     config.Config
	form    viewmodel.Form
	globals viewmodel.Globals
	names   []string
}

var errorColor = walk.RGB(0xc0, 0x10, 0x10)

func (t *Tray) openSettings() {
	if t.settings != nil {
		_ = t.settings.mw.Activate()
		return
	}
	w, err := newSettingsWin(t)
	if err != nil {
		walk.MsgBox(nil, "VPN Monitor", "Não foi possível abrir as configurações: "+err.Error(), walk.MsgBoxIconError|walk.MsgBoxOK)
		return
	}
	t.settings = w
	w.mw.Show()
	w.reload("")
}

func newSettingsWin(t *Tray) (*settingsWin, error) {
	w := &settingsWin{t: t, edits: map[string]*walk.LineEdit{}, errs: map[string]*walk.Label{}}
	var grid []d.Widget
	editPtrs := map[string]**walk.LineEdit{}
	errPtrs := map[string]**walk.Label{}
	for _, f := range viewmodel.FormFields {
		grid = append(grid, d.Label{Text: viewmodel.FieldLabels[f]})
		switch f {
		case viewmodel.FieldRasEntry:
			grid = append(grid, d.ComboBox{AssignTo: &w.entry, Editable: true})
		case viewmodel.FieldKind:
			grid = append(grid, d.ComboBox{AssignTo: &w.kind, Model: viewmodel.CheckKinds, OnCurrentIndexChanged: w.kindChanged})
		case viewmodel.FieldEnabled:
			grid = append(grid, d.CheckBox{AssignTo: &w.enabled})
		default: // viewmodel.TextFields
			p := new(*walk.LineEdit)
			editPtrs[f] = p
			grid = append(grid, d.LineEdit{AssignTo: p})
		}
		p := new(*walk.Label)
		errPtrs[f] = p
		grid = append(grid, d.Label{AssignTo: p, TextColor: errorColor})
	}
	err := d.MainWindow{
		AssignTo: &w.mw,
		Title:    "VPN Monitor — Configurações",
		MinSize:  d.Size{Width: 760, Height: 520},
		Layout:   d.VBox{},
		Children: []d.Widget{
			d.Composite{Layout: d.HBox{MarginsZero: true}, Children: []d.Widget{
				d.Composite{Layout: d.VBox{MarginsZero: true}, MaxSize: d.Size{Width: 200}, Children: []d.Widget{
					d.ListBox{AssignTo: &w.list, OnCurrentIndexChanged: w.selected},
					d.PushButton{Text: "Nova VPN", OnClicked: w.newVPN},
				}},
				d.Composite{Layout: d.Grid{Columns: 3, MarginsZero: true}, Children: grid},
			}},
			d.Label{AssignTo: &w.general, TextColor: errorColor},
			d.Composite{Layout: d.HBox{MarginsZero: true}, Children: []d.Widget{
				d.HSpacer{},
				// Desabilitados até o getConfig concluir: salvar antes gravaria
				// um formulário vazio por cima da VPN.
				d.PushButton{AssignTo: &w.save, Text: "Salvar VPN", Enabled: false, OnClicked: w.saveVPN},
			}},
			d.GroupBox{Title: "Opções gerais", Layout: d.Grid{Columns: 3}, Children: []d.Widget{
				d.CheckBox{AssignTo: &w.notif, Text: "Mostrar avisos (balões)", ColumnSpan: 3},
				d.Label{Text: "Nível de log"},
				d.ComboBox{AssignTo: &w.level, Model: viewmodel.LogLevels},
				d.PushButton{AssignTo: &w.saveGlobals, Text: "Salvar opções gerais", Enabled: false, OnClicked: w.saveGlobalOptions},
			}},
		},
	}.Create()
	if err != nil {
		return nil, err
	}
	for f, p := range editPtrs {
		w.edits[f] = *p
	}
	for f, p := range errPtrs {
		w.errs[f] = *p
	}
	// Sem isto, fechar a janela encerraria a bandeja inteira.
	w.mw.SetExitOnClose(false)
	w.mw.Closing().Attach(func(*bool, walk.CloseReason) { t.settings = nil })
	return w, nil
}

// setSaving liga ou desliga os dois botões de salvar.
func (w *settingsWin) setSaving(enabled bool) {
	w.save.SetEnabled(enabled)
	w.saveGlobals.SetEnabled(enabled)
}

// reload pede a config ao serviço e seleciona a VPN dada ("" = a primeira).
// Os botões de salvar só voltam depois da resposta.
func (w *settingsWin) reload(selectName string) {
	w.setSaving(false)
	var cfg config.Config
	w.t.call(viewmodel.GetConfig(), &cfg, func(err error) {
		if w.mw.IsDisposed() {
			return
		}
		if err != nil {
			_ = w.general.SetText(viewmodel.ErrorText(err))
			return
		}
		w.cfg = cfg
		w.names = viewmodel.VPNNames(cfg)
		w.globals = viewmodel.GlobalsFrom(cfg)
		w.notif.SetChecked(w.globals.Notifications)
		_ = w.level.SetCurrentIndex(w.globals.LevelIndex())
		_ = w.list.SetModel(w.names)
		if i := viewmodel.SelectIndex(w.names, selectName); i < 0 {
			w.newVPN()
		} else {
			_ = w.list.SetCurrentIndex(i)
			w.selected()
		}
		w.setSaving(true)
	})
	var r ipc.RasEntries
	w.t.call(viewmodel.ListRasEntries(), &r, func(err error) {
		if err != nil || w.mw.IsDisposed() {
			return
		}
		text := w.entry.Text()
		_ = w.entry.SetModel(viewmodel.EntryNames(r.Entries))
		_ = w.entry.SetText(text)
	})
}

func (w *settingsWin) selected() {
	i := w.list.CurrentIndex()
	if i < 0 || i >= len(w.names) {
		return
	}
	if v, ok := viewmodel.FindVPN(w.cfg, w.names[i]); ok {
		w.show(viewmodel.FormFrom(v))
	}
}

func (w *settingsWin) newVPN() {
	_ = w.list.SetCurrentIndex(-1)
	w.show(viewmodel.NewForm())
}

// show copia o formulário para os controles e limpa os erros.
func (w *settingsWin) show(f viewmodel.Form) {
	w.form = f
	for _, field := range viewmodel.TextFields {
		_ = w.edits[field].SetText(f.Text(field))
	}
	_ = w.entry.SetText(f.Text(viewmodel.FieldRasEntry))
	_ = w.kind.SetCurrentIndex(f.KindIndex())
	w.enabled.SetChecked(f.Enabled)
	_ = w.edits[viewmodel.FieldName].SetReadOnly(f.NameReadOnly())
	w.kindChanged()
	w.showErrors(nil, "")
}

// read copia os controles de volta para o formulário.
func (w *settingsWin) read() viewmodel.Form {
	f := w.form
	for _, field := range viewmodel.TextFields {
		f = f.WithText(field, w.edits[field].Text())
	}
	f = f.WithText(viewmodel.FieldRasEntry, w.entry.Text())
	f = f.WithKindIndex(w.kind.CurrentIndex())
	f.Enabled = w.enabled.Checked()
	return f
}

// kindChanged habilita host e porta conforme o tipo de verificação.
func (w *settingsWin) kindChanged() {
	if len(w.edits) == 0 { // evento disparado durante a criação da janela
		return
	}
	f := w.read()
	w.edits[viewmodel.FieldHost].SetEnabled(f.HostEnabled())
	w.edits[viewmodel.FieldPort].SetEnabled(f.PortEnabled())
}

func (w *settingsWin) showErrors(fields viewmodel.FieldErrors, general string) {
	for f, l := range w.errs {
		_ = l.SetText(fields[f])
	}
	_ = w.general.SetText(general)
}

func (w *settingsWin) saveVPN() {
	f := w.read()
	c, local := f.Command()
	if len(local) > 0 {
		w.showErrors(local, "Corrija os campos marcados.")
		return
	}
	w.setSaving(false)
	w.t.call(c, nil, func(err error) {
		if w.mw.IsDisposed() {
			return
		}
		if err != nil {
			w.setSaving(true)
			w.showErrors(viewmodel.ServiceErrors(err))
			return
		}
		w.reload(f.Name)
	})
}

func (w *settingsWin) saveGlobalOptions() {
	g := w.globals.WithLevelIndex(w.level.CurrentIndex())
	g.Notifications = w.notif.Checked()
	w.setSaving(false)
	w.t.call(g.Command(), nil, func(err error) {
		if w.mw.IsDisposed() {
			return
		}
		if err != nil {
			w.setSaving(true)
			_, msg := viewmodel.ServiceErrors(err)
			_ = w.general.SetText(msg)
			return
		}
		_ = w.general.SetText("")
		w.reload(w.form.Name)
	})
}
