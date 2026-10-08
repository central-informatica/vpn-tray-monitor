// Package dpapi protege dados com a DPAPI no escopo da máquina
// (CRYPTPROTECT_LOCAL_MACHINE). Qualquer processo local decifra o blob se
// puder lê-lo: a proteção real é a ACL da pasta (§5.4).
package dpapi

// Protector cifra e decifra com entropia adicional.
type Protector interface {
	Protect(plain, entropy []byte) ([]byte, error)
	Unprotect(blob, entropy []byte) ([]byte, error)
}
