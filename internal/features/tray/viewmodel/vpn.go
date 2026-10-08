package viewmodel

import (
	"fmt"
	"strings"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

// Limites de texto do menu, em runas.
const (
	maxName   = 32
	maxDetail = 96
)

// Icon é a cor do ícone da bandeja (§7).
type Icon int

const (
	IconGray Icon = iota
	IconGreen
	IconAmber
	IconRed
)

func (i Icon) String() string {
	switch i {
	case IconGray:
		return "cinza"
	case IconGreen:
		return "verde"
	case IconAmber:
		return "âmbar"
	case IconRed:
		return "vermelho"
	}
	return fmt.Sprintf("Icon(%d)", int(i))
}

// VPNItem é o submenu de uma VPN. Textos já vêm escapados para menu ("&&").
type VPNItem struct {
	Name         string // nome real (identidade nos pedidos), sem escape
	Label        string
	Details      []string
	CanCheck     bool
	CanReconnect bool
	CanPause     bool
	CanResume    bool
	Enabled      bool
	ToggleLabel  string // "Desativar" ou "Ativar"
}

// MenuEscape dobra o "&", que no menu do Windows marca o atalho.
func MenuEscape(s string) string { return strings.ReplaceAll(s, "&", "&&") }

// severity é a cor de uma VPN ativa: o ícone mostra a pior (§7).
func severity(state string) Icon {
	switch state {
	case ipc.StateConectada:
		return IconGreen
	case ipc.StateDesconectada, ipc.StateCredencialInvalida, ipc.StateErroConfig:
		return IconRed
	}
	return IconAmber
}

// inactive: pausada ou desativada não conta para o ícone.
func inactive(state string) bool {
	return state == ipc.StatePausada || state == ipc.StateDesativada
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// nextIn é o tempo até a próxima tentativa ("" se não há ou já venceu).
func nextIn(v ipc.VPNView, now time.Time) string {
	if v.NextAttemptUnix == 0 {
		return ""
	}
	d := time.Unix(v.NextAttemptUnix, 0).Sub(now)
	if d <= 0 {
		return ""
	}
	return Duration(d)
}

func pausedText(v ipc.VPNView, now time.Time) string {
	if v.PausedIndefinite || v.PausedUntilUnix == 0 {
		return "Pausada até retomar"
	}
	return "Pausada até " + ClockTime(v.PausedUntilUnix, now)
}

// shortState é o estado na linha do submenu e no tooltip.
func shortState(v ipc.VPNView, now time.Time) string {
	switch v.State {
	case ipc.StateDesconhecido:
		return "Verificando…"
	case ipc.StateConectada:
		return "Conectada"
	case ipc.StateDegradada:
		if v.Failures > 0 {
			return fmt.Sprintf("Instável (%s)", plural(v.Failures, "falha", "falhas"))
		}
		return "Instável"
	case ipc.StateReconectando:
		var parts []string
		if v.Attempt > 0 {
			parts = append(parts, fmt.Sprintf("tent. %d", v.Attempt))
		}
		if n := nextIn(v, now); n != "" {
			parts = append(parts, "próxima em "+n)
		}
		if len(parts) == 0 {
			return "Reconectando"
		}
		return "Reconectando (" + strings.Join(parts, ", ") + ")"
	case ipc.StateDesconectada:
		if n := nextIn(v, now); n != "" {
			return "Desconectada (próxima em " + n + ")"
		}
		return "Desconectada"
	case ipc.StateCredencialInvalida:
		return "Credencial rejeitada"
	case ipc.StateErroConfig:
		return "Erro de configuração"
	case ipc.StatePausada:
		return pausedText(v, now)
	case ipc.StateSemRede:
		return "Sem rede"
	case ipc.StateDesativada:
		return "Desativada"
	}
	return v.State
}

// stateWord é o estado sem detalhes, para "<estado> há X".
func stateWord(state string) string {
	switch state {
	case ipc.StateConectada:
		return "Conectada"
	case ipc.StateDegradada:
		return "Instável"
	case ipc.StateReconectando:
		return "Reconectando"
	case ipc.StateDesconectada:
		return "Desconectada"
	case ipc.StateCredencialInvalida:
		return "Credencial rejeitada"
	case ipc.StateErroConfig:
		return "Erro de configuração"
	case ipc.StateSemRede:
		return "Sem rede"
	}
	return state
}

func since(unix int64, now time.Time) string {
	if unix == 0 {
		return ""
	}
	return " há " + Duration(now.Sub(time.Unix(unix, 0)))
}

// mainLine é a primeira linha do detalhe.
func mainLine(v ipc.VPNView, now time.Time) string {
	var parts []string
	switch v.State {
	case ipc.StateDesconhecido:
		parts = []string{"Verificando…"}
	case ipc.StatePausada:
		parts = []string{pausedText(v, now)}
	case ipc.StateDesativada:
		return "Desativada"
	default:
		parts = []string{stateWord(v.State) + since(v.SinceUnix, now)}
	}
	switch v.State {
	case ipc.StateConectada:
		if v.CheckKind == "link" {
			parts = append(parts, "só enlace")
		} else if v.LatencyMs > 0 {
			parts = append(parts, fmt.Sprintf("%s %d ms", v.CheckKind, v.LatencyMs))
		} else if v.LastCheckUnix > 0 {
			// Latência 0 com verificação feita: abaixo de 1 ms, não "sem dado".
			parts = append(parts, v.CheckKind+" <1 ms")
		}
	case ipc.StateDegradada:
		if v.Failures > 0 {
			parts = append(parts, plural(v.Failures, "falha de alcance", "falhas de alcance"))
		}
	case ipc.StateReconectando:
		if v.Attempt > 0 {
			parts = append(parts, fmt.Sprintf("tentativa %d", v.Attempt))
		}
		if n := nextIn(v, now); n != "" {
			parts = append(parts, "próxima em "+n)
		}
	case ipc.StateDesconectada:
		if n := nextIn(v, now); n != "" {
			parts = append(parts, "próxima tentativa em "+n)
		}
	case ipc.StateSemRede:
		parts = append(parts, "aguardando uma rede")
	}
	if v.Reconnects24h > 0 {
		parts = append(parts, plural(v.Reconnects24h, "reconexão em 24 h", "reconexões em 24 h"))
	}
	return strings.Join(parts, " · ")
}

func errorLine(e *ipc.ErrorInfo) string {
	if e.Code > 0 {
		return fmt.Sprintf("Último erro: %s (erro %d)", e.Message, e.Code)
	}
	return "Último erro: " + e.Message
}

// details são as linhas informativas do topo do submenu.
func details(v ipc.VPNView, now time.Time) []string {
	lines := []string{mainLine(v, now)}
	if v.State == ipc.StateDesativada {
		return lines
	}
	if (v.State == ipc.StateConectada || v.State == ipc.StateDegradada) && v.LastCheckUnix > 0 {
		lines = append(lines, "Última verificação"+since(v.LastCheckUnix, now))
	}
	switch {
	case v.LastError != nil && v.State != ipc.StateConectada:
		lines = append(lines, errorLine(v.LastError))
	case v.State == ipc.StateCredencialInvalida && v.BlockedUntilUnix > 0:
		// Bloqueio restaurado do state.json: o serviço não guarda o erro,
		// só o fim da janela de 15 min (§4.7, §5.5).
		lines = append(lines, "Rejeitada antes do reinício do serviço; nova tentativa às "+ClockTime(v.BlockedUntilUnix, now))
	}
	for i, l := range lines {
		lines[i] = MenuEscape(Truncate(l, maxDetail))
	}
	if v.State == ipc.StateCredencialInvalida {
		// O comando vai inteiro numa linha própria (nunca truncado): é para
		// ser digitado. A CLI exige --user (§5.4).
		lines = append(lines, "Corrija como administrador:",
			MenuEscape(CredentialCommand(v.Name)))
	}
	return lines
}

// CredentialCommand é o comando que grava a credencial de uma VPN.
func CredentialCommand(vpn string) string {
	return fmt.Sprintf(`vpnmon-svc credential set "%s" --user <usuário>`, strings.ReplaceAll(vpn, `"`, `\"`))
}

// vpnItem monta o submenu de uma VPN.
func vpnItem(v ipc.VPNView, now time.Time) VPNItem {
	bullet := "●"
	if inactive(v.State) {
		bullet = "○"
	}
	usable := !inactive(v.State)
	it := VPNItem{
		Name:         v.Name,
		Label:        MenuEscape(Truncate(v.Name, maxName) + "  " + bullet + " " + shortState(v, now)),
		Details:      details(v, now),
		CanCheck:     usable,
		CanReconnect: usable,
		CanPause:     usable || v.State == ipc.StatePausada, // pausar de novo troca a duração (§4.7)
		CanResume:    v.State == ipc.StatePausada,
		Enabled:      v.Enabled,
		ToggleLabel:  "Desativar",
	}
	if !it.Enabled {
		it.ToggleLabel = "Ativar"
	}
	return it
}
