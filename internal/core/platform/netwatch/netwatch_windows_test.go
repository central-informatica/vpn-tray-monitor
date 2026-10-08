//go:build windows

package netwatch

import "testing"

// Só no job Windows: o runner tem rede, então há rota padrão física.
func TestWindowsHasPhysicalDefaultRoute(t *testing.T) {
	w, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ok, err := w.HasPhysicalDefaultRoute()
	if err != nil || !ok {
		t.Fatalf("runner deveria ter rota padrão física: %v %v", ok, err)
	}
}
