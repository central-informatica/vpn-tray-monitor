package shared

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// tempFile é o que WriteFile precisa de um arquivo temporário.
type tempFile interface {
	io.Writer
	Sync() error
	Close() error
	Name() string
}

// fileOps isola as chamadas de sistema para os testes simularem falhas.
type fileOps struct {
	createTemp func(dir, pattern string) (tempFile, error)
	chmod      func(name string, mode os.FileMode) error
	rename     func(from, to string) error
	remove     func(name string) error
}

var osOps = fileOps{
	createTemp: func(dir, pattern string) (tempFile, error) { return os.CreateTemp(dir, pattern) },
	chmod:      os.Chmod,
	rename:     os.Rename,
	remove:     os.Remove,
}

// WriteFile grava data em path de forma atômica: escreve num temporário na
// mesma pasta, faz fsync, fecha e renomeia por cima. Em qualquer falha o
// arquivo original fica intacto e o temporário é apagado.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	return writeFile(osOps, path, data, perm)
}

func writeFile(ops fileOps, path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	f, err := ops.createTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("criando temporário para %s: %w", path, err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = ops.remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("gravando %s: %w", tmp, err)
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync de %s: %w", tmp, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("fechando %s: %w", tmp, err)
	}
	if err = ops.chmod(tmp, perm); err != nil {
		return fmt.Errorf("permissões de %s: %w", tmp, err)
	}
	if err = ops.rename(tmp, path); err != nil {
		return fmt.Errorf("renomeando %s → %s: %w", tmp, path, err)
	}
	return nil
}
