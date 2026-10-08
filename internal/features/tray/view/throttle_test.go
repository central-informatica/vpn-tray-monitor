package view

import (
	"testing"
	"time"
)

func TestWarnLimiter(t *testing.T) {
	var l warnLimiter
	t0 := time.Unix(1000, 0)
	if !l.allow("icone", "falhou", t0) {
		t.Fatal("a primeira falha deve ser registrada")
	}
	for i := 1; i < 60; i++ {
		if l.allow("icone", "falhou", t0.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("repetição em %ds não deve ser registrada", i)
		}
	}
	if !l.allow("icone", "falhou", t0.Add(time.Minute)) {
		t.Fatal("após 1 minuto deve registrar de novo")
	}
	if !l.allow("icone", "outro erro", t0.Add(time.Minute+time.Second)) {
		t.Fatal("mensagem diferente deve registrar")
	}
	if !l.allow("tooltip", "outro erro", t0.Add(time.Minute+time.Second)) {
		t.Fatal("origem diferente é independente")
	}
}
