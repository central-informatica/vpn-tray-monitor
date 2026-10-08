package ipc

import (
	"fmt"
	"log/slog"
	"time"
)

// EventReporter é o Event Log visto pelo servidor (logging.EventSink serve).
type EventReporter interface {
	Info(msg string)
	Warning(msg string)
}

// Limites do registro de falhas de Accept: uma linha de resumo por
// acceptLogEvery e aviso no Event Log após acceptWarnAfter de falhas contínuas.
const (
	acceptLogEvery  = time.Minute
	acceptWarnAfter = time.Minute
)

// acceptTrouble registra falhas seguidas de Accept sem inundar o log: a 1ª
// falha vai ao log, depois no máximo uma linha por minuto com a contagem;
// ~1 min de falhas contínuas avisa o Event Log (uma vez); voltar a aceitar
// registra Info (e Info no Event Log, se houve o aviso). Não é seguro para
// uso concorrente: só o laço de Accept o usa.
type acceptTrouble struct {
	log    *slog.Logger
	events EventReporter // nil = sem Event Log

	failing    bool
	since      time.Time // primeira falha da sequência
	lastLog    time.Time
	total      int // falhas da sequência
	sinceLast  int // falhas desde a última linha de log
	warnedSink bool
}

func (a *acceptTrouble) failed(now time.Time, err error) {
	if !a.failing {
		a.failing, a.since, a.lastLog, a.total, a.sinceLast, a.warnedSink = true, now, now, 1, 0, false
		a.log.Warn("falha ao aceitar conexão no pipe; nova tentativa", "erro", err)
		return
	}
	a.total++
	a.sinceLast++
	if now.Sub(a.lastLog) >= acceptLogEvery {
		a.log.Warn("falhas ao aceitar conexão no pipe continuam", "falhas", a.sinceLast,
			"desde", a.since.Format(time.RFC3339), "erro", err)
		a.lastLog, a.sinceLast = now, 0
	}
	if !a.warnedSink && now.Sub(a.since) >= acceptWarnAfter {
		a.warnedSink = true
		if a.events != nil {
			a.events.Warning(fmt.Sprintf("o pipe do VPN Monitor não aceita conexões há %s (bandeja e CLI sem acesso); último erro: %v",
				now.Sub(a.since).Round(time.Second), err))
		}
	}
}

func (a *acceptTrouble) recovered(now time.Time) {
	if !a.failing {
		return
	}
	d := now.Sub(a.since).Round(time.Millisecond)
	a.log.Info("pipe voltou a aceitar conexões", "falhas", a.total, "duracao", d)
	if a.warnedSink && a.events != nil {
		a.events.Info(fmt.Sprintf("o pipe do VPN Monitor voltou a aceitar conexões após %s", d.Round(time.Second)))
	}
	a.failing = false
}
