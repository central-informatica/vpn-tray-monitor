package main

import "testing"

func TestAppVersion(t *testing.T) {
	defer func(v, c, d string) { version, commit, date = v, c, d }(version, commit, date)
	cases := []struct{ v, c, d, want string }{
		{"dev", "", "", "dev"},
		{"v2.1.0", "abc1234", "", "v2.1.0 (abc1234)"},
		{"v2.1.0", "abc1234", "2026-10-08T12:00:00Z", "v2.1.0 (abc1234, 2026-10-08T12:00:00Z)"},
		// Fora de tag o git describe já é o commit: não repete.
		{"abc1234-dirty", "abc1234", "2026-10-08T12:00:00Z", "abc1234-dirty (2026-10-08T12:00:00Z)"},
		{"abc1234", "abc1234", "", "abc1234"},
		{"v2.1.0-3-gabc1234", "abc1234", "", "v2.1.0-3-gabc1234 (abc1234)"},
	}
	for _, c := range cases {
		version, commit, date = c.v, c.c, c.d
		if got := appVersion(); got != c.want {
			t.Errorf("%q, esperava %q", got, c.want)
		}
	}
}
