//go:build !windows

package config

// ReadSeedRegistry fora do Windows não há registro: nunca há seed.
func ReadSeedRegistry() (Seed, bool, error) { return Seed{}, false, nil }
