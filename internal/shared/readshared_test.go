package shared

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// Com o arquivo aberto pela leitura do serviço, quem o edita ainda consegue
// apagá-lo ou trocá-lo por rename (no Windows, os.Open bloquearia ambos).
func TestOpenSharedAllowsRemoveAndReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := OpenShared(path)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "novo.tmp")
	if err := os.WriteFile(tmp, []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("troca por rename com o arquivo aberto: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remoção com o arquivo aberto: %v", err)
	}
	_ = f.Close()
	if _, err := ReadFileShared(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("depois de apagado deve ser ErrNotExist: %v", err)
	}
	if err := os.WriteFile(path, []byte("v3"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadFileShared(path); err != nil || string(b) != "v3" {
		t.Fatalf("leitura: %q %v", b, err)
	}
}
