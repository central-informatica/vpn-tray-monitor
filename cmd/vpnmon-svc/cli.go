package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/dpapi"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/svc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/credentials"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

const usage = `uso: vpnmon-svc <comando>

  run                                  modo console, para depurar
  status                               estado de cada VPN (via pipe)
  check <vpn>                          verificação única, sem discar
  vpn add --name N --entry E [--check ping|tcp|link] [--host H] [--port P]
          [--interval S] [--failures N] [--grace S] [--connect-timeout S]
          [--max-backoff S] [--disabled]
  vpn remove <vpn>
  vpn list
  install | uninstall                  registro manual do serviço (sem MSI)
  credential set <vpn> --user U [--password-stdin]
  credential clear <vpn>
  credential list
  config validate [arquivo]
  version

Todos os comandos, exceto version, exigem um prompt de administrador.
`

// env isola o que a CLI usa de fora, para os testes rodarem no Linux.
type env struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	dataDir        func() (string, error)
	elevated       func() bool
	dpapi          dpapi.Protector
	readPassword   func(io.Reader) (string, error)
	dial           func(ctx context.Context) (*ipc.Client, error)
	platform       func() (Platform, error)
	install        func(exe string) error
	uninstall      func() error
	isService      func() (bool, error)
	runService     func(svc.Hooks) error
	now            func() time.Time
}

func defaultEnv() env {
	return env{
		stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr,
		dataDir: dataDir, elevated: svc.IsElevated, dpapi: dpapi.New(), readPassword: readPassword,
		dial: func(ctx context.Context) (*ipc.Client, error) {
			c, err := ipc.Dial(ctx)
			if err != nil {
				return nil, err
			}
			return ipc.Handshake(c, version)
		},
		platform: realPlatform, install: svc.Install, uninstall: svc.Uninstall,
		isService: svc.IsService, runService: svc.Run, now: time.Now,
	}
}

// usageError leva ao código de saída 2.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func runCLI(args []string, e env) int {
	if len(args) == 0 {
		fmt.Fprint(e.stderr, usage)
		return 2
	}
	err := dispatch(args, e)
	var ue usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ue):
		fmt.Fprintf(e.stderr, "%s\n\n%s", ue.msg, usage)
		return 2
	default:
		fmt.Fprintf(e.stderr, "erro: %v\n", err)
		return 1
	}
}

func dispatch(args []string, e env) error {
	cmd, rest := args[0], args[1:]
	if cmd == "version" {
		fmt.Fprintf(e.stdout, "vpnmon-svc %s (commit %s, %s)\n", version, orDash(commit), orDash(date))
		return nil
	}
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Fprint(e.stdout, usage)
		return nil
	}
	if !e.elevated() {
		return errors.New("este comando exige um prompt de administrador")
	}
	switch cmd {
	case "install":
		return cmdInstall(e)
	case "uninstall":
		return e.uninstall()
	case "credential":
		return cmdCredential(rest, e)
	case "config":
		return cmdConfig(rest, e)
	}
	return usageError{fmt.Sprintf("comando desconhecido %q", cmd)}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func paths(e env) (layout, error) {
	d, err := e.dataDir()
	if err != nil {
		return layout{}, err
	}
	return newLayout(d), nil
}

// parseFlags aceita flags antes ou depois dos posicionais.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, usageError{err.Error()}
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// trimEOL remove só UM fim de linha (\n ou \r\n); espaços fazem parte da senha.
func trimEOL(s string) string {
	s = strings.TrimSuffix(s, "\n")
	return strings.TrimSuffix(s, "\r")
}

func cmdInstall(e env) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := e.install(exe); err != nil {
		return err
	}
	fmt.Fprintln(e.stdout, "serviço VPNMonitor instalado; inicie com: sc start VPNMonitor")
	return nil
}

func cmdConfig(args []string, e env) error {
	if len(args) == 0 || args[0] != "validate" {
		return usageError{"use: config validate [arquivo]"}
	}
	path := ""
	if len(args) > 1 {
		path = args[1]
	} else {
		l, err := paths(e)
		if err != nil {
			return err
		}
		path = l.ConfigFile
	}
	c, err := config.Load(path)
	if err != nil {
		var ve *config.ValidationError
		if errors.As(err, &ve) {
			for _, p := range ve.Problems {
				fmt.Fprintf(e.stdout, "  %s: %s\n", p.Field, p.Message)
			}
			return fmt.Errorf("%s inválido (%d problema(s))", path, len(ve.Problems))
		}
		return err
	}
	fmt.Fprintf(e.stdout, "%s válido (%d VPN(s))\n", path, len(c.VPNs))
	return nil
}

func cmdCredential(args []string, e env) error {
	if len(args) == 0 {
		return usageError{"use: credential set|clear|list"}
	}
	l, err := paths(e)
	if err != nil {
		return err
	}
	vault := credentials.Vault{Dir: l.Credentials, DPAPI: e.dpapi}
	cfg, cfgErr := config.Load(l.ConfigFile)
	known := func(name string) bool {
		if cfgErr != nil {
			return true // sem config legível não dá para conferir
		}
		for _, v := range cfg.VPNs {
			if config.NameKey(v.Name) == config.NameKey(name) {
				return true
			}
		}
		return false
	}
	switch args[0] {
	case "set":
		fs := flag.NewFlagSet("credential set", flag.ContinueOnError)
		user := fs.String("user", "", "usuário")
		stdin := fs.Bool("password-stdin", false, "lê a senha da entrada padrão")
		pos, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(pos) != 1 || *user == "" {
			return usageError{"use: credential set <vpn> --user U [--password-stdin]"}
		}
		if !known(pos[0]) {
			return fmt.Errorf("VPN %q não existe na config", pos[0])
		}
		var pw string
		if *stdin {
			line, err := bufio.NewReader(e.stdin).ReadString('\n')
			if err != nil && line == "" {
				return fmt.Errorf("lendo a senha: %w", err)
			}
			pw = trimEOL(line)
		} else {
			fmt.Fprint(e.stderr, "senha: ")
			pw, err = e.readPassword(e.stdin)
			fmt.Fprintln(e.stderr)
			if err != nil {
				return err
			}
		}
		if pw == "" {
			return errors.New("senha vazia")
		}
		secret := shared.NewSecret(pw)
		defer secret.Wipe()
		if err := vault.Set(pos[0], *user, secret); err != nil {
			return err
		}
		fmt.Fprintf(e.stdout, "credencial de %q gravada; o serviço vai usá-la na próxima discagem\n", pos[0])
		return nil
	case "clear":
		if len(args) != 2 {
			return usageError{"use: credential clear <vpn>"}
		}
		removed, err := vault.Clear(args[1])
		if err != nil {
			return err
		}
		if !removed {
			fmt.Fprintf(e.stdout, "%q não tinha credencial no cofre\n", args[1])
			return nil
		}
		fmt.Fprintf(e.stdout, "credencial de %q apagada\n", args[1])
		return nil
	case "list":
		if cfgErr != nil {
			return cfgErr
		}
		for _, v := range cfg.VPNs {
			has := "—"
			if vault.Has(v.Name) {
				has = "cofre"
			}
			fmt.Fprintf(e.stdout, "%s\t%s\n", v.Name, has)
		}
		return nil
	}
	return usageError{fmt.Sprintf("subcomando desconhecido %q", args[0])}
}
