package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/acl"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/dpapi"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/svc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/credentials"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

const usage = `uso: vpnmon-svc <comando>

  run                                  modo console, para depurar
  status                               estado de cada VPN (via pipe)
  check <vpn>                          verificação única, sem discar; sai com 1
                                       se sem rede, enlace caído ou alvo falhando
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
	securer        acl.Securer // endurece a pasta de dados antes de gravar segredos
	stdinConsole   func() bool // stdin é um console (senha digitada exporia eco)
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
		securer:      acl.NewWithLog(func(f string, a ...any) { fmt.Fprintf(os.Stderr, "aviso: "+f+"\n", a...) }),
		stdinConsole: stdinIsConsole,
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

// errCancelled indica Ctrl+C durante a leitura da senha (código 130).
var errCancelled = errors.New("cancelado")

// usageError leva ao código de saída 2.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// silentExit leva ao código dado sem imprimir "erro:" (o comando já
// explicou o resultado na saída padrão).
type silentExit struct{ code int }

func (e silentExit) Error() string { return fmt.Sprintf("código de saída %d", e.code) }

func runCLI(args []string, e env) int {
	if len(args) == 0 {
		// Sem argumentos e iniciado pelo SCM: o processo vira o serviço.
		if ok, _ := e.isService(); ok {
			return serviceMain(e)
		}
		fmt.Fprint(e.stderr, usage)
		return 2
	}
	err := dispatch(args, e)
	var ue usageError
	var se silentExit
	switch {
	case err == nil:
		return 0
	case errors.As(err, &se):
		return se.code
	case errors.As(err, &ue):
		fmt.Fprintf(e.stderr, "%s\n\n%s", ue.msg, usage)
		return 2
	case errors.Is(err, errCancelled):
		fmt.Fprintf(e.stderr, "erro: %v\n", err)
		return 130
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
	// O nome do comando é validado antes da elevação: desconhecido é uso (2).
	run, ok := commands[cmd]
	if !ok {
		return usageError{fmt.Sprintf("comando desconhecido %q", cmd)}
	}
	if !e.elevated() {
		return errors.New("este comando exige um prompt de administrador")
	}
	return run(rest, e)
}

// commands é a única lista dos comandos que exigem elevação: serve tanto para
// reconhecer o nome antes da elevação quanto para despachar. version e help
// são tratados antes, em dispatch.
var commands = map[string]func(rest []string, e env) error{
	"run":     noArgs("run", cmdRun),
	"status":  noArgs("status", cmdStatus),
	"check":   cmdCheck,
	"vpn":     cmdVPN,
	"install": noArgs("install", cmdInstall),
	"uninstall": noArgs("uninstall", func(e env) error {
		if err := e.uninstall(); err != nil {
			return err
		}
		fmt.Fprintln(e.stdout, "serviço VPNMonitor removido")
		return nil
	}),
	"credential": cmdCredential,
	"config":     cmdConfig,
}

// noArgs adapta um comando sem argumentos; argumentos a mais são uso (2).
func noArgs(name string, f func(env) error) func([]string, env) error {
	return func(rest []string, e env) error {
		if len(rest) > 0 {
			return usageError{fmt.Sprintf("%s não aceita argumentos", name)}
		}
		return f(e)
	}
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

// ensureSecure aplica a ACL da pasta de dados. Quarentena é aviso, não falha.
func ensureSecure(e env, dir string) error {
	_, err := e.securer.EnsureDir(dir)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, acl.ErrQuarantined):
		fmt.Fprintf(e.stderr, "aviso: %v\n", err)
		return nil
	}
	return fmt.Errorf("protegendo a pasta de dados: %w", err)
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
	if len(args) == 0 || args[0] != "validate" || len(args) > 2 {
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
	// Antes de qualquer gravação/remoção no cofre, a pasta de dados precisa ter
	// a ACL restrita (senão herda a do ProgramData e o blob DPAPI de máquina
	// ficaria legível por qualquer usuário local). Fica antes de ler a config:
	// a quarentena pode renomear a pasta.
	if args[0] == "set" || args[0] == "clear" {
		if err := ensureSecure(e, l.Dir); err != nil {
			return err
		}
	}
	cfg, cfgErr := config.Load(l.ConfigFile)
	// known: só a config inexistente é tolerada (aviso); outro erro bloqueia.
	known := func(name string) (bool, error) {
		if cfgErr != nil {
			if errors.Is(cfgErr, fs.ErrNotExist) {
				fmt.Fprintln(e.stderr, "aviso: config.json ainda não existe; nome não validado")
				return true, nil
			}
			return false, cfgErr
		}
		for _, v := range cfg.VPNs {
			if config.NameKey(v.Name) == config.NameKey(name) {
				return true, nil
			}
		}
		return false, nil
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
		ok, err := known(pos[0])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("VPN %q não existe na config", pos[0])
		}
		var pw string
		if *stdin {
			// Escolha: recusar (em vez de desligar o eco) se stdin for console;
			// a leitura interativa sem --password-stdin já cuida disso.
			if e.stdinConsole() {
				return errors.New("--password-stdin exige entrada redirecionada (pipe); sem ela, omita a flag para digitar sem eco")
			}
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
