// Package logging monta o slog do serviço: texto, rotação por tamanho,
// redação de segredos, nível ajustável em execução e Event Log.
package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// RotatingWriter grava em path e, ao passar de MaxBytes, renomeia
// vpnmon.log → vpnmon.1.log → … → vpnmon.<MaxFiles>.log (o mais velho some).
type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	maxFiles int
	f        *os.File
	size     int64
	// retryAt: após uma rotação que falhou, só tenta de novo quando o arquivo
	// passar deste tamanho (não repete a cada linha).
	retryAt int64
	closed  bool
	rename  func(from, to string) error
	remove  func(name string) error
}

// OpenRotating abre (ou cria) o log em modo append.
func OpenRotating(path string, maxBytes int64, maxFiles int) (*RotatingWriter, error) {
	w := &RotatingWriter{path: path, maxBytes: maxBytes, maxFiles: maxFiles, rename: shared.RenameReplace, remove: os.Remove}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

// SetLimits muda os limites (recarga da config) sem reabrir.
func (w *RotatingWriter) SetLimits(maxBytes int64, maxFiles int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.maxBytes, w.maxFiles = maxBytes, maxFiles
}

func (w *RotatingWriter) open() error {
	if err := os.MkdirAll(filepath.Dir(w.path), 0o700); err != nil {
		return err
	}
	f, err := shared.OpenAppendShared(w.path, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f, w.size = f, st.Size()
	return nil
}

// rotatedName devolve vpnmon.<n>.log para n ≥ 1.
func rotatedName(path string, n int) string {
	ext := filepath.Ext(path)
	return fmt.Sprintf("%s.%d%s", strings.TrimSuffix(path, ext), n, ext)
}

// rotate é best-effort: falhas de remove/rename (no Windows, Tail ou antivírus
// segurando o arquivo) são ignoradas e o arquivo atual é reaberto em append,
// para o log nunca morrer. Se o rename do arquivo atual falha, a rotação só é tentada de novo
// depois que o arquivo crescer mais maxBytes/4 (retryAt).
func (w *RotatingWriter) rotate() error {
	if err := w.f.Close(); err != nil {
		w.f = nil
		if oerr := w.open(); oerr != nil {
			return oerr
		}
		return err
	}
	w.f = nil
	_ = w.remove(rotatedName(w.path, w.maxFiles))
	for i := w.maxFiles - 1; i >= 1; i-- {
		_ = w.rename(rotatedName(w.path, i), rotatedName(w.path, i+1))
	}
	if err := w.rename(w.path, rotatedName(w.path, 1)); err != nil {
		w.retryAt = w.size + max(w.maxBytes/4, 1)
	} else {
		w.retryAt = 0
	}
	return w.open()
}

// Write grava p, girando antes se p não couber no arquivo atual.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if w.f == nil { // reabertura anterior falhou
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes && w.size >= w.retryAt {
		if err := w.rotate(); err != nil {
			return 0, fmt.Errorf("girando log: %w", err)
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// Close fecha o arquivo.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
