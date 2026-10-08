package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/client"
)

// Command é um pedido pronto para client.Call(ctx, c.Type, c.Payload, out).
type Command struct {
	Type    string
	Payload any
}

// maxNameRunes é o limite de config para o nome de uma VPN (§5.2).
const maxNameRunes = 64

// logTailBytes cabe com folga numa resposta de 64 KB.
const logTailBytes = 60000

// Pedidos sem escolha: verificar, reconectar, retomar, remover, listar
// entradas RAS, ler a config e o fim do log.
func CheckNow(vpn string) Command  { return Command{ipc.TypeCheckNow, ipc.VPNRef{VPN: vpn}} }
func Reconnect(vpn string) Command { return Command{ipc.TypeReconnect, ipc.VPNRef{VPN: vpn}} }
func Resume(vpn string) Command    { return Command{ipc.TypeResume, ipc.VPNRef{VPN: vpn}} }
func Remove(vpn string) Command    { return Command{ipc.TypeRemoveVPN, ipc.RemoveVPNRequest{Name: vpn}} }
func ListRasEntries() Command      { return Command{Type: ipc.TypeListRasEntries} }
func GetConfig() Command           { return Command{Type: ipc.TypeGetConfig} }
func LogTail() Command             { return Command{ipc.TypeLogTail, ipc.LogTailRequest{MaxBytes: logTailBytes}} }

// SetEnabled ativa ou desativa a VPN ("Desativar"/"Ativar").
func SetEnabled(vpn string, enabled bool) Command {
	return Command{ipc.TypeSetEnabled, ipc.SetEnabledRequest{VPN: vpn, Enabled: enabled}}
}

// ToggleCommand é o pedido do item "Desativar"/"Ativar" do submenu.
func (it VPNItem) ToggleCommand() Command { return SetEnabled(it.Name, !it.Enabled) }

// PauseChoice é uma opção do submenu "Pausar".
type PauseChoice int

const (
	Pause15Min PauseChoice = iota
	Pause1Hour
	PauseUntilResume
)

// PauseChoices são as opções do submenu, na ordem da §7.
var PauseChoices = []struct {
	Choice PauseChoice
	Label  string
}{{Pause15Min, "15 min"}, {Pause1Hour, "1 h"}, {PauseUntilResume, "Até retomar"}}

// Pause monta o pedido; "até retomar" vai com untilUnix null.
func Pause(vpn string, c PauseChoice, now time.Time) Command {
	req := ipc.PauseRequest{VPN: vpn}
	var d time.Duration
	switch c {
	case Pause15Min:
		d = 15 * time.Minute
	case Pause1Hour:
		d = time.Hour
	}
	if d > 0 {
		u := now.Add(d).Unix()
		req.UntilUnix = &u
	}
	return Command{ipc.TypePause, req}
}

// AddFromEntry cria a VPN de uma entrada RAS com verificação "link" (§7); o
// alvo de ping se define depois em Configurações. O nome é a entrada sem
// espaços nas pontas, até 64 runas, com " (2)", " (3)"… se já existir
// (sem diferenciar maiúsculas).
func AddFromEntry(entry string, existing []string) Command {
	taken := map[string]bool{}
	for _, n := range existing {
		taken[config.NameKey(n)] = true
	}
	base := []rune(strings.TrimSpace(entry))
	name := clip(base, maxNameRunes)
	for i := 2; taken[config.NameKey(name)]; i++ {
		suffix := fmt.Sprintf(" (%d)", i)
		name = strings.TrimSpace(clip(base, maxNameRunes-len([]rune(suffix)))) + suffix
	}
	return Command{ipc.TypeAddVPN, ipc.AddVPNRequest{Config: config.RawVPN{
		Name: name, RasEntry: entry, Check: &config.RawCheck{Kind: config.CheckLink}}}}
}

func clip(r []rune, n int) string {
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// ErrorText é a mensagem de um pedido que falhou, para a caixa de aviso.
func ErrorText(err error) string {
	var e *ipc.Error
	switch {
	case err == nil:
		return ""
	case errors.Is(err, client.ErrNotConnected):
		return "Sem conexão com o serviço VPN Monitor."
	case errors.Is(err, context.DeadlineExceeded):
		return "O serviço VPN Monitor não respondeu a tempo."
	case errors.As(err, &e):
		if e.Message == "" {
			return e.Code
		}
		return e.Message
	}
	return err.Error()
}

// RemoveConfirm é a pergunta de "Remover…". O comando sugerido segue as
// mesmas regras de aspas de CredentialCommand.
func RemoveConfirm(vpn string) string {
	return fmt.Sprintf("Remover a VPN %q do monitoramento?\n\nA conexão não é derrubada e a credencial guardada continua no cofre "+
		"(apague com: vpnmon-svc credential clear %s).", vpn, quoteArg(vpn))
}
