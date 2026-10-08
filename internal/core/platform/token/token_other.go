//go:build !windows

package token

// SetDefaultOwnerAdmins fora do Windows não faz nada: o dono de um arquivo é
// sempre o uid efetivo e não há o que ajustar (desenvolvimento com
// VPNMON_DATA_DIR).
func SetDefaultOwnerAdmins() error { return nil }

// DefaultOwner fora do Windows não tem o que informar.
func DefaultOwner() (string, error) { return "", nil }
