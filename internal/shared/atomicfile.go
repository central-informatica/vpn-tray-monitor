package shared

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Tentativas do rename e as esperas entre elas. Só erros transitórios
// (ver isTransientRenameErr) são repetidos; os demais falham na hora.
const renameAttempts = 5

var renameDelays = []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 60 * time.Millisecond, 80 * time.Millisecond}

// tempFile é o que WriteFile precisa de um arquivo temporário.
type tempFile interface {
	io.Writer
	Sync() error
	Close() error
	Name() string
}

// fileOps isola as chamadas de sistema e a espera para os testes simularem
// falhas sem dormir de verdade.
type fileOps struct {
	createTemp func(dir, pattern string) (tempFile, error)
	chmod      func(name string, mode os.FileMode) error
	rename     func(from, to string) error
	remove     func(name string) error
	sleep      func(d time.Duration)
	transient  func(err error) bool
}

var osOps = fileOps{
	createTemp: func(dir, pattern string) (tempFile, error) { return os.CreateTemp(dir, pattern) },
	chmod:      os.Chmod,
	rename:     RenameReplace,
	remove:     os.Remove,
	sleep:      time.Sleep,
	transient:  isTransientRenameErr,
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
	if err = renameWithRetry(ops, tmp, path); err != nil {
		return fmt.Errorf("renomeando %s → %s: %w", tmp, path, err)
	}
	return nil
}

// renameWithRetry troca o temporário pelo destino. No Windows a substituição
// falha de forma transitória quando outro processo mantém o destino aberto
// (leitor, antivírus); por isso repete com espera crescente, até renameAttempts.
func renameWithRetry(ops fileOps, from, to string) error {
	var err error
	for i := 0; i < renameAttempts; i++ {
		if i > 0 {
			ops.sleep(renameDelays[i-1])
		}
		if err = ops.rename(from, to); err == nil || !ops.transient(err) {
			return err
		}
	}
	return err
}
