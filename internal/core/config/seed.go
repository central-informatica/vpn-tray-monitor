package config

import (
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

// Seed são as propriedades que o MSI grava em HKLM\SOFTWARE\VPNMonitor\Seed
// (§5.3). Tudo chega como texto.
type Seed struct {
	VPNEntry  string // VPN_ENTRY
	VPNName   string // VPN_NAME (padrão: igual à entrada)
	CheckKind string // CHECK_KIND (padrão: ping com host, link sem host)
	CheckHost string // CHECK_HOST
	CheckPort string // CHECK_PORT
	Interval  string // INTERVAL
}

// SeedReader lê o seed; found=false quando a chave não existe.
type SeedReader func() (seed Seed, found bool, err error)

// FromSeed gera a config inicial. Sem VPN_ENTRY, gera a config vazia.
func FromSeed(s Seed) (Config, error) {
	c := Empty()
	entry := strings.TrimSpace(s.VPNEntry)
	if entry == "" {
		return c, nil
	}
	raw := RawVPN{Name: strings.TrimSpace(s.VPNName), RasEntry: entry, Check: &RawCheck{}}
	if raw.Name == "" {
		raw.Name = entry
	}
	raw.Check.Host = strings.TrimSpace(s.CheckHost)
	switch kind := strings.ToLower(strings.TrimSpace(s.CheckKind)); {
	case kind != "":
		raw.Check.Kind = CheckKind(kind)
	case raw.Check.Host == "":
		raw.Check.Kind = CheckLink
	default:
		raw.Check.Kind = CheckPing
	}
	if p := strings.TrimSpace(s.CheckPort); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Config{}, fmt.Errorf("CHECK_PORT %q não é número", p)
		}
		raw.Check.Port = n
	}
	if iv := strings.TrimSpace(s.Interval); iv != "" {
		n, err := strconv.Atoi(iv)
		if err != nil {
			return Config{}, fmt.Errorf("INTERVAL %q não é número", iv)
		}
		raw.IntervalSeconds = &n
		if n > DefaultMaxBackoff {
			raw.MaxBackoffSeconds = &n // mantém maxBackoff ≥ interval
		}
	}
	c.VPNs = []VPN{raw.Normalize()}
	if err := Validate(c); err != nil {
		return Config{}, fmt.Errorf("seed do instalador inválido: %w", err)
	}
	return c, nil
}

// seedField decide o que fazer com a leitura de um valor do seed: ausente
// vira vazio; qualquer outro erro (ex.: tipo errado, REG_DWORD) é propagado
// com o nome do valor, para não gerar config padrão silenciosamente.
func seedField(name, val string, err error, missing bool) (string, error) {
	switch {
	case err == nil:
		return val, nil
	case missing:
		return "", nil
	default:
		return "", fmt.Errorf("lendo valor %s do seed: %w", name, err)
	}
}

// Bootstrap conta de onde veio a config carregada por LoadOrCreate.
type Bootstrap struct {
	Created     bool  // o arquivo não existia e foi gravado agora
	FromSeed    bool  // gerado a partir do seed
	SeedProblem error // seed presente mas inválido (gerou config vazia)
}

// LoadOrCreate carrega path; se não existir, gera pelo seed (ou vazia) e grava.
func LoadOrCreate(path string, readSeed SeedReader) (Config, Bootstrap, error) {
	c, err := Load(path)
	if err == nil {
		return c, Bootstrap{}, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return Config{}, Bootstrap{}, err
	}
	b := Bootstrap{Created: true}
	c = Empty()
	if readSeed != nil {
		s, found, rerr := readSeed()
		switch {
		case rerr != nil:
			b.SeedProblem = rerr
		case found:
			if sc, serr := FromSeed(s); serr != nil {
				b.SeedProblem = serr
			} else {
				c, b.FromSeed = sc, len(sc.VPNs) > 0
			}
		}
	}
	if _, err := Save(path, c); err != nil {
		return Config{}, b, err
	}
	return c, b, nil
}
