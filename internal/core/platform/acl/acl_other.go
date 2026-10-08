//go:build !windows

package acl

import "os"

type posixSecurer struct{}

// New fora do Windows: pasta 0700 (desenvolvimento com VPNMON_DATA_DIR).
func New() Securer { return posixSecurer{} }

// NewWithLog fora do Windows ignora o log (não há o que endurecer além do modo).
func NewWithLog(Logf) Securer { return posixSecurer{} }

func (posixSecurer) EnsureDir(path string) (bool, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return false, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if st.Mode().Perm() == 0o700 {
		return false, nil
	}
	return true, os.Chmod(path, 0o700)
}
