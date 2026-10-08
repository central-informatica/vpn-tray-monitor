// Package acl aplica e confere a segurança da pasta de dados (§5.1): dono
// Administradores; SYSTEM e Administradores com controle total; herança
// desligada; mais ninguém.
package acl

import (
	"errors"
	"sort"
	"strings"
)

// ErrQuarantined é informativo: a raiz existente não era confiável (ponto de
// reparse, não era pasta ou dono estranho) e foi renomeada para o lado; a
// raiz foi recriada e o EnsureDir teve sucesso (changed=true). O chamador deve
// logar e seguir (errors.Is), sem impedir a subida do serviço.
var ErrQuarantined = errors.New("pasta de dados não confiável posta de lado e recriada")

// Logf registra ocorrências do endurecimento (privilégios, links removidos…).
type Logf func(format string, args ...any)

// DirSDDL é o descritor da pasta. O dono (O:BA) importa: quem pré-cria a
// pasta continua dono e, como dono, mantém WRITE_DAC mesmo fora da DACL.
// A DACL é a mesma que o MSI aplica via PermissionEx.
const DirSDDL = "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

var wantACEs = []string{"A;OICI;FA;;;BA", "A;OICI;FA;;;SY"}

// Securer garante a segurança de uma pasta.
type Securer interface {
	// EnsureDir cria a pasta (já com o descritor) se preciso e corrige dono e DACL
	// da raiz e de toda a árvore, sem seguir pontos de reparse (§5.1). Pode
	// devolver changed=true com um erro que satisfaz errors.Is(err, ErrQuarantined):
	// informativo, não fatal.
	// changed=true quando precisou corrigir.
	EnsureDir(path string) (changed bool, err error)
}

// section devolve o trecho de um SDDL que começa em tag ("O:", "D:") até a
// próxima seção.
func section(sddl, tag string) (string, bool) {
	i := strings.Index(sddl, tag)
	if i < 0 {
		return "", false
	}
	s := sddl[i+len(tag):]
	for _, next := range []string{"O:", "G:", "D:", "S:"} {
		if next == tag {
			continue
		}
		if j := strings.Index(s, next); j >= 0 {
			s = s[:j] // as ACEs que o Windows produz não contêm essas marcas
		}
	}
	return s, true
}

// Matches confere um SDDL com dono (O:) e DACL (D:): dono Administradores
// ou SYSTEM (como nos filhos),
// DACL protegida (P) e exatamente as ACEs de SYSTEM e Administradores, em
// qualquer ordem.
func Matches(sddl string) bool {
	owner, ok := section(sddl, "O:")
	if !ok || !OwnerTrusted(owner) {
		return false
	}
	d, ok := section(sddl, "D:")
	if !ok {
		return false
	}
	open := strings.IndexByte(d, '(')
	if open < 0 || !strings.Contains(d[:open], "P") {
		return false
	}
	var aces []string
	for _, part := range strings.Split(d[open:], ")") {
		if part = strings.TrimPrefix(part, "("); part != "" {
			aces = append(aces, part)
		}
	}
	sort.Strings(aces)
	if len(aces) != len(wantACEs) {
		return false
	}
	for k := range aces {
		if aces[k] != wantACEs[k] {
			return false
		}
	}
	return true
}
