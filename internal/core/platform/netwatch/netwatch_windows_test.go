//go:build windows

package netwatch

import "testing"

// Só no job Windows: o runner tem rede, então há rota padrão física.
func TestWindowsHasNetwork(t *testing.T) {
	w, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ok, err := w.HasNetwork(nil)
	if err != nil || !ok {
		t.Fatalf("runner deveria ter rede com rota padrão: %v %v", ok, err)
	}
}
