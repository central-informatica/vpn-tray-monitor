package acl

import "testing"

func TestDecide(t *testing.T) {
	const inherited = "O:BAD:AI(A;ID;FA;;;SY)(A;OICIID;FA;;;BA)"
	good := func(p string) Entry { return Entry{Path: p, Exists: true, SDDL: inherited} }
	root := Entry{Path: ".", Exists: true, IsDir: true, SDDL: DirSDDL}
	with := func(e Entry, f func(*Entry)) Entry { f(&e); return e }

	cases := []struct {
		name     string
		root     Entry
		children []Entry
		want     Action
	}{
		{"não existe", Entry{}, nil, ActionCreate},
		{"tudo confere, vazia", root, nil, ActionOK},
		{"tudo confere, com filhos", root, []Entry{good("a"), good("logs/x")}, ActionOK},
		{"filho com dono SYSTEM confere", root, []Entry{with(good("a"), func(e *Entry) { e.SDDL = "O:SYD:AI(A;ID;FA;;;SY)" })}, ActionOK},
		{"raiz é junção", with(root, func(e *Entry) { e.Reparse = true }), nil, ActionQuarantine},
		{"raiz é arquivo", with(root, func(e *Entry) { e.IsDir = false }), nil, ActionQuarantine},
		{"raiz ilegível", with(root, func(e *Entry) { e.Unreadable = true }), nil, ActionQuarantine},
		{"filho junção", root, []Entry{good("a"), with(good("logs/j"), func(e *Entry) { e.Reparse = true })}, ActionQuarantine},
		{"filho ilegível", root, []Entry{with(good("a"), func(e *Entry) { e.Unreadable = true })}, ActionQuarantine},
		{"filho DACL protegida", root, []Entry{with(good("a"), func(e *Entry) { e.SDDL = "O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)" })}, ActionQuarantine},
		{"filho ACE explícita", root, []Entry{with(good("a"), func(e *Entry) { e.SDDL = "O:BAD:AI(A;ID;FA;;;SY)(A;;FA;;;WD)" })}, ActionQuarantine},
		{"filho dono usuário", root, []Entry{with(good("a"), func(e *Entry) { e.SDDL = "O:S-1-5-21-1-2-3-1001D:AI(A;ID;FA;;;SY)" })}, ActionQuarantine},
		{"filho DACL vazia", root, []Entry{with(good("a"), func(e *Entry) { e.SDDL = "O:BAD:AI" })}, ActionQuarantine},
		{"filho sem DACL", root, []Entry{with(good("a"), func(e *Entry) { e.SDDL = "O:BA" })}, ActionQuarantine},
		{"raiz vazia, DACL errada, dono BA", with(root, func(e *Entry) { e.SDDL = "O:BAD:AI(A;OICI;FA;;;SY)" }), nil, ActionReapplyRoot},
		{"raiz vazia, dono SYSTEM, DACL certa", with(root, func(e *Entry) { e.SDDL = "O:SYD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)" }), nil, ActionOK},
		{"raiz com conteúdo, dono SYSTEM, DACL certa", with(root, func(e *Entry) { e.SDDL = "O:SYD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)" }), []Entry{good("a")}, ActionOK},
		{"raiz vazia, dono SYSTEM, DACL errada", with(root, func(e *Entry) { e.SDDL = "O:SYD:AI(A;OICI;FA;;;SY)" }), nil, ActionReapplyRoot},
		{"raiz vazia, dono usuário", with(root, func(e *Entry) { e.SDDL = "O:S-1-5-21-1-2-3-1001D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)" }), nil, ActionQuarantine},
		{"raiz com conteúdo, DACL errada", with(root, func(e *Entry) { e.SDDL = "O:BAD:P(D;OICI;FA;;;SY)(A;OICI;FA;;;BA)" }), []Entry{good("a")}, ActionQuarantine},
	}
	for _, c := range cases {
		d := Decide(c.root, c.children)
		if d.Action != c.want {
			t.Errorf("%s: %v (%s), quer %v", c.name, d.Action, d.Reason, c.want)
		}
		if d.Action != ActionOK && d.Reason == "" {
			t.Errorf("%s: sem motivo", c.name)
		}
	}
}

// O motivo distingue dono e DACL da raiz (os testes Windows conferem o motivo).
func TestDecideRootReasons(t *testing.T) {
	user := Entry{Path: ".", Exists: true, IsDir: true, SDDL: "O:S-1-5-21-1-2-3-1001D:AI(A;ID;FA;;;SY)"}
	if d := Decide(user, nil); d.Action != ActionQuarantine || d.Reason != "dono da raiz não confiável" {
		t.Fatalf("dono usuário: %v %q", d.Action, d.Reason)
	}
	noOwner := Entry{Path: ".", Exists: true, IsDir: true, SDDL: "D:P(A;OICI;FA;;;SY)"}
	if d := Decide(noOwner, nil); d.Action != ActionQuarantine || d.Reason != "dono da raiz não confiável" {
		t.Fatalf("sem dono: %v %q", d.Action, d.Reason)
	}
	dacl := Entry{Path: ".", Exists: true, IsDir: true, SDDL: "O:BAD:AI(A;OICI;FA;;;SY)"}
	if d := Decide(dacl, nil); d.Action != ActionReapplyRoot || d.Reason != "DACL da raiz diverge" {
		t.Fatalf("DACL, vazia: %v %q", d.Action, d.Reason)
	}
	child := Entry{Path: "a", Exists: true, SDDL: "O:BAD:AI(A;ID;FA;;;SY)"}
	if d := Decide(dacl, []Entry{child}); d.Action != ActionQuarantine || d.Reason != "DACL da raiz diverge e há conteúdo" {
		t.Fatalf("DACL, com conteúdo: %v %q", d.Action, d.Reason)
	}
}
