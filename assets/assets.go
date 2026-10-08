// Package assets embute os ícones da bandeja. Os .ico são gerados por
// `go run ./tools/geniconos` (paleta no código) e versionados; cada um traz
// PNGs de 16, 20, 24, 32 e 48 px.
package assets

import (
	"bytes"
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"slices"
)

//go:embed *.ico
var files embed.FS

// Ícones por cor: verde, âmbar, vermelho e cinza (§7).
const (
	Conectada    = "conectada"
	Conectando   = "conectando"
	Desconectada = "desconectada"
	Inativa      = "inativa"
)

var names = []string{Conectada, Conectando, Desconectada, Inativa}

// ICO devolve o arquivo .ico do ícone.
func ICO(name string) ([]byte, error) {
	if !slices.Contains(names, name) {
		return nil, fmt.Errorf("ícone %q desconhecido", name)
	}
	return files.ReadFile(name + ".ico")
}

// Image devolve a imagem do ícone com o menor lado ≥ size (a maior, se
// nenhuma alcança): a bandeja pede o tamanho do DPI atual.
func Image(name string, size int) (image.Image, error) {
	b, err := ICO(name)
	if err != nil {
		return nil, err
	}
	return pickPNG(b, size)
}

// pickPNG lê o diretório do .ico (ICONDIR + ICONDIRENTRY de 16 bytes) e
// decodifica a entrada escolhida, que tem de ser PNG.
func pickPNG(b []byte, size int) (image.Image, error) {
	if len(b) < 6 || binary.LittleEndian.Uint16(b[0:]) != 0 || binary.LittleEndian.Uint16(b[2:]) != 1 {
		return nil, errors.New("não é um .ico")
	}
	n := int(binary.LittleEndian.Uint16(b[4:]))
	if n == 0 || len(b) < 6+16*n {
		return nil, errors.New(".ico sem entradas ou com diretório cortado")
	}
	best, bestSide := -1, 0
	for i := 0; i < n; i++ {
		side := int(b[6+16*i])
		if side == 0 {
			side = 256
		}
		better := best < 0 ||
			(side >= size && (bestSide < size || side < bestSide)) ||
			(side < size && bestSide < size && side > bestSide)
		if better {
			best, bestSide = i, side
		}
	}
	e := b[6+16*best:]
	length := int64(binary.LittleEndian.Uint32(e[8:]))
	off := int64(binary.LittleEndian.Uint32(e[12:]))
	if off+length > int64(len(b)) {
		return nil, errors.New("entrada do .ico aponta para fora do arquivo")
	}
	data := b[off : off+length]
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return nil, errors.New("entrada do .ico não é PNG")
	}
	return png.Decode(bytes.NewReader(data))
}
