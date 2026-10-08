package viewmodel

import (
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

func TestDuration(t *testing.T) {
	cases := map[time.Duration]string{
		-time.Second:                    "0 s",
		0:                               "0 s",
		1500 * time.Millisecond:         "1 s",
		59 * time.Second:                "59 s",
		time.Minute:                     "1 min",
		59*time.Minute + 59*time.Second: "59 min",
		time.Hour:                       "1 h",
		3*time.Hour + 5*time.Minute:     "3 h 5 min",
		47*time.Hour + 59*time.Minute:   "47 h 59 min",
		48 * time.Hour:                  "2 d",
		100 * time.Hour:                 "4 d",
	}
	for d, want := range cases {
		if got := Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, esperava %q", d, got, want)
		}
	}
}

func TestClockTime(t *testing.T) {
	loc := time.FixedZone("BRT", -3*3600)
	now := time.Date(2026, 10, 8, 14, 0, 0, 0, loc)
	if got := ClockTime(time.Date(2026, 10, 8, 15, 30, 0, 0, loc).Unix(), now); got != "15:30" {
		t.Fatalf("mesmo dia: %q", got)
	}
	// Horário no fuso de quem vê (now), não em UTC.
	if got := ClockTime(time.Date(2026, 10, 8, 18, 30, 0, 0, time.UTC).Unix(), now); got != "15:30" {
		t.Fatalf("fuso: %q", got)
	}
	if got := ClockTime(time.Date(2026, 10, 9, 8, 5, 0, 0, loc).Unix(), now); got != "09/10 08:05" {
		t.Fatalf("outro dia: %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"Matriz", 10, "Matriz"},
		{"Matriz", 6, "Matriz"},
		{"Conexão São Paulo", 8, "Conexão…"},
		{"ãããã", 3, "ãã…"},
		{"abc", 1, "…"},
		{"abc", 0, ""},
		{"", 5, ""},
	}
	for _, c := range cases {
		got := Truncate(c.in, c.max)
		if got != c.want {
			t.Errorf("Truncate(%q, %d) = %q, esperava %q", c.in, c.max, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("Truncate(%q, %d) cortou uma runa ao meio", c.in, c.max)
		}
	}
}

// O tooltip do Windows conta unidades UTF-16 (szTip tem 128 com o NUL):
// emoji fora do BMP ocupam 2 e nunca são partidos.
func TestTruncateUTF16(t *testing.T) {
	if got := truncateUTF16("VPN 😀😀", 7); got != "VPN 😀…" {
		t.Fatalf("emoji: %q", got)
	}
	if got := truncateUTF16("VPN 😀", 6); got != "VPN 😀" {
		t.Fatalf("cabe exato: %q", got)
	}
	long := ""
	for range 200 {
		long += "ç"
	}
	got := truncateUTF16(long, 127)
	if n := len(utf16.Encode([]rune(got))); n != 127 {
		t.Fatalf("tamanho %d", n)
	}
	if truncateUTF16("abc", 0) != "" {
		t.Fatal("max 0")
	}
}
