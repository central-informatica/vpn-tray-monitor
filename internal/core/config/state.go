package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// State é o conteúdo de state.json, por VPN (chave = NameKey): pausas e
// rejeições recentes de credencial. Campos novos são opcionais: o
// state.json de versões anteriores continua legível.
type State struct {
	Pauses map[string]Pause `json:"pauses"`
	// Rejections guarda só instantes, nunca impressão digital ou hash da
	// credencial (§4.7): na partida, uma rejeição com menos de 15 min impede
	// discar sozinho até a janela vencer.
	Rejections map[string]Rejection `json:"rejections,omitempty"`
}

// Rejection é a última rejeição de credencial de uma VPN (Unix, segundos).
type Rejection struct {
	RejectedAtUnix    int64 `json:"rejectedAtUnix"`
	LastManualTryUnix int64 `json:"lastManualTryUnix,omitempty"`
}

// Pause é uma pausa temporária (UntilUnix) ou indefinida.
type Pause struct {
	UntilUnix  int64 `json:"untilUnix,omitempty"`
	Indefinite bool  `json:"indefinite,omitempty"`
}

// CorruptStateError avisa que state.json estava corrompido e foi isolado.
type CorruptStateError struct {
	MovedTo string
	Cause   error
}

func (e *CorruptStateError) Error() string {
	return fmt.Sprintf("state.json corrompido (%v); movido para %s", e.Cause, e.MovedTo)
}

// LoadState lê state.json. Ausente → vazio, sem erro. Corrompido → renomeia
// para state.json.corrompido-<data>, devolve vazio e *CorruptStateError
// (não fatal: o chamador registra e segue).
func LoadState(path string, now time.Time) (State, error) {
	empty := State{Pauses: map[string]Pause{}, Rejections: map[string]Rejection{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, err
	}
	var s State
	if derr := DecodeStrict(data, &s); derr != nil {
		moved := path + ".corrompido-" + now.Format("20060102-150405")
		if rerr := os.Rename(path, moved); rerr != nil {
			return empty, fmt.Errorf("isolando state.json corrompido: %w", rerr)
		}
		return empty, &CorruptStateError{MovedTo: moved, Cause: derr}
	}
	if s.Pauses == nil {
		s.Pauses = map[string]Pause{}
	}
	if s.Rejections == nil {
		s.Rejections = map[string]Rejection{}
	}
	return s, nil
}

// SaveState grava state.json de forma atômica.
func SaveState(path string, s State) error {
	if s.Pauses == nil {
		s.Pauses = map[string]Pause{}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return shared.WriteFile(path, append(data, '\n'), 0o600)
}
