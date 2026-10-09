//go:build !windows

package shared

import "os"

// OpenShared: fora do Windows um arquivo aberto não impede remoção nem troca.
func OpenShared(path string) (*os.File, error) { return os.Open(path) }

// ReadFileShared: fora do Windows equivale a os.ReadFile.
func ReadFileShared(path string) ([]byte, error) { return os.ReadFile(path) }
