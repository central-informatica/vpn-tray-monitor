package view

import "time"

// warnEvery é o intervalo mínimo entre dois avisos iguais da mesma origem.
const warnEvery = time.Minute

// warnLimiter evita que uma falha persistente (repetida a cada tique de 1 s)
// encha o log: registra a primeira falha de cada origem, depois só quando a
// mensagem de erro muda ou passou warnEvery desde o último registro. Só é
// usado na thread da interface (sem trava).
type warnLimiter struct {
	last map[string]warnState
}

type warnState struct {
	msg string
	at  time.Time
}

// allow diz se a falha (origem key, texto msg) deve ser registrada agora.
func (l *warnLimiter) allow(key, msg string, now time.Time) bool {
	if l.last == nil {
		l.last = map[string]warnState{}
	}
	s, ok := l.last[key]
	if ok && s.msg == msg && now.Sub(s.at) < warnEvery {
		return false
	}
	l.last[key] = warnState{msg, now}
	return true
}
