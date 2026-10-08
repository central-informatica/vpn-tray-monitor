package logging

import (
	"bytes"
	"io"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// Tail devolve no máximo maxBytes do fim do arquivo, começando numa linha
// inteira. Serve para a bandeja mostrar o log sem acesso à pasta.
func Tail(path string, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		return "", nil
	}
	// Compartilhado: não pode impedir a rotação de renomear o arquivo.
	f, err := shared.OpenShared(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	start := st.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	buf := make([]byte, st.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return "", err
	}
	if start > 0 {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	return string(buf), nil
}
