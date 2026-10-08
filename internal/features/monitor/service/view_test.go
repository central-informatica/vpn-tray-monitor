package service

import (
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
)

// A bandeja precisa do fim da janela de uma credencial rejeitada para
// explicar o bloqueio restaurado do state.json (que não tem LastErr).
func TestToViewBlockedUntil(t *testing.T) {
	v := config.VPN{Name: "Matriz", RasEntry: "VPN Matriz", Enabled: true, Check: config.Check{Kind: config.CheckLink}}
	until := t0.Add(10 * time.Minute)
	restored := domain.Status{State: domain.CredencialInvalida, Since: t0, Blocked: domain.CredencialInvalida, BlockedUntil: until}
	if got := ToView(v, restored, t0); got.BlockedUntilUnix != until.Unix() || got.LastError != nil {
		t.Fatalf("restaurada: %+v", got)
	}
	// Prazo vencido não vai para a bandeja.
	if got := ToView(v, restored, until); got.BlockedUntilUnix != 0 {
		t.Fatalf("prazo vencido: %+v", got)
	}
	// Fora de CredencialInvalida (ex.: pausada guardando o bloqueio) não há prazo.
	paused := restored
	paused.State = domain.Pausada
	if got := ToView(v, paused, t0); got.BlockedUntilUnix != 0 {
		t.Fatalf("pausada: %+v", got)
	}
	rejected := domain.Status{State: domain.CredencialInvalida, Since: t0,
		LastErr: &domain.DialError{Class: ras.ClassCredencial, Code: 691, Message: "acesso negado"}}
	if got := ToView(v, rejected, t0); got.BlockedUntilUnix != 0 || got.LastError == nil || got.LastError.Class != "credencial" {
		t.Fatalf("rejeição ao vivo: %+v", got)
	}
}

// Os nomes de estado do protocolo (que a bandeja usa sem importar o
// monitor) são os mesmos do domínio.
func TestStateNamesMatchProtocol(t *testing.T) {
	pairs := map[domain.State]string{
		domain.Desconhecido: ipc.StateDesconhecido, domain.Conectada: ipc.StateConectada,
		domain.Degradada: ipc.StateDegradada, domain.Reconectando: ipc.StateReconectando,
		domain.Desconectada: ipc.StateDesconectada, domain.CredencialInvalida: ipc.StateCredencialInvalida,
		domain.ErroConfig: ipc.StateErroConfig, domain.Pausada: ipc.StatePausada,
		domain.SemRede: ipc.StateSemRede, domain.Desativada: ipc.StateDesativada,
	}
	for d, p := range pairs {
		if string(d) != p {
			t.Errorf("domínio %q × protocolo %q", d, p)
		}
	}
	if ras.ClassCredencial.String() != ipc.ClassCredencial || ras.ClassConfiguracao.String() != ipc.ClassConfiguracao ||
		ras.ClassTransitorio.String() != ipc.ClassTransitorio {
		t.Error("classes de erro do protocolo divergem de ras.Class")
	}
}
