//go:build !windows

package shared

import "os"

// OpenShared: fora do Windows um arquivo aberto não impede remoção nem troca.
func OpenShared(path string) (*os.File, error) { return os.Open(path) }

// ReadFileShared: fora do Windows equivale a os.ReadFile.
func ReadFileShared(path string) ([]byte, error) { return os.ReadFile(path) }

// OpenAppendShared abre (ou cria) path só para acrescentar.
func OpenAppendShared(path string, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, perm)
}
