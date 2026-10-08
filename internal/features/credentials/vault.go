// Package credentials guarda usuário e senha por VPN no cofre
// (credentials\<id>.bin, DPAPI de máquina) e resolve a credencial de uma
// discagem: cofre → credencial salva no Windows → nenhuma (§5.4).
package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/dpapi"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// maxCredentialUnits é UNLEN/PWLEN do lmcons.h.
const maxCredentialUnits = 256

// entropy é a entropia fixa do app passada à DPAPI.
var entropy = []byte("VPNMonitor/v2/credenciais")

// FileID deriva o nome do arquivo do nome da VPN, sem permitir caminho:
// versão saneada (até 32 caracteres [a-z0-9_-]) + 12 hex do hash do nome
// sem diferenciar maiúsculas.
func FileID(name string) string {
	key := config.NameKey(name)
	var b strings.Builder
	for _, r := range key {
		if b.Len() >= 32 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	sum := sha256.Sum256([]byte(key))
	return b.String() + "-" + hex.EncodeToString(sum[:6])
}

// Vault é o cofre em disco.
type Vault struct {
	Dir   string
	DPAPI dpapi.Protector
}

type record struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// utf16Len conta unidades UTF-16 sem alocar (runa inválida vale U+FFFD, 1).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if l := utf16.RuneLen(r); l > 0 {
			n += l
		} else {
			n++
		}
	}
	return n
}

func hashBlob(blob []byte) string {
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:8])
}

func (v Vault) path(name string) string { return filepath.Join(v.Dir, FileID(name)+".bin") }

// Set grava (ou substitui) a credencial da VPN de forma atômica.
func (v Vault) Set(name, user string, password shared.Secret) error {
	if user == "" {
		return errors.New("usuário vazio")
	}
	// Limites do RASDIALPARAMSW (UNLEN e PWLEN, em unidades UTF-16): acima
	// disso a discagem nem poderia ser montada.
	pw := password.Reveal()
	if n := utf16Len(user); n > maxCredentialUnits {
		return fmt.Errorf("usuário com %d caracteres; o Windows aceita até %d", n, maxCredentialUnits)
	}
	if n := utf16Len(pw); n > maxCredentialUnits {
		return fmt.Errorf("senha com %d caracteres; o Windows aceita até %d", n, maxCredentialUnits)
	}
	plain, err := json.Marshal(record{User: user, Password: pw})
	if err != nil {
		return err
	}
	defer clear(plain)
	blob, err := v.DPAPI.Protect(plain, entropy)
	if err != nil {
		return fmt.Errorf("protegendo com DPAPI: %w", err)
	}
	if err := os.MkdirAll(v.Dir, 0o700); err != nil {
		return err
	}
	return shared.WriteFile(v.path(name), blob, 0o600)
}

// Get lê a credencial. ok=false se não houver arquivo.
func (v Vault) Get(name string) (user string, password shared.Secret, ok bool, err error) {
	user, password, _, ok, err = v.get(name)
	return
}

// get lê a credencial e devolve também a impressão digital do MESMO blob
// lido (sem segunda leitura do arquivo).
func (v Vault) get(name string) (user string, password shared.Secret, fp string, ok bool, err error) {
	blob, err := shared.ReadFileShared(v.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", shared.Secret{}, "", false, nil
	}
	if err != nil {
		return "", shared.Secret{}, "", false, err
	}
	fp = hashBlob(blob)
	plain, err := v.DPAPI.Unprotect(blob, entropy)
	if err != nil {
		return "", shared.Secret{}, "", false, fmt.Errorf("decifrando a credencial de %q: %w", name, err)
	}
	defer clear(plain)
	var r record
	if err := json.Unmarshal(plain, &r); err != nil {
		return "", shared.Secret{}, "", false, fmt.Errorf("credencial de %q corrompida", name)
	}
	return r.User, shared.NewSecret(r.Password), fp, true, nil
}

// Clear apaga a credencial; removed=false se não havia.
func (v Vault) Clear(name string) (removed bool, err error) {
	err = os.Remove(v.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Has diz se a VPN tem credencial no cofre.
func (v Vault) Has(name string) bool {
	_, err := os.Stat(v.path(name))
	return err == nil
}

// Fingerprint é o hash do arquivo da VPN. exists=false se não há arquivo;
// err != nil se o arquivo existe mas não pôde ser lido (fp vazio nesse caso).
// Muda quando a credencial é regravada, sem revelar nada; regravar a mesma
// senha gera um blob DPAPI novo e portanto conta como mudança (aceitável: é
// ação explícita do administrador).
func (v Vault) Fingerprint(name string) (fp string, exists bool, err error) {
	blob, err := shared.ReadFileShared(v.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", true, err
	}
	return hashBlob(blob), true, nil
}

// DirFingerprint resume a pasta (nomes, tamanhos, datas) para o observador.
// Ignora os temporários ".<nome>.tmp-*" de shared.WriteFile.
func (v Vault) DirFingerprint() string {
	ents, err := os.ReadDir(v.Dir)
	if err != nil {
		return ""
	}
	var parts []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if info, err := e.Info(); err == nil {
			parts = append(parts, fmt.Sprintf("%s:%d:%d", e.Name(), info.Size(), info.ModTime().UnixNano()))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}
