package shared

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// Com o arquivo aberto pela leitura do serviço (OpenShared), quem o edita
// ainda consegue substituí-lo de forma atômica (RenameReplace) e apagá-lo; o
// leitor antigo segue vendo o conteúdo antigo. No Windows, os.Open bloquearia
// a remoção e MoveFileEx recusaria substituir o destino aberto.
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
	defer f.Close()
	tmp := filepath.Join(dir, "novo.tmp")
	if err := os.WriteFile(tmp, []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RenameReplace(tmp, path); err != nil {
		t.Fatalf("substituição com o destino aberto: %v", err)
	}
	if b, err := io.ReadAll(f); err != nil || string(b) != "v1" {
		t.Fatalf("leitor antigo deve ver o conteúdo antigo: %q %v", b, err)
	}
	if b, err := ReadFileShared(path); err != nil || string(b) != "v2" {
		t.Fatalf("leitura nova deve ver o conteúdo novo: %q %v", b, err)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("origem deveria sumir: %v", err)
	}
	g, err := OpenShared(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remoção com o arquivo aberto: %v", err)
	}
	_ = g.Close()
	if _, err := ReadFileShared(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("depois de apagado deve ser ErrNotExist: %v", err)
	}
	if err := WriteFile(path, []byte("v3"), 0o600); err != nil {
		t.Fatalf("recriar: %v", err)
	}
	if b, err := ReadFileShared(path); err != nil || string(b) != "v3" {
		t.Fatalf("leitura: %q %v", b, err)
	}
}

func TestRenameReplaceMissingSource(t *testing.T) {
	dir := t.TempDir()
	err := RenameReplace(filepath.Join(dir, "nao-existe"), filepath.Join(dir, "destino"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("origem ausente deve ser ErrNotExist: %v", err)
	}
}
