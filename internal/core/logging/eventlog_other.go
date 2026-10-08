//go:build !windows

package logging

// OpenEventSink fora do Windows não há Event Log.
func OpenEventSink() (EventSink, error) { return NopSink{}, nil }
