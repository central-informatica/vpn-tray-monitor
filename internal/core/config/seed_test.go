package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFromSeed(t *testing.T) {
	c, err := FromSeed(Seed{VPNEntry: "VPN Matriz", CheckHost: "10.254.1.172", Interval: "20"})
	if err != nil {
		t.Fatal(err)
	}
	v := c.VPNs[0]
	if v.Name != "VPN Matriz" || v.Check.Kind != CheckPing || v.IntervalSeconds != 20 {
		t.Fatalf("seed gerou %+v", v)
	}

	c, _ = FromSeed(Seed{VPNEntry: "X", VPNName: "Filial"})
	if c.VPNs[0].Name != "Filial" || c.VPNs[0].Check.Kind != CheckLink {
		t.Fatalf("sem host deve virar link: %+v", c.VPNs[0])
	}

	c, _ = FromSeed(Seed{VPNEntry: "X", CheckKind: "TCP", CheckHost: "h.local", CheckPort: "443", Interval: "900"})
	if v := c.VPNs[0]; v.Check.Kind != CheckTCP || v.Check.Port != 443 || v.MaxBackoffSeconds != 900 {
		t.Fatalf("tcp: %+v", v)
	}

	if c, err := FromSeed(Seed{}); err != nil || len(c.VPNs) != 0 {
		t.Fatalf("seed vazio deve gerar config vazia: %v %v", c, err)
	}
	for _, bad := range []Seed{
		{VPNEntry: "X", CheckPort: "abc"},
		{VPNEntry: "X", Interval: "1"},
		{VPNEntry: "X", CheckKind: "tcp", CheckHost: "h"},
	} {
		if _, err := FromSeed(bad); err == nil {
			t.Errorf("seed %+v deveria falhar", bad)
		}
	}
}

func TestLoadOrCreate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	seed := func() (Seed, bool, error) { return Seed{VPNEntry: "VPN Matriz", CheckHost: "10.0.0.1"}, true, nil }

	c, b, err := LoadOrCreate(p, seed)
	if err != nil || !b.Created || !b.FromSeed || len(c.VPNs) != 1 {
		t.Fatalf("primeiro início: %+v %+v %v", c, b, err)
	}
	// Segundo início: lê o arquivo, ignora o seed.
	c, b, err = LoadOrCreate(p, func() (Seed, bool, error) { t.Fatal("não deve ler seed"); return Seed{}, false, nil })
	if err != nil || b.Created || len(c.VPNs) != 1 {
		t.Fatalf("segundo início: %+v %+v %v", c, b, err)
	}

	p2 := filepath.Join(dir, "outra.json")
	c, b, err = LoadOrCreate(p2, func() (Seed, bool, error) { return Seed{VPNEntry: "X", Interval: "x"}, true, nil })
	if err != nil || b.SeedProblem == nil || len(c.VPNs) != 0 {
		t.Fatalf("seed inválido deve gerar vazia e relatar: %+v %+v %v", c, b, err)
	}
	if _, err := os.Stat(p2); err != nil {
		t.Fatal("config vazia deveria ter sido gravada")
	}

	p3 := filepath.Join(dir, "invalida.json")
	_ = os.WriteFile(p3, []byte(`{"version":7}`), 0o600)
	if _, _, err := LoadOrCreate(p3, seed); err == nil {
		t.Fatal("arquivo existente inválido deve dar erro (não sobrescrever)")
	}
}

func TestLoadOrCreateSeedReaderError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	c, b, err := LoadOrCreate(p, func() (Seed, bool, error) { return Seed{}, false, errors.New("boom") })
	if err != nil || b.SeedProblem == nil || len(c.VPNs) != 0 || !b.Created || b.FromSeed {
		t.Fatalf("erro do leitor: %+v %+v %v", c, b, err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("config vazia deveria ter sido gravada")
	}
}

func TestSeedField(t *testing.T) {
	cause := errors.New("tipo errado")
	if v, err := seedField("INTERVAL", "20", nil, false); v != "20" || err != nil {
		t.Fatalf("ok: %q %v", v, err)
	}
	if v, err := seedField("INTERVAL", "", cause, true); v != "" || err != nil {
		t.Fatalf("ausente deve virar vazio: %q %v", v, err)
	}
	_, err := seedField("INTERVAL", "", cause, false)
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "INTERVAL") {
		t.Fatalf("erro deve propagar com o nome: %v", err)
	}
}

func TestStateCorruptIsQuarantined(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	s, err := LoadState(p, t0)
	if err != nil || len(s.Pauses) != 0 {
		t.Fatalf("ausente: %v %v", s, err)
	}
	s.Pauses["matriz"] = Pause{UntilUnix: 1700000000}
	s.Pauses["filial"] = Pause{Indefinite: true}
	if err := SaveState(p, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(p, t0)
	if err != nil || got.Pauses["matriz"].UntilUnix != 1700000000 || !got.Pauses["filial"].Indefinite {
		t.Fatalf("ida e volta: %+v %v", got, err)
	}

	_ = os.WriteFile(p, []byte("{lixo"), 0o600)
	got, err = LoadState(p, t0)
	var ce *CorruptStateError
	if !errors.As(err, &ce) || len(got.Pauses) != 0 {
		t.Fatalf("corrompido: %+v %v", got, err)
	}
	if filepath.Base(ce.MovedTo) != "state.json.corrompido-20261007-120000" {
		t.Fatalf("movido para %s", ce.MovedTo)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("state.json corrompido deveria ter saído do lugar")
	}
}
