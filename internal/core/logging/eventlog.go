package logging

import "sync"

// EventSourceName é a origem registrada no Event Log (§5.6).
const EventSourceName = "VPNMonitor"

// EventSink recebe os eventos que vão para o Event Log do Windows: início e
// parada do serviço, erros fatais, config inválida e panics recuperados.
type EventSink interface {
	Info(msg string)
	Warning(msg string)
	Error(msg string)
	Close() error
}

// NopSink descarta tudo (modo console, Linux).
type NopSink struct{}

func (NopSink) Info(string)    {}
func (NopSink) Warning(string) {}
func (NopSink) Error(string)   {}
func (NopSink) Close() error   { return nil }

// RecordingSink guarda os eventos em memória; para testes.
type RecordingSink struct {
	mu     sync.Mutex
	Events []RecordedEvent
}

// RecordedEvent é um evento gravado por RecordingSink.
type RecordedEvent struct {
	Level string
	Msg   string
}

func (r *RecordingSink) add(level, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Events = append(r.Events, RecordedEvent{level, msg})
}

func (r *RecordingSink) Info(m string)    { r.add("info", m) }
func (r *RecordingSink) Warning(m string) { r.add("warning", m) }
func (r *RecordingSink) Error(m string)   { r.add("error", m) }
func (r *RecordingSink) Close() error     { return nil }

// Snapshot devolve uma cópia dos eventos.
func (r *RecordingSink) Snapshot() []RecordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]RecordedEvent(nil), r.Events...)
}
