package view

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No walk, fechar uma MainWindow encerra o aplicativo, a menos que se chame
// SetExitOnClose(false). Toda janela da bandeja (Configurações, log) precisa
// disso, senão fechar a janela some com o ícone. A view só compila no Windows;
// esta conferência lê o código-fonte e roda no Linux.
func TestEveryWindowKeepsTrayAlive(t *testing.T) {
	files, err := filepath.Glob("*_windows.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("fontes da view: %v %v", files, err)
	}
	windows := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		created := strings.Count(src, "d.MainWindow{")
		kept := strings.Count(src, ".SetExitOnClose(false)")
		if created != kept {
			t.Errorf("%s: %d janela(s) e %d SetExitOnClose(false)", f, created, kept)
		}
		windows += created
	}
	if windows < 2 {
		t.Fatalf("esperava as janelas de Configurações e de log, achei %d", windows)
	}
}
