package instance

import (
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform"
)

func TestTrayMutexName(t *testing.T) {
	if TrayMutex != `Local\VPNMonitorTray` {
		t.Fatalf("nome do mutex: %q (§4.9)", TrayMutex)
	}
}

func TestAcquireOutsideWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("só fora do Windows")
	}
	if _, err := Acquire(TrayMutex); !errors.Is(err, platform.ErrNotSupported) {
		t.Fatalf("esperava ErrNotSupported: %v", err)
	}
}

// Já existe, ou acesso negado (mutex criado por uma bandeja elevada na mesma
// sessão, cuja DACL não deixa esta abri-lo): as duas são "já aberta".
func TestClassify(t *testing.T) {
	exists, denied, other := errors.New("existe"), errors.New("negado"), errors.New("outro")
	if !errors.Is(classify(exists, exists, denied), ErrAlreadyRunning) {
		t.Fatal("já existe")
	}
	if !errors.Is(classify(fmt.Errorf("x: %w", denied), exists, denied), ErrAlreadyRunning) {
		t.Fatal("acesso negado")
	}
	if got := classify(other, exists, denied); got != other {
		t.Fatalf("outro erro: %v", got)
	}
	if classify(nil, exists, denied) != nil {
		t.Fatal("nil")
	}
}
