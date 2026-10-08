package fake

import (
	"bytes"
	"errors"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/dpapi"
)

// DPAPI é um dpapi.Protector reversível e determinístico: "FAKEDPAPI" +
// entropia + dados com XOR. Recusa entropia errada, como a real.
type DPAPI struct{}

var fakeMagic = []byte("FAKEDPAPI:")

func xor(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = c ^ 0x5A
	}
	return out
}

func (DPAPI) Protect(plain, entropy []byte) ([]byte, error) {
	out := append([]byte{}, fakeMagic...)
	out = append(out, byte(len(entropy)))
	out = append(out, entropy...)
	return append(out, xor(plain)...), nil
}

func (DPAPI) Unprotect(blob, entropy []byte) ([]byte, error) {
	if !bytes.HasPrefix(blob, fakeMagic) || len(blob) < len(fakeMagic)+1 {
		return nil, errors.New("blob DPAPI inválido")
	}
	rest := blob[len(fakeMagic):]
	n := int(rest[0])
	if len(rest) < 1+n || !bytes.Equal(rest[1:1+n], entropy) {
		return nil, errors.New("entropia incorreta")
	}
	return xor(rest[1+n:]), nil
}

var _ dpapi.Protector = DPAPI{}
