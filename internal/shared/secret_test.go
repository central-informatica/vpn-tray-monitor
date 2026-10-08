package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestSecretNeverLeaks(t *testing.T) {
	s := NewSecret("hunter2")
	outs := []string{
		s.String(),
		fmt.Sprint(s), fmt.Sprintf("%v %+v %#v %s %q %x", s, s, s, s, s, s),
	}
	j, _ := json.Marshal(struct{ P Secret }{s})
	outs = append(outs, string(j))
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "senha", s)
	outs = append(outs, buf.String())
	for _, o := range outs {
		if strings.Contains(o, "hunter2") {
			t.Fatalf("segredo vazou em %q", o)
		}
	}
	if s.Reveal() != "hunter2" {
		t.Fatal("Reveal deve devolver o valor")
	}
}

func TestSecretWipe(t *testing.T) {
	s := NewSecret("abc")
	s.Wipe()
	if s.Reveal() != "\x00\x00\x00" {
		t.Fatalf("Wipe não zerou: %q", s.Reveal())
	}
	if !NewSecret("").IsEmpty() || s.IsEmpty() {
		t.Fatal("IsEmpty incorreto")
	}
}
