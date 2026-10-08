//go:build !windows

package dpapi

import "github.com/guibsu/vpn-tray-monitor/internal/core/platform"

type stub struct{}

// New fora do Windows devolve um Protector que sempre falha.
func New() Protector { return stub{} }

func (stub) Protect([]byte, []byte) ([]byte, error)   { return nil, platform.ErrNotSupported }
func (stub) Unprotect([]byte, []byte) ([]byte, error) { return nil, platform.ErrNotSupported }
