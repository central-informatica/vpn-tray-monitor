// Package viewmodel transforma o estado do serviço (snapshot, eventos e
// estado da conexão) no modelo de tela da bandeja: itens de menu, rótulos,
// habilitados, ícone, tooltip e balões. É puro (sem walk, sem relógio
// próprio) e testado no Linux; toda a apresentação, inclusive textos e
// truncamento por runas, mora aqui (§7).
package viewmodel

import (
	"fmt"
	"time"
	"unicode/utf16"
)

// Duration escreve uma duração curta: "45 s", "4 min", "3 h", "3 h 5 min", "2 d".
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		h, m := int(d.Hours()), int(d.Minutes())%60
		if m == 0 {
			return fmt.Sprintf("%d h", h)
		}
		return fmt.Sprintf("%d h %d min", h, m)
	}
	return fmt.Sprintf("%d d", int(d.Hours())/24)
}

// ClockTime escreve um instante Unix no fuso de now: "15:30" no mesmo dia,
// "09/10 08:05" em outro.
func ClockTime(unix int64, now time.Time) string {
	t := time.Unix(unix, 0).In(now.Location())
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("02/01 15:04")
}

// Truncate limita s a max runas, terminando em "…" quando corta (§14 item 12:
// nunca por bytes).
func Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// truncateUTF16 limita s a max unidades UTF-16 (o tooltip e o balão do
// Windows contam assim), sem partir um par substituto.
func truncateUTF16(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(utf16.Encode([]rune(s))) <= max {
		return s
	}
	n := 0
	out := []rune{}
	for _, r := range s {
		w := utf16.RuneLen(r)
		if w < 0 {
			w = 1
		}
		if n+w > max-1 { // reserva 1 para o "…"
			break
		}
		out = append(out, r)
		n += w
	}
	return string(out) + "…"
}
