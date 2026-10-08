package acl

import (
	"fmt"
	"strings"
)

// Action é o que fazer com a pasta de dados, decidido a partir do estado
// observado (lógica pura, sem E/S).
type Action int

const (
	// ActionOK: raiz e toda a árvore conferem.
	ActionOK Action = iota
	// ActionCreate: a raiz não existe; criar já com o descritor.
	ActionCreate
	// ActionReapplyRoot: raiz vazia, não é link, dono confiável, mas dono/DACL
	// divergem; reaplicar o descritor por handle.
	ActionReapplyRoot
	// ActionQuarantine: qualquer outro desvio; renomear a raiz inteira para o
	// lado e recriá-la vazia.
	ActionQuarantine
)

// Entry é o estado observado de uma entrada. Path é só para as mensagens.
type Entry struct {
	Path       string
	Exists     bool
	IsDir      bool
	Reparse    bool   // symlink, junção ou outro ponto de reparse
	Unreadable bool   // não foi possível observar (ex.: acesso negado)
	SDDL       string // dono + DACL
}

// Decision é a ação escolhida e, quando há desvio, o motivo.
type Decision struct {
	Action Action
	Reason string
}

// OwnerTrusted diz se o dono é Administradores ou SYSTEM (abreviado ou SID).
func OwnerTrusted(owner string) bool {
	switch owner {
	case "BA", "SY", "S-1-5-32-544", "S-1-5-18":
		return true
	}
	return false
}

// Decide escolhe a ação para a raiz e os filhos já observados (sem seguir
// links; entradas de reparse vêm marcadas e não são percorridas).
func Decide(root Entry, children []Entry) Decision {
	if !root.Exists {
		return Decision{ActionCreate, "a pasta não existe"}
	}
	switch {
	case root.Unreadable:
		return Decision{ActionQuarantine, "raiz ilegível"}
	case root.Reparse:
		return Decision{ActionQuarantine, "raiz é ponto de reparse"}
	case !root.IsDir:
		return Decision{ActionQuarantine, "raiz não é pasta"}
	}
	for _, c := range children {
		if why := childProblem(c); why != "" {
			return Decision{ActionQuarantine, fmt.Sprintf("%s: %s", c.Path, why)}
		}
	}
	if Matches(root.SDDL) {
		return Decision{ActionOK, ""}
	}
	owner, _ := section(root.SDDL, "O:")
	if len(children) == 0 && OwnerTrusted(owner) {
		return Decision{ActionReapplyRoot, "dono ou DACL da raiz divergem"}
	}
	return Decision{ActionQuarantine, "dono ou DACL da raiz divergem e há conteúdo ou dono não confiável"}
}

// childProblem devolve por que um filho não confere ("" se confere): não é
// reparse, dono BA/SY, DACL não protegida e só com ACEs herdadas (ao menos uma).
func childProblem(e Entry) string {
	if e.Unreadable {
		return "ilegível"
	}
	if e.Reparse {
		return "ponto de reparse"
	}
	owner, ok := section(e.SDDL, "O:")
	if !ok || !OwnerTrusted(owner) {
		return "dono não confiável"
	}
	d, ok := section(e.SDDL, "D:")
	if !ok {
		return "sem DACL"
	}
	open := strings.IndexByte(d, '(')
	if open < 0 {
		return "DACL vazia"
	}
	if strings.Contains(d[:open], "P") {
		return "DACL protegida"
	}
	for _, part := range strings.Split(d[open:], ")") {
		if part = strings.TrimPrefix(part, "("); part == "" {
			continue
		}
		fields := strings.Split(part, ";")
		if len(fields) < 2 || !hasFlag(fields[1], "ID") {
			return "ACE explícita"
		}
	}
	return ""
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
