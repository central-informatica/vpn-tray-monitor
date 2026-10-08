package shared

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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

type failingFile struct {
	*os.File
	failSync bool
}

func (f failingFile) Sync() error {
	if f.failSync {
		return errors.New("disco cheio")
	}
	return f.File.Sync()
}

func TestWriteFileFailuresKeepOriginal(t *testing.T) {
	steps := map[string]func(ops *fileOps){
		"sync": func(ops *fileOps) {
			ops.createTemp = func(dir, pattern string) (tempFile, error) {
				f, err := os.CreateTemp(dir, pattern)
				return failingFile{File: f, failSync: true}, err
			}
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
			ops := osOps
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
