//go:build windows

package instance

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// Só no job Windows: a segunda trava do mesmo nome falha; depois de
// liberada, volta a valer.
func TestWindowsAcquireTwice(t *testing.T) {
	name := fmt.Sprintf(`Local\VPNMonitorTrayTeste-%d-%d`, os.Getpid(), time.Now().UnixNano())
	release, err := Acquire(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(name); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("segunda instância: %v", err)
	}
	release()
	release() // idempotente
	again, err := Acquire(name)
	if err != nil {
		t.Fatalf("após liberar: %v", err)
	}
	again()
}
