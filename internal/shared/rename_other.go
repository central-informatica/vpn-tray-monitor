//go:build !windows

package shared

import "os"

// RenameReplace: fora do Windows o rename já substitui o destino aberto.
func RenameReplace(from, to string) error { return os.Rename(from, to) }
