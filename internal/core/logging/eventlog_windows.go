//go:build windows

package logging

import "golang.org/x/sys/windows/svc/eventlog"

// IDs de evento: um por nível, suficiente para filtrar no Visualizador.
const (
	eventIDInfo    = 1
	eventIDWarning = 2
	eventIDError   = 3
)

type winSink struct{ l *eventlog.Log }

// OpenEventSink abre a origem VPNMonitor (registrada pelo install/MSI).
func OpenEventSink() (EventSink, error) {
	l, err := eventlog.Open(EventSourceName)
	if err != nil {
		return nil, err
	}
	return winSink{l}, nil
}

func (s winSink) Info(m string)    { _ = s.l.Info(eventIDInfo, m) }
func (s winSink) Warning(m string) { _ = s.l.Warning(eventIDWarning, m) }
func (s winSink) Error(m string)   { _ = s.l.Error(eventIDError, m) }
func (s winSink) Close() error     { return s.l.Close() }
