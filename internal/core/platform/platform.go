// Package platform reúne as interfaces de acesso ao SO. Cada subpacote tem a
// interface, a implementação Windows (_windows.go) e um stub (!windows); os
// fakes para teste ficam em platform/fake.
package platform

import "errors"

// ErrNotSupported é devolvido pelos stubs fora do Windows.
var ErrNotSupported = errors.New("operação disponível só no Windows")
