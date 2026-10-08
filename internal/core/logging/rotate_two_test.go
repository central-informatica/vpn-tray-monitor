package logging

import (
	"os"
	"path/filepath"
	"testing"
)

// Dois writers no mesmo arquivo (duas bandejas do mesmo usuário): a rotação
// acontece mesmo com o outro handle aberto (relevante no Windows, CI).
func TestRotationWithSecondWriterOpen(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vpnmon-tray.log")
	w1, err := OpenRotating(p, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w1.Close()
	w2, err := OpenRotating(p, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	for _, s := range []string{"aaaaaaaa\n", "bbbbbbbb\n"} {
		if _, err := w1.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "vpnmon-tray.1.log")); err != nil || string(b) != "aaaaaaaa\n" {
		t.Fatalf("rotação não aconteceu: %q %v", b, err)
	}
}
