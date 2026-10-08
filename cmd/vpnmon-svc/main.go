// Comando vpnmon-svc: o serviço VPNMonitor e a CLI de administração.
package main

import (
	"os"
)

// Preenchidos por -ldflags no build de release.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	os.Exit(runCLI(os.Args[1:], defaultEnv()))
}
