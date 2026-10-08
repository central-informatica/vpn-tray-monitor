//go:build windows

package dpapi

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type machine struct{}

// New devolve o Protector real (escopo de máquina).
func New() Protector { return machine{} }

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func takeOut(out *windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	res := make([]byte, out.Size)
	copy(res, unsafe.Slice(out.Data, out.Size))
	return res
}

const flags = windows.CRYPTPROTECT_LOCAL_MACHINE | windows.CRYPTPROTECT_UI_FORBIDDEN

func (machine) Protect(plain, entropy []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptProtectData(blob(plain), nil, blob(entropy), 0, nil, flags, &out); err != nil {
		return nil, err
	}
	return takeOut(&out), nil
}

func (machine) Unprotect(b, entropy []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(blob(b), nil, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeOut(&out), nil
}
