package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// sensitiveParts são trechos que, presentes na chave normalizada, marcam o
// valor como segredo (vpn_password, apiKey, Authorization...), mesmo que
// alguém passe uma string em vez de shared.Secret.
var sensitiveParts = []string{
	"pass", "senha", "secret", "segredo", "token", "psk",
	"apikey", "credential", "credencial", "authorization",
}

var keyNormalizer = strings.NewReplacer("_", "", "-", "", ".", "")

func isSensitiveKey(key string) bool {
	k := keyNormalizer.Replace(strings.ToLower(key))
	for _, p := range sensitiveParts {
		if strings.Contains(k, p) {
			return true
		}
	}
	return false
}

func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() != slog.KindGroup && isSensitiveKey(a.Key) {
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
