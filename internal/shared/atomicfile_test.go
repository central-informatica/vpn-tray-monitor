package shared

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteFileReplacesContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := WriteFile(p, []byte("um"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(p, []byte("dois"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "dois" {
		t.Fatalf("conteúdo = %q", got)
	}
	assertOnlyFile(t, dir, "config.json")
}

// errTransient simula um erro de compartilhamento que some sozinho.
var errTransient = errors.New("sharing violation simulada")

// testOps devolve osOps sem esperas de verdade e com errTransient como o
// único erro transitório, para o teste controlar a classificação.
func testOps() fileOps {
	ops := osOps
	ops.sleep = func(time.Duration) {}
	ops.transient = func(err error) bool { return errors.Is(err, errTransient) }
	return ops
}

type failingFile struct {
	*os.File
	failWrite bool
	failSync  bool
	failClose bool
}

func (f failingFile) Write(b []byte) (int, error) {
	if f.failWrite {
		return 0, errors.New("disco cheio")
	}
	return f.File.Write(b)
}

func (f failingFile) Sync() error {
	if f.failSync {
		return errors.New("disco cheio")
	}
	return f.File.Sync()
}

func (f failingFile) Close() error {
	if err := f.File.Close(); err != nil || !f.failClose {
		return err
	}
	return errors.New("erro ao fechar")
}

func TestWriteFileFailuresKeepOriginal(t *testing.T) {
	steps := map[string]func(ops *fileOps){
		"write": func(ops *fileOps) {
			ops.createTemp = func(dir, pattern string) (tempFile, error) {
				f, err := os.CreateTemp(dir, pattern)
				return failingFile{File: f, failWrite: true}, err
			}
		},
		"sync": func(ops *fileOps) {
			ops.createTemp = func(dir, pattern string) (tempFile, error) {
				f, err := os.CreateTemp(dir, pattern)
				return failingFile{File: f, failSync: true}, err
			}
		},
		"close": func(ops *fileOps) {
			ops.createTemp = func(dir, pattern string) (tempFile, error) {
				f, err := os.CreateTemp(dir, pattern)
				return failingFile{File: f, failClose: true}, err
			}
		},
		"chmod": func(ops *fileOps) {
			ops.chmod = func(string, os.FileMode) error { return errors.New("permissão negada") }
		},
		"rename": func(ops *fileOps) {
			ops.rename = func(string, string) error { return errors.New("acesso negado") }
		},
		"createTemp": func(ops *fileOps) {
			ops.createTemp = func(string, string) (tempFile, error) { return nil, errors.New("sem espaço") }
		},
	}
	for name, breakIt := range steps {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "config.json")
			if err := os.WriteFile(p, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			ops := testOps()
			breakIt(&ops)
			if err := writeFile(ops, p, []byte("novo"), 0o600); err == nil {
				t.Fatal("esperava erro")
			}
			got, _ := os.ReadFile(p)
			if string(got) != "original" {
				t.Fatalf("original alterado: %q", got)
			}
			assertOnlyFile(t, dir, "config.json")
		})
	}
}

func TestWriteFileRetriesTransientRename(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	ops := testOps()
	realRename := ops.rename
	calls := 0
	ops.rename = func(from, to string) error {
		calls++
		if calls <= 2 {
			return errTransient
		}
		return realRename(from, to)
	}
	if err := writeFile(ops, p, []byte("novo"), 0o600); err != nil {
		t.Fatalf("esperava sucesso após retries: %v", err)
	}
	if calls != 3 {
		t.Fatalf("rename chamado %d vezes, esperava 3", calls)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "novo" {
		t.Fatalf("conteúdo = %q", got)
	}
	assertOnlyFile(t, dir, "config.json")
}

func TestWriteFileGivesUpOnPersistentTransientRename(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	ops := testOps()
	calls := 0
	var waits []time.Duration
	ops.rename = func(string, string) error {
		calls++
		return errTransient
	}
	ops.sleep = func(d time.Duration) { waits = append(waits, d) }
	if err := writeFile(ops, p, []byte("novo"), 0o600); err == nil {
		t.Fatal("esperava erro")
	}
	if calls != renameAttempts {
		t.Fatalf("rename chamado %d vezes, esperava %d", calls, renameAttempts)
	}
	if len(waits) != renameAttempts-1 {
		t.Fatalf("esperas = %v", waits)
	}
	for i := 1; i < len(waits); i++ {
		if waits[i] <= waits[i-1] {
			t.Fatalf("esperas não crescem: %v", waits)
		}
	}
	got, _ := os.ReadFile(p)
	if string(got) != "original" {
		t.Fatalf("original alterado: %q", got)
	}
	assertOnlyFile(t, dir, "config.json")
}

func TestWriteFileDoesNotRetryPermanentRename(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	ops := testOps()
	calls := 0
	ops.rename = func(string, string) error {
		calls++
		return errors.New("erro permanente")
	}
	if err := writeFile(ops, p, []byte("novo"), 0o600); err == nil {
		t.Fatal("esperava erro")
	}
	if calls != 1 {
		t.Fatalf("rename chamado %d vezes, esperava 1", calls)
	}
	assertOnlyFile(t, dir, "config.json")
}

func assertOnlyFile(t *testing.T, dir, name string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name() != name {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("pasta deveria ter só %s, tem %v", name, names)
	}
}
