package shared

import (
	"os"
	"path/filepath"
	"testing"
)

// Com um handle de append aberto (outra bandeja), o rename do arquivo ainda
// funciona e os dois handles escrevem. No Windows, os.OpenFile falharia aqui
// (sem FILE_SHARE_DELETE); roda no CI Windows.
func TestOpenAppendSharedAllowsRename(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.log")
	f1, err := OpenAppendShared(p, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f1.Close()
	f2, err := OpenAppendShared(p, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	_, _ = f1.WriteString("um\n")
	if err := RenameReplace(p, filepath.Join(dir, "a.1.log")); err != nil {
		t.Fatalf("rename com outro handle aberto: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.1.log")); string(b) != "um\n" {
		t.Fatalf("a.1.log = %q", b)
	}
}
