//go:build windows

package view

import (
	"time"

	"github.com/tailscale/walk"

	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/viewmodel"
)

// menuBuilder acumula erros de walk ao montar o menu (só vão ao log).
type menuBuilder struct {
	t   *Tray
	err error
}

func (b *menuBuilder) keep(err error) {
	if b.err == nil {
		b.err = err
	}
}

// item, sep e sub ignoram lista nil (submenu que não pôde ser criado).
func (b *menuBuilder) item(l *walk.ActionList, text string, enabled bool, fn func()) {
	if l == nil {
		return
	}
	a := walk.NewAction()
	b.keep(a.SetText(text))
	b.keep(a.SetEnabled(enabled))
	if fn != nil {
		a.Triggered().Attach(fn)
	}
	b.keep(l.Add(a))
}

func (b *menuBuilder) sep(l *walk.ActionList) {
	if l != nil {
		b.keep(l.Add(walk.NewSeparatorAction()))
	}
}

func (b *menuBuilder) sub(l *walk.ActionList, text string, enabled bool) *walk.ActionList {
	if l == nil {
		return nil
	}
	m, err := walk.NewMenu()
	if err != nil {
		b.keep(err)
		return nil
	}
	a, err := l.AddMenu(m)
	b.keep(err)
	if a != nil {
		b.keep(a.SetText(text))
		b.keep(a.SetEnabled(enabled))
	}
	return m.Actions()
}

// disposeMenus libera os submenus (HMENU) do menu antigo.
func disposeMenus(l *walk.ActionList) {
	for i := 0; i < l.Len(); i++ {
		if m := l.At(i).Menu(); m != nil {
			disposeMenus(m.Actions())
			m.Dispose()
		}
	}
}

// buildMenu refaz o menu inteiro a partir do modelo (§7).
func (t *Tray) buildMenu(m viewmodel.Model) {
	root := t.ni.ContextMenu().Actions()
	disposeMenus(root)
	b := &menuBuilder{t: t}
	b.keep(root.Clear())

	b.item(root, m.Header, false, nil)
	if m.Notice != "" {
		b.item(root, m.Notice, false, nil)
	}
	b.sep(root)
	for _, v := range m.VPNs {
		t.vpnMenu(b, b.sub(root, v.Label, true), v)
	}
	if len(m.VPNs) > 0 {
		b.sep(root)
	}
	add := b.sub(root, "Adicionar VPN", m.Connected)
	b.item(add, m.AddNote, false, nil)
	if len(m.AddEntries) > 0 {
		b.sep(add)
	}
	for _, e := range m.AddEntries {
		entry := e.Entry
		b.item(add, e.Label, true, func() { t.do(viewmodel.AddFromEntry(entry, t.vm.Names())) })
	}
	b.item(root, "Configurações…", m.Connected, t.openSettings)
	b.item(root, "Abrir log", m.Connected, t.openLog)
	b.sep(root)
	b.item(root, "Sobre / versão", true, func() {
		walk.MsgBox(nil, "Sobre o VPN Monitor", t.vm.About(t.o.Stats()), walk.MsgBoxIconInformation|walk.MsgBoxOK)
	})
	b.item(root, "Sair da bandeja", true, func() { t.app.Exit(0) })
	if b.err != nil {
		t.o.Log.Warn("montando o menu", "erro", b.err)
	}
}

func (t *Tray) vpnMenu(b *menuBuilder, l *walk.ActionList, v viewmodel.VPNItem) {
	name := v.Name
	for _, d := range v.Details {
		b.item(l, d, false, nil)
	}
	b.sep(l)
	b.item(l, "Verificar agora", v.CanCheck, func() { t.do(viewmodel.CheckNow(name)) })
	b.item(l, "Reconectar agora", v.CanReconnect, func() { t.do(viewmodel.Reconnect(name)) })
	pause := b.sub(l, "Pausar", v.CanPause)
	for _, c := range viewmodel.PauseChoices {
		choice := c.Choice
		b.item(pause, c.Label, true, func() { t.do(viewmodel.Pause(name, choice, time.Now())) })
	}
	b.item(l, "Retomar", v.CanResume, func() { t.do(viewmodel.Resume(name)) })
	b.sep(l)
	b.item(l, v.ToggleLabel, true, func() { t.do(v.ToggleCommand()) })
	b.item(l, "Remover…", true, func() {
		if walk.MsgBox(nil, "Remover VPN", viewmodel.RemoveConfirm(name), walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) == walk.DlgCmdYes {
			t.do(viewmodel.Remove(name))
		}
	})
}
