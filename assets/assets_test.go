package assets

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestImageEveryIcon(t *testing.T) {
	for _, name := range []string{Conectada, Conectando, Desconectada, Inativa} {
		for _, tc := range []struct{ ask, want int }{{16, 16}, {17, 20}, {32, 32}, {40, 48}, {256, 48}, {0, 16}} {
			img, err := Image(name, tc.ask)
			if err != nil {
				t.Fatalf("%s %d: %v", name, tc.ask, err)
			}
			if got := img.Bounds().Dx(); got != tc.want {
				t.Fatalf("%s pedido %d: veio %d, esperava %d", name, tc.ask, got, tc.want)
			}
		}
	}
}

func TestICOBytes(t *testing.T) {
	b, err := ICO(Conectada)
	if err != nil || len(b) < 6 || binary.LittleEndian.Uint16(b[2:]) != 1 {
		t.Fatalf("ICO: %d bytes, %v", len(b), err)
	}
	if _, err := ICO("../go.mod"); err == nil {
		t.Fatal("nome fora da lista deveria falhar")
	}
	if _, err := Image("nenhum", 16); err == nil {
		t.Fatal("ícone inexistente deveria falhar")
	}
}

func TestParseICORejectsGarbage(t *testing.T) {
	good, _ := ICO(Inativa)
	cases := map[string][]byte{
		"curto":             {0, 0, 1},
		"tipo cursor":       append([]byte{0, 0, 2, 0}, good[4:]...),
		"sem entradas":      {0, 0, 1, 0, 0, 0},
		"diretório cortado": good[:10],
		"dados fora":        withOffset(good, 1<<20),
		"não PNG":           withData(good, []byte("BMxxxxxxxx")),
	}
	for name, b := range cases {
		if _, err := pickPNG(b, 16); err == nil {
			t.Errorf("%s: deveria falhar", name)
		}
	}
}

// withOffset aponta a primeira entrada para fora do arquivo.
func withOffset(ico []byte, off uint32) []byte {
	b := bytes.Clone(ico)
	binary.LittleEndian.PutUint32(b[6+12:], off)
	return b
}

// withData troca o conteúdo de todas as entradas por data (só cabeçalhos válidos).
func withData(ico []byte, data []byte) []byte {
	n := int(binary.LittleEndian.Uint16(ico[4:]))
	b := bytes.Clone(ico[:6+16*n])
	off := len(b)
	for i := 0; i < n; i++ {
		e := b[6+16*i:]
		binary.LittleEndian.PutUint32(e[8:], uint32(len(data)))
		binary.LittleEndian.PutUint32(e[12:], uint32(off))
	}
	return append(b, data...)
}
