package main

import (
	"bytes"
	"testing"
)

func TestRun(t *testing.T) {
	cases := []struct {
		args       []string
		code       int
		out, inErr string
	}{
		{[]string{"v2.1.0-rc.1"}, 0, "tag=v2.1.0-rc.1\nsemver=2.1.0-rc.1\nproduct=2.1.1\nfilever=2.1.1.0\nprerelease=true\n", ""},
		{[]string{"-field", "product", "v2.1.0"}, 0, "2.1.99\n", ""},
		{[]string{"-field", "semver", "v2.1.0"}, 0, "2.1.0\n", ""},
		{[]string{"v2.1.0-beta.1"}, 1, "", "fora do formato"},
		{[]string{"-dev", "-field", "product", "v2.1.0-3-gabc1234"}, 0, "0.0.0\n", ""},
		{[]string{"-dev", "-field", "semver", "abc1234-dirty"}, 0, "abc1234-dirty\n", ""},
		{[]string{"-dev", "-field", "semver", ""}, 0, "dev\n", ""},
		{[]string{"-dev", "-field", "product", "v1.2.3"}, 0, "1.2.399\n", ""},
		{[]string{"-field", "nada", "v1.2.3"}, 2, "", "campo desconhecido"},
		{[]string{}, 2, "", "uso:"},
		{[]string{"v1.0.0", "v2.0.0"}, 2, "", "uso:"},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		code := run(c.args, &out, &errb)
		if code != c.code || out.String() != c.out || !bytes.Contains(errb.Bytes(), []byte(c.inErr)) {
			t.Errorf("%q: código %d, saída %q, erro %q", c.args, code, out.String(), errb.String())
		}
	}
}
