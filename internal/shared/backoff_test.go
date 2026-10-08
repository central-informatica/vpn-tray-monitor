package shared

import (
	"testing"
	"time"
)

func TestBackoffGrowsAndCaps(t *testing.T) {
	b := Backoff{Base: 30 * time.Second, Max: 300 * time.Second, Jitter: 0.2}
	cases := []struct {
		n    int
		want time.Duration
	}{
		{0, 30 * time.Second}, {1, 30 * time.Second}, {2, 60 * time.Second},
		{3, 120 * time.Second}, {4, 240 * time.Second}, {5, 300 * time.Second},
		{50, 300 * time.Second},
	}
	for _, c := range cases {
		if got := b.Delay(c.n, 0.5); got != c.want { // r=0,5 → sem variação
			t.Errorf("Delay(%d) = %v, quer %v", c.n, got, c.want)
		}
	}
}

func TestBackoffJitterLimits(t *testing.T) {
	b := Backoff{Base: 30 * time.Second, Max: 300 * time.Second, Jitter: 0.2}
	if got := b.Delay(2, 0); got != 48*time.Second {
		t.Errorf("r=0 → %v, quer 48s (−20%%)", got)
	}
	if got := b.Delay(2, 0.999999); got < 71*time.Second || got > 72*time.Second {
		t.Errorf("r→1 → %v, quer ≈72s (+20%%)", got)
	}
	if got := b.Delay(10, 0.999999); got != 300*time.Second {
		t.Errorf("teto com jitter positivo = %v, quer 300s", got)
	}
	if got := b.Delay(1, 0); got != 24*time.Second {
		t.Errorf("piso = %v, quer 24s", got)
	}
}

func TestBackoffBaseAboveMax(t *testing.T) {
	b := Backoff{Base: 600 * time.Second, Max: 300 * time.Second}
	if got := b.Delay(3, 0.5); got != 600*time.Second {
		t.Errorf("base acima do teto deve valer a base, veio %v", got)
	}
}
