package credentials

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/fake"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

func TestFileIDIsSafe(t *testing.T) {
	cases := map[string]string{
		"Matriz":           "matriz-",
		"MATRIZ":           "matriz-",
		`..\..\Windows`:    "______windows-",
		"Filial São Paulo": "filial_s_o_paulo-",
	}
	for in, prefix := range cases {
		id := FileID(in)
		if !strings.HasPrefix(id, prefix) || strings.ContainsAny(id, `/\.:`) || len(id) != len(prefix)+8 {
			t.Errorf("FileID(%q) = %q", in, id)
		}
	}
	if FileID("Matriz") != FileID("matriz") {
		t.Error("nomes iguais sem maiúsculas devem dar o mesmo arquivo")
	}
	if FileID("a/b") == FileID("a_b") {
		t.Error("o hash distingue nomes com a mesma versão saneada")
	}
	if long := FileID(strings.Repeat("x", 64)); len(long) != 32+1+8 {
		t.Errorf("tamanho %d", len(long))
	}
}

func newVault(t *testing.T) Vault {
	return Vault{Dir: filepath.Join(t.TempDir(), "credentials"), DPAPI: fake.DPAPI{}}
}

func TestVaultSetGetClear(t *testing.T) {
	v := newVault(t)
	if _, _, ok, err := v.Get("Matriz"); ok || err != nil {
		t.Fatal("cofre vazio")
	}
	if err := v.Set("Matriz", "ana", shared.NewSecret("s3nha")); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(v.Dir, FileID("Matriz")+".bin"))
	if strings.Contains(string(raw), "s3nha") {
		t.Fatal("senha em claro no arquivo")
	}
	u, pw, ok, err := v.Get("MATRIZ")
	if err != nil || !ok || u != "ana" || pw.Reveal() != "s3nha" {
		t.Fatalf("%q %v %v", u, ok, err)
	}
	fp1 := v.Fingerprint("Matriz")
	_ = v.Set("Matriz", "ana", shared.NewSecret("nova"))
	if fp1 == "" || v.Fingerprint("Matriz") == fp1 {
		t.Fatal("impressão digital deve mudar ao regravar")
	}
	if removed, err := v.Clear("Matriz"); !removed || err != nil || v.Has("Matriz") {
		t.Fatal("Clear")
	}
	if removed, err := v.Clear("Matriz"); removed || err != nil {
		t.Fatal("Clear de ausente não é erro")
	}
	if err := v.Set("X", "", shared.NewSecret("x")); err == nil {
		t.Fatal("usuário vazio")
	}
	if err := v.Set("X", "ana", shared.NewSecret(strings.Repeat("é", 257))); err == nil || !strings.Contains(err.Error(), "até 256") {
		t.Fatalf("senha acima de PWLEN deve ser recusada na gravação: %v", err)
	}
	if err := v.Set("X", "ana", shared.NewSecret(strings.Repeat("é", 256))); err != nil {
		t.Fatalf("256 caracteres cabem: %v", err)
	}
}

func TestVaultCorruptBlob(t *testing.T) {
	v := newVault(t)
	_ = os.MkdirAll(v.Dir, 0o700)
	_ = os.WriteFile(filepath.Join(v.Dir, FileID("Matriz")+".bin"), []byte("lixo"), 0o600)
	if _, _, _, err := v.Get("Matriz"); err == nil {
		t.Fatal("blob inválido deve dar erro")
	}
}

func TestVaultUnreadableFileIsNotEmptyFingerprint(t *testing.T) {
	v := newVault(t)
	_ = os.MkdirAll(v.Dir, 0o700)
	// Um diretório no lugar do arquivo: existe, mas a leitura falha.
	if err := os.Mkdir(filepath.Join(v.Dir, FileID("Matriz")+".bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if fp := v.Fingerprint("Matriz"); fp == "" {
		t.Fatal("arquivo ilegível não pode virar \"\" (credencial não mudou)")
	}
	res := Resolver{Vault: v, RAS: fake.NewRAS("VPN Matriz")}
	if _, err := res.Resolve(context.Background(), "Matriz", "VPN Matriz"); err == nil {
		t.Fatal("Resolve deve propagar o erro de leitura")
	}
	if res.Fingerprint(context.Background(), "Matriz", "VPN Matriz") == "" {
		t.Fatal("Fingerprint do resolver não pode ser \"\" com arquivo ilegível")
	}
}

func TestDirFingerprintIgnoresTempFiles(t *testing.T) {
	v := newVault(t)
	_ = v.Set("Matriz", "ana", shared.NewSecret("x"))
	before := v.DirFingerprint()
	_ = os.WriteFile(filepath.Join(v.Dir, ".abc.bin.tmp-123"), []byte("t"), 0o600)
	if v.DirFingerprint() != before || before == "" {
		t.Fatal("temporários com ponto devem ser ignorados")
	}
}

func TestResolverOrder(t *testing.T) {
	r := fake.NewRAS("VPN Matriz")
	v := newVault(t)
	res := Resolver{Vault: v, RAS: r}
	ctx := context.Background()

	got, err := res.Resolve(ctx, "Matriz", "VPN Matriz")
	if err != nil || got.Source != SourceNone || got.Fingerprint != "" || got.Saved == nil {
		t.Fatalf("nenhuma: %+v %v", got, err)
	}
	r.SetSaved("VPN Matriz", "ana", "marcador")
	got, _ = res.Resolve(ctx, "Matriz", "VPN Matriz")
	if got.Source != SourceWindows || !strings.HasPrefix(got.Fingerprint, "windows:") || res.Fingerprint(ctx, "Matriz", "VPN Matriz") != got.Fingerprint {
		t.Fatalf("windows: %+v", got)
	}
	_ = v.Set("Matriz", "bia", shared.NewSecret("pw"))
	got, _ = res.Resolve(ctx, "Matriz", "VPN Matriz")
	if got.Source != SourceVault || got.User != "bia" || got.Password.Reveal() != "pw" || !strings.HasPrefix(got.Fingerprint, "cofre:") {
		t.Fatalf("cofre: %+v", got)
	}
	if res.Fingerprint(ctx, "Matriz", "VPN Matriz") != got.Fingerprint {
		t.Fatal("Fingerprint deve bater com Resolve")
	}
	if _, err := res.Resolve(ctx, "Outra", "Inexistente"); err == nil {
		t.Fatal("entrada inexistente deve dar erro 623")
	}
}
