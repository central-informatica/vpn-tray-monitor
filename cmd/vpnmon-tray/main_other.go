//go:build !windows

package main

import (
	"fmt"
	"os"
)

// run fora do Windows só explica: a bandeja usa a API de ícones do Windows.
func run() int {
	fmt.Fprintln(os.Stderr, "vpnmon-tray só roda no Windows")
	return 1
}
