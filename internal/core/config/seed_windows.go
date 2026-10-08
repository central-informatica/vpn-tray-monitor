//go:build windows

package config

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

// SeedRegistryPath é a chave que o MSI grava.
const SeedRegistryPath = `SOFTWARE\VPNMonitor\Seed`

// ReadSeedRegistry lê o seed do HKLM (visão de 64 bits).
func ReadSeedRegistry() (Seed, bool, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, SeedRegistryPath, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, registry.ErrNotExist) {
		return Seed{}, false, nil
	}
	if err != nil {
		return Seed{}, false, err
	}
	defer k.Close()
	get := func(name string) string {
		v, _, err := k.GetStringValue(name)
		if err != nil {
			return ""
		}
		return v
	}
	return Seed{
		VPNEntry:  get("VPN_ENTRY"),
		VPNName:   get("VPN_NAME"),
		CheckKind: get("CHECK_KIND"),
		CheckHost: get("CHECK_HOST"),
		CheckPort: get("CHECK_PORT"),
		Interval:  get("INTERVAL"),
	}, true, nil
}
