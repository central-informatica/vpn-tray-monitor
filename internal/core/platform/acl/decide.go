package acl

import "strings"

// Action é o que fazer com uma entrada da pasta de dados, decidido a partir
// do estado observado (lógica pura, sem E/S).
type Action int

const (
	// ActionOK: nada a fazer.
	ActionOK Action = iota
	// ActionCreateRoot: a raiz não existe; criar já com o descritor.
	ActionCreateRoot
	// ActionQuarantineRoot: raiz não confiável (reparse, não é pasta ou dono
	// estranho); renomear para o lado e recriar.
	ActionQuarantineRoot
	// ActionApplyRoot: raiz confiável, mas dono/DACL divergem; reaplicar.
	ActionApplyRoot
	// ActionRemoveLink: filho é ponto de reparse; remover só o link.
	ActionRemoveLink
	// ActionResetChild: filho com dono, herança ou ACE explícita indevidos.
	ActionResetChild
)

// Entry é o estado observado de uma entrada.
type Entry struct {
	Exists  bool
	IsDir   bool
	Reparse bool   // symlink, junção ou outro ponto de reparse
	SDDL    string // dono + DACL (vazio se não lido)
}

// OwnerTrusted diz se o dono de uma raiz já existente é Administradores ou
// SYSTEM (formas abreviada e por SID).
func OwnerTrusted(owner string) bool {
	switch owner {
	case "BA", "SY", "S-1-5-32-544", "S-1-5-18":
		return true
	}
	return false
}

// DecideRoot escolhe a ação para a raiz da pasta de dados.
func DecideRoot(e Entry) Action {
	if !e.Exists {
		return ActionCreateRoot
	}
	if e.Reparse || !e.IsDir {
		return ActionQuarantineRoot
	}
	owner, _ := section(e.SDDL, "O:")
	if !OwnerTrusted(owner) {
		return ActionQuarantineRoot
	}
	if !Matches(e.SDDL) {
		return ActionApplyRoot
	}
	return ActionOK
}

// DecideChild escolhe a ação para uma entrada abaixo da raiz.
func DecideChild(e Entry) Action {
	if e.Reparse {
		return ActionRemoveLink
	}
	if !ChildMatches(e.SDDL) {
		return ActionResetChild
	}
	return ActionOK
}

// ChildMatches confere um filho: dono Administradores, DACL não protegida e
// só ACEs herdadas (flag ID), ao menos uma.
func ChildMatches(sddl string) bool {
	owner, ok := section(sddl, "O:")
	if !ok || owner != "BA" {
		return false
	}
	d, ok := section(sddl, "D:")
	if !ok {
		return false
	}
	open := strings.IndexByte(d, '(')
	if open < 0 || strings.Contains(d[:open], "P") {
		return false
	}
	n := 0
	for _, part := range strings.Split(d[open:], ")") {
		if part = strings.TrimPrefix(part, "("); part == "" {
			continue
		}
		fields := strings.Split(part, ";")
		if len(fields) < 2 || !hasFlag(fields[1], "ID") {
			return false
		}
		n++
	}
	return n > 0
}

// hasFlag procura um flag de 2 letras (OI, CI, ID…) numa sequência de flags.
func hasFlag(flags, want string) bool {
	for i := 0; i+2 <= len(flags); i += 2 {
		if flags[i:i+2] == want {
			return true
		}
	}
	return false
}
