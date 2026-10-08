//go:build windows

package instance

import (
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sys/windows"
)

// Acquire cria o mutex nomeado; se ele já existir (ou for de uma instância
// elevada, acesso negado), outra instância o detém e
// devolve ErrAlreadyRunning. release fecha o handle (idempotente); o Windows
// também o fecha quando o processo termina.
func Acquire(name string) (release func(), err error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateMutex(nil, false, p)
	if errors.Is(classify(err, windows.ERROR_ALREADY_EXISTS, windows.ERROR_ACCESS_DENIED), ErrAlreadyRunning) {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		return nil, fmt.Errorf("criando o mutex %s: %w", name, err)
	}
	var once sync.Once
	return func() { once.Do(func() { windows.CloseHandle(h) }) }, nil
}
