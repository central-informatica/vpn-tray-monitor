package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// sensitiveKeys são chaves de atributo cujo valor nunca vai para o log,
// mesmo que alguém passe uma string em vez de shared.Secret.
var sensitiveKeys = map[string]bool{
	"password": true, "senha": true, "secret": true, "segredo": true, "token": true, "pass": true,
}

func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if sensitiveKeys[strings.ToLower(a.Key)] {
		return slog.String(a.Key, "***")
	}
	return a
}

// New cria o logger em texto sobre w, com o nível controlado por level.
func New(w io.Writer, level *slog.LevelVar) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: redactAttr}))
}

// ParseLevel converte o logLevel da config.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return slog.LevelInfo, fmt.Errorf("nível de log desconhecido %q", s)
}

// ForVPN devolve um logger que põe vpn=<nome> em toda linha.
func ForVPN(l *slog.Logger, name string) *slog.Logger { return l.With("vpn", name) }
