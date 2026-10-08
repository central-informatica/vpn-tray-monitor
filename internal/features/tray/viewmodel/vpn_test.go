package viewmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

var (
	brt = time.FixedZone("BRT", -3*3600)
	now = time.Date(2026, 10, 8, 14, 0, 0, 0, brt)
)

func ago(d time.Duration) int64   { return now.Add(-d).Unix() }
func ahead(d time.Duration) int64 { return now.Add(d).Unix() }

func TestVPNItemLabels(t *testing.T) {
	cases := []struct {
		name string
		v    ipc.VPNView
		want string
	}{
		{"conectada", ipc.VPNView{Name: "Matriz", State: ipc.StateConectada}, "Matriz  ● Conectada"},
		{"verificando", ipc.VPNView{Name: "M", State: ipc.StateDesconhecido}, "M  ● Verificando…"},
		{"instável", ipc.VPNView{Name: "M", State: ipc.StateDegradada, Failures: 2}, "M  ● Instável (2 falhas)"},
		{"instável sem falha", ipc.VPNView{Name: "M", State: ipc.StateDegradada}, "M  ● Instável"},
		{"reconectando", ipc.VPNView{Name: "Filial", State: ipc.StateReconectando, Attempt: 3, NextAttemptUnix: ahead(40 * time.Second)},
			"Filial  ● Reconectando (tent. 3, próxima em 40 s)"},
		{"reconectando discando", ipc.VPNView{Name: "M", State: ipc.StateReconectando}, "M  ● Reconectando"},
		{"próxima vencida", ipc.VPNView{Name: "M", State: ipc.StateReconectando, Attempt: 1, NextAttemptUnix: ago(time.Second)},
			"M  ● Reconectando (tent. 1)"},
		{"desconectada", ipc.VPNView{Name: "M", State: ipc.StateDesconectada, NextAttemptUnix: ahead(2 * time.Minute)},
			"M  ● Desconectada (próxima em 2 min)"},
		{"credencial", ipc.VPNView{Name: "M", State: ipc.StateCredencialInvalida}, "M  ● Credencial rejeitada"},
		{"erro config", ipc.VPNView{Name: "M", State: ipc.StateErroConfig}, "M  ● Erro de configuração"},
		{"pausada até", ipc.VPNView{Name: "Backup", State: ipc.StatePausada, PausedUntilUnix: time.Date(2026, 10, 8, 15, 30, 0, 0, brt).Unix()},
			"Backup  ○ Pausada até 15:30"},
		{"pausada indefinida", ipc.VPNView{Name: "M", State: ipc.StatePausada, PausedIndefinite: true}, "M  ○ Pausada até retomar"},
		{"sem rede", ipc.VPNView{Name: "M", State: ipc.StateSemRede}, "M  ● Sem rede"},
		{"desativada", ipc.VPNView{Name: "M", State: ipc.StateDesativada}, "M  ○ Desativada"},
		{"estado futuro", ipc.VPNView{Name: "M", State: "Hibernando"}, "M  ● Hibernando"},
		{"& vira &&", ipc.VPNView{Name: "P&D", State: ipc.StateConectada}, "P&&D  ● Conectada"},
		{"nome longo", ipc.VPNView{Name: strings.Repeat("ç", 40), State: ipc.StateConectada}, strings.Repeat("ç", 31) + "…  ● Conectada"},
	}
	for _, c := range cases {
		if got := vpnItem(c.v, now).Label; got != c.want {
			t.Errorf("%s: %q, esperava %q", c.name, got, c.want)
		}
	}
}

func TestVPNItemDetails(t *testing.T) {
	cases := []struct {
		name string
		v    ipc.VPNView
		want []string
	}{
		{"conectada", ipc.VPNView{Name: "Matriz", State: ipc.StateConectada, CheckKind: "ping", SinceUnix: ago(3 * time.Hour),
			LatencyMs: 12, Reconnects24h: 2, LastCheckUnix: ago(20 * time.Second)},
			[]string{"Conectada há 3 h · ping 12 ms · 2 reconexões em 24 h", "Última verificação há 20 s"}},
		{"conectada link", ipc.VPNView{Name: "M", State: ipc.StateConectada, CheckKind: "link", SinceUnix: ago(time.Minute), Reconnects24h: 1},
			[]string{"Conectada há 1 min · só enlace · 1 reconexão em 24 h"}},
		{"instável", ipc.VPNView{Name: "M", State: ipc.StateDegradada, CheckKind: "tcp", SinceUnix: ago(2 * time.Minute), Failures: 1},
			[]string{"Instável há 2 min · 1 falha de alcance"}},
		{"reconectando", ipc.VPNView{Name: "M", State: ipc.StateReconectando, SinceUnix: ago(time.Minute), Attempt: 3,
			NextAttemptUnix: ahead(40 * time.Second), LastError: &ipc.ErrorInfo{Class: ipc.ClassTransitorio, Code: 800, Message: "servidor não respondeu"}},
			[]string{"Reconectando há 1 min · tentativa 3 · próxima em 40 s", "Último erro: servidor não respondeu (erro 800)"}},
		{"desconectada", ipc.VPNView{Name: "M", State: ipc.StateDesconectada, SinceUnix: ago(time.Minute), NextAttemptUnix: ahead(time.Minute)},
			[]string{"Desconectada há 1 min · próxima tentativa em 1 min"}},
		{"sem rede", ipc.VPNView{Name: "M", State: ipc.StateSemRede, SinceUnix: ago(5 * time.Minute)},
			[]string{"Sem rede há 5 min · aguardando uma rede"}},
		{"verificando", ipc.VPNView{Name: "M", State: ipc.StateDesconhecido, SinceUnix: ago(time.Second),
			LastError: &ipc.ErrorInfo{Class: ipc.ClassTransitorio, Message: "supervisor reiniciando"}},
			[]string{"Verificando…", "Último erro: supervisor reiniciando"}},
		{"credencial ao vivo", ipc.VPNView{Name: "Matriz", State: ipc.StateCredencialInvalida, SinceUnix: ago(5 * time.Minute),
			LastError: &ipc.ErrorInfo{Class: ipc.ClassCredencial, Code: 691, Message: "usuário ou senha inválidos"}},
			[]string{"Credencial rejeitada há 5 min", "Último erro: usuário ou senha inválidos (erro 691)",
				"Corrija como administrador:", `vpnmon-svc credential set "Matriz" --user <usuário>`}},
		// Pendência do Marco A: restaurada do state.json, sem LastError.
		{"credencial restaurada", ipc.VPNView{Name: "Matriz", State: ipc.StateCredencialInvalida, SinceUnix: ago(time.Minute),
			BlockedUntilUnix: time.Date(2026, 10, 8, 14, 10, 0, 0, brt).Unix()},
			[]string{"Credencial rejeitada há 1 min", "Rejeitada antes do reinício do serviço; nova tentativa às 14:10",
				"Corrija como administrador:", `vpnmon-svc credential set "Matriz" --user <usuário>`}},
		{"erro config", ipc.VPNView{Name: "M", State: ipc.StateErroConfig, SinceUnix: ago(time.Hour),
			LastError: &ipc.ErrorInfo{Class: ipc.ClassConfiguracao, Code: 623, Message: `a entrada RAS "X" não existe`}},
			[]string{"Erro de configuração há 1 h", `Último erro: a entrada RAS "X" não existe (erro 623)`}},
		{"pausada", ipc.VPNView{Name: "M", State: ipc.StatePausada, PausedIndefinite: true, Reconnects24h: 3},
			[]string{"Pausada até retomar · 3 reconexões em 24 h"}},
		{"desativada", ipc.VPNView{Name: "M", State: ipc.StateDesativada, Reconnects24h: 3,
			LastError: &ipc.ErrorInfo{Message: "antigo"}}, []string{"Desativada"}},
		{"& nos detalhes", ipc.VPNView{Name: "P&D", State: ipc.StateCredencialInvalida},
			[]string{"Credencial rejeitada", "Corrija como administrador:", `vpnmon-svc credential set "P&&D" --user <usuário>`}},
	}
	for _, c := range cases {
		got := vpnItem(c.v, now).Details
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s:\n%q\nesperava\n%q", c.name, got, c.want)
		}
	}
}

func TestVPNItemDetailTruncated(t *testing.T) {
	v := ipc.VPNView{Name: "M", State: ipc.StateErroConfig, LastError: &ipc.ErrorInfo{Message: strings.Repeat("x", 300)}}
	for _, l := range vpnItem(v, now).Details {
		if n := len([]rune(l)); n > maxDetail {
			t.Fatalf("linha com %d runas", n)
		}
	}
	// O comando da credencial nunca é cortado, mesmo com o nome de 64 runas.
	long := strings.Repeat("ç", 64)
	d := vpnItem(ipc.VPNView{Name: long, State: ipc.StateCredencialInvalida}, now).Details
	if last := d[len(d)-1]; last != CredentialCommand(long) || strings.Contains(last, "…") {
		t.Fatalf("comando cortado: %q", last)
	}
}

func TestVPNItemActions(t *testing.T) {
	type acts struct{ check, reconnect, pause, resume, enabled bool }
	cases := map[string]struct {
		v      ipc.VPNView
		want   acts
		toggle string
	}{
		"conectada":  {ipc.VPNView{State: ipc.StateConectada, Enabled: true}, acts{true, true, true, false, true}, "Desativar"},
		"credencial": {ipc.VPNView{State: ipc.StateCredencialInvalida, Enabled: true}, acts{true, true, true, false, true}, "Desativar"},
		"pausada":    {ipc.VPNView{State: ipc.StatePausada, Enabled: true}, acts{false, false, false, true, true}, "Desativar"},
		"desativada": {ipc.VPNView{State: ipc.StateDesativada}, acts{false, false, false, false, false}, "Ativar"},
	}
	for name, c := range cases {
		it := vpnItem(c.v, now)
		got := acts{it.CanCheck, it.CanReconnect, it.CanPause, it.CanResume, it.Enabled}
		if got != c.want || it.ToggleLabel != c.toggle {
			t.Errorf("%s: %+v %q", name, got, it.ToggleLabel)
		}
	}
}

func TestSeverity(t *testing.T) {
	cases := map[string]Icon{
		ipc.StateConectada: IconGreen, ipc.StateDegradada: IconAmber, ipc.StateReconectando: IconAmber,
		ipc.StateDesconhecido: IconAmber, ipc.StateSemRede: IconAmber, ipc.StateDesconectada: IconRed,
		ipc.StateCredencialInvalida: IconRed, ipc.StateErroConfig: IconRed, "Futuro": IconAmber,
	}
	for st, want := range cases {
		if got := severity(st); got != want {
			t.Errorf("%s: %v, esperava %v", st, got, want)
		}
	}
	if !inactive(ipc.StatePausada) || !inactive(ipc.StateDesativada) || inactive(ipc.StateConectada) {
		t.Fatal("inactive")
	}
}
