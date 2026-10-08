// Package fake tem implementações em memória das interfaces de platform,
// para testar no Linux toda a lógica que depende do SO.
package fake

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

// DialOutcome roteiriza uma discagem do fake.
type DialOutcome struct {
	// Immediate faz StartDial falhar na hora com Code.
	Immediate bool
	// Code é o erro final (0 = conecta).
	Code uint32
	// Polls é quantas consultas de Status respondem "conectando" antes do
	// resultado. -1 = fica conectando para sempre (até HangUp).
	Polls int
}

type fakeDial struct {
	entry   string
	outcome DialOutcome
	polls   int
	failed  bool // falhou: o handle vale até o HangUp, como no Windows
}

// RAS é um ras.Client em memória.
type RAS struct {
	mu        sync.Mutex
	entries   []string
	active    map[string]ras.Handle
	dials     map[ras.Handle]*fakeDial
	script    map[string][]DialOutcome
	saved     map[string]*ras.Saved
	calls     []string
	next      ras.Handle
	watchers  []chan struct{}
	activeErr error
	hangUpErr error
	statusErr error
}

// SetActiveErr define o erro devolvido por Active (nil limpa).
func (f *RAS) SetActiveErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activeErr = err
}

// SetHangUpErr faz HangUp falhar com err (nil limpa); a chamada é registrada
// e o handle é mantido, como numa falha real.
func (f *RAS) SetHangUpErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hangUpErr = err
}

// SetStatusErr faz Status falhar com err (nil limpa).
func (f *RAS) SetStatusErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusErr = err
}

// NewRAS cria o fake com as entradas do catálogo.
func NewRAS(entries ...string) *RAS {
	return &RAS{
		entries: entries, active: map[string]ras.Handle{}, dials: map[ras.Handle]*fakeDial{},
		script: map[string][]DialOutcome{}, saved: map[string]*ras.Saved{}, next: 100,
	}
}

// Script enfileira resultados para as próximas discagens da entrada.
// Sem roteiro, a discagem conecta após 1 consulta.
func (f *RAS) Script(entry string, outcomes ...DialOutcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script[entry] = append(f.script[entry], outcomes...)
}

// SetActive marca a entrada como conectada (ex.: conexão já existente).
func (f *RAS) SetActive(entry string) ras.Handle {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	f.active[entry] = f.next
	return f.next
}

// Drop derruba a conexão da entrada e avisa os observadores.
func (f *RAS) Drop(entry string) {
	f.mu.Lock()
	delete(f.active, entry)
	ws := append([]chan struct{}(nil), f.watchers...)
	f.mu.Unlock()
	for _, w := range ws {
		select {
		case w <- struct{}{}:
		default:
		}
	}
}

// IsActive diz se a entrada está conectada.
func (f *RAS) IsActive(entry string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.active[entry]
	return ok
}

// SetSaved grava a "credencial salva no Windows"; password vira o marcador.
func (f *RAS) SetSaved(entry, user, password string) {
	p := ras.NewDialParams(ras.DialParamsSizes[1])
	_ = p.SetEntry(entry)
	_ = p.SetUser(user)
	_ = p.SetPassword(password)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved[entry] = ras.NewSaved(p, password != "")
}

// Calls devolve o registro de chamadas ("StartDial X", "HangUp X").
func (f *RAS) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *RAS) Entries() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.entries...), nil
}

func (f *RAS) Active() ([]ras.ActiveConn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.activeErr != nil {
		return nil, f.activeErr
	}
	var out []ras.ActiveConn
	for e, h := range f.active {
		out = append(out, ras.ActiveConn{Handle: h, Entry: e})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Entry < out[j].Entry })
	return out, nil
}

func (f *RAS) StartDial(req ras.DialRequest) (ras.Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "StartDial "+req.Entry)
	out := DialOutcome{Polls: 1}
	if q := f.script[req.Entry]; len(q) > 0 {
		out, f.script[req.Entry] = q[0], q[1:]
	}
	if out.Immediate {
		return 0, &ras.Error{Op: "RasDialW", Code: out.Code}
	}
	f.next++
	f.dials[f.next] = &fakeDial{entry: req.Entry, outcome: out, polls: out.Polls}
	return f.next, nil
}

func (f *RAS) Status(h ras.Handle) (ras.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statusErr != nil {
		return ras.Status{}, f.statusErr
	}
	d, ok := f.dials[h]
	if !ok {
		for _, ah := range f.active {
			if ah == h {
				return ras.Status{State: ras.StateConnected}, nil
			}
		}
		return ras.Status{State: ras.StateDisconnected}, nil
	}
	if d.failed {
		return ras.Status{State: ras.StateDisconnected, Code: d.outcome.Code}, nil
	}
	if d.polls != 0 {
		if d.polls > 0 {
			d.polls--
		}
		return ras.Status{State: ras.StateConnecting}, nil
	}
	if d.outcome.Code != 0 {
		d.failed = true
		return ras.Status{State: ras.StateDisconnected, Code: d.outcome.Code}, nil
	}
	delete(f.dials, h)
	f.active[d.entry] = h
	return ras.Status{State: ras.StateConnected}, nil
}

func (f *RAS) HangUp(h ras.Handle) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hangUpErr != nil {
		entry := ""
		if d, ok := f.dials[h]; ok {
			entry = d.entry
		}
		for e, ah := range f.active {
			if ah == h {
				entry = e
			}
		}
		f.calls = append(f.calls, "HangUp "+entry)
		return f.hangUpErr
	}
	entry := ""
	if d, ok := f.dials[h]; ok {
		entry = d.entry
		delete(f.dials, h)
	}
	for e, ah := range f.active {
		if ah == h {
			entry = e
			delete(f.active, e)
		}
	}
	f.calls = append(f.calls, "HangUp "+entry)
	return nil
}

func (f *RAS) Saved(entry string) (*ras.Saved, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	known := false
	for _, e := range f.entries {
		known = known || e == entry
	}
	if !known {
		return nil, &ras.Error{Op: "RasGetEntryDialParamsW", Code: ras.ERROR_CANNOT_FIND_PHONEBOOK_ENTRY}
	}
	if s, ok := f.saved[entry]; ok {
		return s, nil
	}
	p := ras.NewDialParams(ras.DialParamsSizes[1])
	_ = p.SetEntry(entry)
	return ras.NewSaved(p, false), nil
}

func (f *RAS) WatchDisconnects(ctx context.Context) (<-chan struct{}, error) {
	ch := make(chan struct{}, 1)
	f.mu.Lock()
	f.watchers = append(f.watchers, ch)
	f.mu.Unlock()
	return ch, nil
}

func (f *RAS) ErrorText(code uint32) string { return fmt.Sprintf("erro RAS %d (fake)", code) }

var _ ras.Client = (*RAS)(nil)
