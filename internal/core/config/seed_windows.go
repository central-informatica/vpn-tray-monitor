//go:build windows

package config

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

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
	for _, f := range seed.values() {
		v, _, gerr := k.GetStringValue(f.name)
		val, ferr := seedField(f.name, v, gerr, errors.Is(gerr, registry.ErrNotExist))
		if ferr != nil {
			return Seed{}, false, ferr
		}
		*f.dst = val
	}
	return seed, true, nil
}
