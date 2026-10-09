//go:build !windows

package shared

import "os"

// OpenAppendShared abre (ou cria) path só para acrescentar.
func OpenAppendShared(path string, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, perm)
}
