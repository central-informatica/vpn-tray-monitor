package shared

import "time"

// CeilMs converte para milissegundos arredondando para cima: um RTT medido
// > 0 nunca vira 0 ms (que o protocolo e a CLI leem como "sem dado").
func CeilMs(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64((d + time.Millisecond - 1) / time.Millisecond)
}
