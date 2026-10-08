package config

import (
	"fmt"
	"net"
	"strings"
	"unicode"
	"unicode/utf8"
)

// FieldError aponta um problema num campo, com caminho estilo JSON
// ("vpns[0].check.port"), para a interface mostrar junto ao campo.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError agrupa todos os problemas encontrados.
type ValidationError struct {
	Problems []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = p.Field + ": " + p.Message
	}
	return "config inválida: " + strings.Join(parts, "; ")
}

var logLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

// Validate confere a config inteira e devolve *ValidationError ou nil.
func Validate(c Config) error {
	var probs []FieldError
	add := func(field, format string, args ...any) {
		probs = append(probs, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
	}
	if c.Version != Version {
		add("version", "versão %d não suportada (use %d)", c.Version, Version)
	}
	if !logLevels[c.LogLevel] {
		add("logLevel", "use debug, info, warn ou error")
	}
	if c.Log.MaxSizeMB < 1 || c.Log.MaxSizeMB > 100 {
		add("log.maxSizeMB", "deve estar entre 1 e 100")
	}
	if c.Log.MaxFiles < 1 || c.Log.MaxFiles > 20 {
		add("log.maxFiles", "deve estar entre 1 e 20")
	}
	seen := map[string]int{}
	for i, v := range c.VPNs {
		prefix := fmt.Sprintf("vpns[%d].", i)
		for _, p := range ValidateVPN(v) {
			add(prefix+p.Field, "%s", p.Message)
		}
		key := NameKey(v.Name)
		if j, dup := seen[key]; dup && v.Name != "" {
			add(prefix+"name", "nome repetido (igual a vpns[%d], sem diferenciar maiúsculas)", j)
		}
		seen[key] = i
	}
	if len(probs) > 0 {
		return &ValidationError{Problems: probs}
	}
	return nil
}

// NameKey é a chave de identidade de uma VPN: o nome sem diferenciar maiúsculas.
func NameKey(name string) string { return strings.ToLower(name) }

// ValidateVPN confere uma VPN isolada. Os campos vêm sem prefixo.
func ValidateVPN(v VPN) []FieldError {
	var probs []FieldError
	add := func(field, format string, args ...any) {
		probs = append(probs, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
	}
	if n := utf8.RuneCountInString(v.Name); n < 1 || n > maxNameRunes {
		add("name", "deve ter de 1 a %d caracteres", maxNameRunes)
	} else if strings.TrimSpace(v.Name) != v.Name {
		add("name", "não pode começar nem terminar com espaço")
	} else if strings.ContainsFunc(v.Name, unicode.IsControl) {
		add("name", "não pode ter caracteres de controle")
	}
	if n := utf8.RuneCountInString(v.RasEntry); n < 1 || n > 256 {
		add("rasEntry", "deve ter de 1 a 256 caracteres")
	}
	switch v.Check.Kind {
	case CheckPing, CheckTCP:
		if err := validHost(v.Check.Host); err != "" {
			add("check.host", "%s", err)
		}
	case CheckLink:
	default:
		add("check.kind", "use ping, tcp ou link")
	}
	if v.Check.Kind == CheckTCP && (v.Check.Port < 1 || v.Check.Port > 65535) {
		add("check.port", "obrigatória em tcp, entre 1 e 65535")
	}
	if v.Check.Kind != CheckTCP && v.Check.Port != 0 {
		add("check.port", "só vale para tcp")
	}
	rng := func(field string, val, lo, hi int) {
		if val < lo || val > hi {
			add(field, "deve estar entre %d e %d", lo, hi)
		}
	}
	rng("check.timeoutSeconds", v.Check.TimeoutSeconds, 1, 30)
	rng("intervalSeconds", v.IntervalSeconds, 5, 3600)
	rng("failuresBeforeReconnect", v.FailuresBeforeReconnect, 1, 20)
	rng("graceAfterConnectSeconds", v.GraceAfterConnectSeconds, 0, 300)
	rng("connectTimeoutSeconds", v.ConnectTimeoutSeconds, 10, 300)
	if v.MaxBackoffSeconds < v.IntervalSeconds || v.MaxBackoffSeconds > 3600 {
		add("maxBackoffSeconds", "deve ser ≥ intervalSeconds e ≤ 3600")
	}
	return probs
}

// validHost aceita IPv4 literal ou nome DNS; devolve a mensagem de erro ou "".
func validHost(h string) string {
	if h == "" {
		return "obrigatório em ping e tcp"
	}
	if ip := net.ParseIP(h); ip != nil {
		if ip.To4() == nil {
			return "só IPv4 é suportado"
		}
		return ""
	}
	if len(h) > 253 {
		return "nome longo demais"
	}
	if strings.Trim(h, "0123456789.") == "" {
		return "endereço IPv4 inválido"
	}
	for _, label := range strings.Split(strings.TrimSuffix(h, "."), ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "nome de host inválido"
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return "nome de host inválido"
			}
		}
	}
	return ""
}
