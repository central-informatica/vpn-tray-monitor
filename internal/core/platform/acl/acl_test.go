package acl

import "testing"

func TestMatches(t *testing.T) {
	cases := map[string]bool{
		DirSDDL: true,
		"O:BAG:SYD:PAI(A;OICI;FA;;;BA)(A;OICI;FA;;;SY)":                 true,
		"O:BAD:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)S:AI":                 true,
		"O:SYD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)":                       true,  // dono SYSTEM
		"D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)":                           false, // sem dono
		"O:S-1-5-21-1-2-3-1001D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)":      false, // usuário que pré-criou é dono
		"O:BAD:AI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)":                      false, // herança ligada
		"O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)": false, // usuários lendo
		"O:BAD:P(A;OICI;FA;;;SY)":                                       false,
		"O:BA":                                                          false,
		"O:BAD:P":                                                       false,
	}
	for in, want := range cases {
		if got := Matches(in); got != want {
			t.Errorf("Matches(%q) = %v, quer %v", in, got, want)
		}
	}
}
