package shared

import (
	"fmt"
	"log/slog"
)

const redacted = "***"

// Secret carrega uma senha sem deixá-la escapar por acidente: String, %v,
// %#v, JSON e slog mostram "***". Só Reveal devolve o valor.
type Secret struct {
	b []byte
}

// NewSecret copia s para um buffer próprio, que Wipe pode zerar.
func NewSecret(s string) Secret { return Secret{b: []byte(s)} }

// Reveal devolve o valor em claro. Use só no ponto de consumo (discagem, cofre).
func (s Secret) Reveal() string { return string(s.b) }

// IsEmpty diz se não há valor.
func (s Secret) IsEmpty() bool { return len(s.b) == 0 }

// Wipe zera o buffer. Cópias de Reveal já feitas não são alcançadas.
func (s Secret) Wipe() {
	for i := range s.b {
		s.b[i] = 0
	}
}

func (s Secret) String() string               { return redacted }
func (s Secret) GoString() string             { return redacted }
func (s Secret) Format(f fmt.State, _ rune)   { _, _ = f.Write([]byte(redacted)) }
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (s Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
func (s Secret) LogValue() slog.Value         { return slog.StringValue(redacted) }
