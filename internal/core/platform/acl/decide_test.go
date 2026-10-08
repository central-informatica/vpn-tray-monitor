package acl

import "testing"

func TestDecideRoot(t *testing.T) {
	cases := []struct {
		name string
		e    Entry
		want Action
	}{
		{"não existe", Entry{}, ActionCreateRoot},
		{"ok", Entry{Exists: true, IsDir: true, SDDL: DirSDDL}, ActionOK},
		{"junção", Entry{Exists: true, IsDir: true, Reparse: true, SDDL: DirSDDL}, ActionQuarantineRoot},
		{"é arquivo", Entry{Exists: true, SDDL: DirSDDL}, ActionQuarantineRoot},
		{"dono usuário", Entry{Exists: true, IsDir: true, SDDL: "O:S-1-5-21-1-2-3-1001D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"}, ActionQuarantineRoot},
		{"sem dono lido", Entry{Exists: true, IsDir: true, SDDL: "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"}, ActionQuarantineRoot},
		{"dono SYSTEM, DACL errada", Entry{Exists: true, IsDir: true, SDDL: "O:SYD:AI(A;OICI;FA;;;SY)"}, ActionApplyRoot},
		{"dono BA, usuários lendo", Entry{Exists: true, IsDir: true, SDDL: "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FR;;;BU)"}, ActionApplyRoot},
	}
	for _, c := range cases {
		if got := DecideRoot(c.e); got != c.want {
			t.Errorf("%s: %v, quer %v", c.name, got, c.want)
		}
	}
}

func TestDecideChild(t *testing.T) {
	const inherited = "O:BAD:AI(A;ID;FA;;;SY)(A;OICIID;FA;;;BA)"
	cases := []struct {
		name string
		e    Entry
		want Action
	}{
		{"herdado, dono BA", Entry{Exists: true, SDDL: inherited}, ActionOK},
		{"junção", Entry{Exists: true, IsDir: true, Reparse: true, SDDL: inherited}, ActionRemoveLink},
		{"DACL protegida", Entry{Exists: true, SDDL: "O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)"}, ActionResetChild},
		{"ACE explícita para Todos", Entry{Exists: true, SDDL: "O:BAD:AI(A;ID;FA;;;SY)(A;;FA;;;WD)"}, ActionResetChild},
		{"dono usuário", Entry{Exists: true, SDDL: "O:S-1-5-21-1-2-3-1001D:AI(A;ID;FA;;;SY)"}, ActionResetChild},
		{"dono SYSTEM", Entry{Exists: true, SDDL: "O:SYD:AI(A;ID;FA;;;SY)"}, ActionResetChild},
		{"DACL vazia", Entry{Exists: true, SDDL: "O:BAD:AI"}, ActionResetChild},
		{"sem DACL", Entry{Exists: true, SDDL: "O:BA"}, ActionResetChild},
	}
	for _, c := range cases {
		if got := DecideChild(c.e); got != c.want {
			t.Errorf("%s: %v, quer %v", c.name, got, c.want)
		}
	}
}
