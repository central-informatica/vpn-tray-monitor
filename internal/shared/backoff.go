package shared

import "time"

// Backoff calcula a espera antes da tentativa n (n ≥ 1): Base·2^(n-1),
// limitada a Max, com variação aleatória de ±Jitter (fração, ex.: 0,2).
// O resultado nunca passa de Max nem fica abaixo de Base·(1-Jitter).
type Backoff struct {
	Base   time.Duration
	Max    time.Duration
	Jitter float64
}

// Delay devolve a espera da tentativa n. r é um número em [0,1) — injetado
// para manter a função pura e testável.
func (b Backoff) Delay(n int, r float64) time.Duration {
	if n < 1 {
		n = 1
	}
	if b.Max < b.Base {
		b.Max = b.Base
	}
	d := b.Base
	for i := 1; i < n && d < b.Max; i++ {
		d *= 2
	}
	if d > b.Max {
		d = b.Max
	}
	factor := 1 + b.Jitter*(2*r-1)
	j := time.Duration(float64(d) * factor)
	if j > b.Max {
		j = b.Max
	}
	if lo := time.Duration(float64(b.Base) * (1 - b.Jitter)); j < lo {
		j = lo
	}
	return j
}
