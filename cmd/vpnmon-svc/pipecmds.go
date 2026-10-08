package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/adapters"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
)

// withClient conecta ao serviço (dial confere o PID do servidor e o
// protocolo) e roda f. Erros do serviço chegam ao usuário só com a mensagem.
func withClient(e env, f func(*ipc.Client) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := e.dial(ctx)
	if err != nil {
		return explain(err)
	}
	defer c.Close()
	return explain(f(c))
}

func cmdStatus(e env) error {
	return withClient(e, func(c *ipc.Client) error {
		var snap ipc.Snapshot
		if err := c.Call(ipc.TypeStatus, nil, &snap); err != nil {
			return err
		}
		fmt.Fprint(e.stdout, formatStatus(snap, e.now()))
		return nil
	})
}

func ago(now time.Time, unix int64) string {
	if unix == 0 {
		return "—"
	}
	d := now.Sub(time.Unix(unix, 0))
	if d < 0 {
		d = 0
	}
	return "há " + domain.FormatOutage(d)
}

// formatStatus monta a tabela do `status`.
func formatStatus(s ipc.Snapshot, now time.Time) string {
	if len(s.VPNs) == 0 {
		return "nenhuma VPN configurada (use: vpnmon-svc vpn add)\n"
	}
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "VPN\tESTADO\tDESDE\tÚLTIMA VERIFICAÇÃO\tDETALHE")
	for _, v := range s.VPNs {
		var detail []string
		if v.LatencyMs > 0 {
			detail = append(detail, fmt.Sprintf("%s %dms", v.CheckKind, v.LatencyMs))
		}
		if v.Failures > 0 {
			detail = append(detail, fmt.Sprintf("%d falha(s)", v.Failures))
		}
		if v.Attempt > 0 {
			detail = append(detail, fmt.Sprintf("tentativa %d", v.Attempt))
		}
		// Prazo já vencido (ou relógio do cliente à frente) é omitido:
		// "próxima em" nunca é negativo.
		if next := time.Unix(v.NextAttemptUnix, 0); v.NextAttemptUnix > 0 && next.After(now) {
			detail = append(detail, fmt.Sprintf("próxima em %s", domain.FormatOutage(next.Sub(now))))
		}
		switch {
		case v.PausedIndefinite:
			detail = append(detail, "até retomar")
		case v.PausedUntilUnix > 0:
			detail = append(detail, "até "+time.Unix(v.PausedUntilUnix, 0).Format("15:04"))
		}
		if v.Reconnects24h > 0 {
			detail = append(detail, fmt.Sprintf("%d reconexão(ões) em 24 h", v.Reconnects24h))
		}
		if v.LastError != nil && v.State != string(domain.Conectada) {
			detail = append(detail, fmt.Sprintf("erro %d: %s", v.LastError.Code, v.LastError.Message))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", v.Name, v.State, ago(now, v.SinceUnix), ago(now, v.LastCheckUnix), strings.Join(detail, " · "))
	}
	w.Flush()
	return b.String()
}

func cmdVPN(args []string, e env) error {
	if len(args) == 0 {
		return usageError{"use: vpn add|remove|list"}
	}
	switch args[0] {
	case "add":
		raw, err := parseVPNAdd(args[1:])
		if err != nil {
			return err
		}
		return withClient(e, func(c *ipc.Client) error {
			if err := c.Call(ipc.TypeAddVPN, ipc.AddVPNRequest{Config: raw}, nil); err != nil {
				return err
			}
			fmt.Fprintf(e.stdout, "VPN %q adicionada\n", raw.Name)
			return nil
		})
	case "remove":
		if len(args) != 2 {
			return usageError{"use: vpn remove <vpn>"}
		}
		return withClient(e, func(c *ipc.Client) error {
			if err := c.Call(ipc.TypeRemoveVPN, ipc.RemoveVPNRequest{Name: args[1]}, nil); err != nil {
				return err
			}
			fmt.Fprintf(e.stdout, "VPN %q removida (a credencial no cofre, se houver, continua: credential clear)\n", args[1])
			return nil
		})
	case "list":
		if len(args) != 1 {
			return usageError{"use: vpn list"}
		}
		return withClient(e, func(c *ipc.Client) error {
			var cfg config.Config
			if err := c.Call(ipc.TypeGetConfig, nil, &cfg); err != nil {
				return err
			}
			printVPNs(e.stdout, cfg)
			return nil
		})
	}
	return usageError{fmt.Sprintf("subcomando desconhecido %q", args[0])}
}

func printVPNs(out io.Writer, cfg config.Config) {
	if len(cfg.VPNs) == 0 {
		fmt.Fprintln(out, "nenhuma VPN configurada (use: vpnmon-svc vpn add)")
		return
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "VPN\tENTRADA RAS\tVERIFICAÇÃO\tINTERVALO\tATIVA")
	for _, v := range cfg.VPNs {
		check := string(v.Check.Kind)
		switch v.Check.Kind {
		case config.CheckPing:
			check += " " + v.Check.Host
		case config.CheckTCP:
			check += fmt.Sprintf(" %s:%d", v.Check.Host, v.Check.Port)
		}
		active := "sim"
		if !v.Enabled {
			active = "não"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%ds\t%s\n", v.Name, v.RasEntry, check, v.IntervalSeconds, active)
	}
	w.Flush()
}

// explain traduz o erro do serviço para o usuário: validação campo a campo
// (com o nome da flag) ou só a mensagem do servidor, sem o código técnico
// (ex.: invalid_config "config.json foi alterado no disco…", busy,
// incompatible, not_found). Outros erros passam intactos.
func explain(err error) error {
	var ie *ipc.Error
	if !errors.As(err, &ie) {
		return err
	}
	if len(ie.Fields) == 0 {
		if ie.Message == "" {
			return errors.New(ie.Code) // nunca "erro: " vazio
		}
		return errors.New(ie.Message)
	}
	parts := make([]string, len(ie.Fields))
	for i, f := range ie.Fields {
		parts[i] = fmt.Sprintf("%s: %s", flagFor(f.Field), f.Message)
	}
	return errors.New(strings.Join(parts, "; "))
}

var fieldFlags = map[string]string{
	"name": "name", "rasEntry": "entry", "check.kind": "check", "check.host": "host", "check.port": "port",
	"check.timeoutSeconds": "timeout", "intervalSeconds": "interval", "failuresBeforeReconnect": "failures",
	"graceAfterConnectSeconds": "grace", "connectTimeoutSeconds": "connect-timeout", "maxBackoffSeconds": "max-backoff",
}

// flagFor devolve "--flag" para o campo; campo sem flag sai como veio.
func flagFor(field string) string {
	if f, ok := fieldFlags[field]; ok {
		return "--" + f
	}
	return field
}

// parseVPNAdd monta a VPN crua: só os campos passados; o resto fica com os
// padrões da §5.2 (aplicados pelo serviço).
func parseVPNAdd(args []string) (config.RawVPN, error) {
	fs := flag.NewFlagSet("vpn add", flag.ContinueOnError)
	name := fs.String("name", "", "")
	entry := fs.String("entry", "", "")
	check := fs.String("check", "", "")
	host := fs.String("host", "", "")
	port := fs.Int("port", 0, "")
	timeout := fs.Int("timeout", 0, "")
	interval := fs.Int("interval", 0, "")
	failures := fs.Int("failures", 0, "")
	grace := fs.Int("grace", 0, "")
	connect := fs.Int("connect-timeout", 0, "")
	maxBackoff := fs.Int("max-backoff", 0, "")
	disabled := fs.Bool("disabled", false, "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return config.RawVPN{}, err
	}
	if len(pos) > 0 || *name == "" || *entry == "" {
		return config.RawVPN{}, usageError{"use: vpn add --name N --entry E [opções]"}
	}
	raw := config.RawVPN{Name: *name, RasEntry: *entry, Check: &config.RawCheck{Kind: config.CheckKind(*check), Host: *host, Port: *port}}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	ptr := func(flagName string, v int) *int {
		if !set[flagName] {
			return nil
		}
		return &v
	}
	raw.Check.TimeoutSeconds = ptr("timeout", *timeout)
	raw.IntervalSeconds = ptr("interval", *interval)
	raw.FailuresBeforeReconnect = ptr("failures", *failures)
	raw.GraceAfterConnectSeconds = ptr("grace", *grace)
	raw.ConnectTimeoutSeconds = ptr("connect-timeout", *connect)
	raw.MaxBackoffSeconds = ptr("max-backoff", *maxBackoff)
	if *disabled {
		f := false
		raw.Enabled = &f
	}
	if raw.Check.Kind == "" && *host == "" {
		raw.Check.Kind = config.CheckLink
	}
	return raw, nil
}

// errCheckFailed: o check rodou e o diagnóstico já saiu na saída padrão,
// mas a VPN não está OK (código 1, sem linha de erro extra).
var errCheckFailed = silentExit{1}

// cmdCheck faz uma verificação única no próprio processo, sem discar: lê a
// config do disco, consulta o enlace no RAS e, se de pé, roda o verificador.
// Sai com 0 só com tudo OK; sem rede, enlace caído ou alvo falhando = 1.
func cmdCheck(args []string, e env) error {
	if len(args) != 1 {
		return usageError{"use: check <vpn>"}
	}
	l, err := paths(e)
	if err != nil {
		return err
	}
	cfg, err := config.Load(l.ConfigFile)
	if err != nil {
		return err
	}
	var vpn *config.VPN
	for i := range cfg.VPNs {
		if config.NameKey(cfg.VPNs[i].Name) == config.NameKey(args[0]) {
			vpn = &cfg.VPNs[i]
		}
	}
	if vpn == nil {
		return fmt.Errorf("VPN %q não existe na config", args[0])
	}
	p, err := e.platform()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	link, err := adapters.LinkProber{RAS: p.RAS, Net: p.Net, Monitored: adapters.EntriesOf(cfg)}.Probe(ctx, vpn.RasEntry)
	if err != nil {
		return fmt.Errorf("consultando o RAS: %w", err)
	}
	switch {
	case !link.Network:
		fmt.Fprintf(e.stdout, "%s: sem rede com rota padrão (física ou PPPoE; a VPN não conta)\n", vpn.Name)
		return errCheckFailed
	case !link.Up:
		fmt.Fprintf(e.stdout, "%s: enlace caído (entrada %q não conectada)\n", vpn.Name, vpn.RasEntry)
		return errCheckFailed
	case vpn.Check.Kind == config.CheckLink:
		fmt.Fprintf(e.stdout, "%s: enlace de pé (verificação link)\n", vpn.Name)
	default:
		// O ping abandona o eco preso quando ctx termina; tipo desconhecido
		// volta como r.Err.
		r := adapters.NewChecker(vpn.Check, p.Pinger, nil).Check(ctx)
		switch {
		case r.OK:
			fmt.Fprintf(e.stdout, "%s: enlace de pé; %s %s respondeu em %dms\n", vpn.Name, vpn.Check.Kind, vpn.Check.Host, r.RTT.Milliseconds())
		case r.Err != nil:
			fmt.Fprintf(e.stdout, "%s: enlace de pé; %s %s falhou: %v\n", vpn.Name, vpn.Check.Kind, vpn.Check.Host, r.Err)
			return errCheckFailed
		default:
			fmt.Fprintf(e.stdout, "%s: enlace de pé; %s %s sem resposta\n", vpn.Name, vpn.Check.Kind, vpn.Check.Host)
			return errCheckFailed
		}
	}
	return nil
}
