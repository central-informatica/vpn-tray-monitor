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
	var seed Seed
	fields := []struct {
		name string
		dst  *string
	}{
		{"VPN_ENTRY", &seed.VPNEntry},
		{"VPN_NAME", &seed.VPNName},
		{"CHECK_KIND", &seed.CheckKind},
		{"CHECK_HOST", &seed.CheckHost},
		{"CHECK_PORT", &seed.CheckPort},
		{"INTERVAL", &seed.Interval},
	}
	for _, f := range fields {
		v, _, gerr := k.GetStringValue(f.name)
		val, ferr := seedField(f.name, v, gerr, errors.Is(gerr, registry.ErrNotExist))
		if ferr != nil {
			return Seed{}, false, ferr
		}
		*f.dst = val
	}
	return seed, true, nil
}
