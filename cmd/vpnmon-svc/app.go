package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/acl"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/netwatch"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/svc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/credentials"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/adapters"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/service"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// configRetryDelays são as esperas entre as novas tentativas de ler
// config.json quando a leitura falha (no Windows, violação de
// compartilhamento enquanto um editor ou o antivírus segura o arquivo).
// Esgotadas, o problema vai para o log, o Event Log e o configStatus.
var configRetryDelays = []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second}

// Prazos da parada, dentro dos 10 s que o SCM espera: o orquestrador usa
// até 7 s; depois os observadores e o servidor do pipe.
const (
	watchersStopWait = 500 * time.Millisecond
	serverStopWait   = 2 * time.Second
)

// credBridge adapta features/credentials ao contrato do monitor (as
// features não se importam entre si; a montagem liga as duas).
type credBridge struct{ r credentials.Resolver }

func (b credBridge) Resolve(ctx context.Context, name, entry string) (adapters.Credentials, error) {
	res, err := b.r.Resolve(ctx, name, entry)
	if err != nil {
		return adapters.Credentials{}, err
	}
	return adapters.Credentials{User: res.User, Password: res.Password, Saved: res.Saved,
		Fingerprint: res.Fingerprint, Source: res.Source}, nil
}

func (b credBridge) Fingerprint(ctx context.Context, name, entry string) string {
	return b.r.Fingerprint(ctx, name, entry)
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// fileHash é a impressão digital de um arquivo para o PollWatcher ("" se
// ilegível ou ausente).
func fileHash(path string) func() string {
	return func() string {
		b, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return hashOf(b)
	}
}

// sleepCtx espera d ou o fim de ctx (false).
func sleepCtx(ctx context.Context, clock shared.Clock, d time.Duration) bool {
	t := clock.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C():
		return true
	}
}

// readConfigRetry lê config.json repetindo as falhas que não são "não
// existe" (violação de compartilhamento).
// Parada pedida durante as esperas devolve ctx.Err().
func readConfigRetry(ctx context.Context, clock shared.Clock, path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	for _, d := range configRetryDelays {
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			break
		}
		if !sleepCtx(ctx, clock, d) {
			return nil, ctx.Err()
		}
		data, err = os.ReadFile(path)
	}
	return data, err
}

// waitTimeout espera wg por até d; false se estourou.
func waitTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// loadStartupConfig carrega config.json na partida e devolve também os bytes
// lidos (ou gravados pelo seed), dos quais o orquestrador guarda o hash. Se o
// arquivo não existe, gera pelo seed. Com erro, data traz o que foi lido
// (pode ser nil) e o arquivo nunca é sobrescrito.
func loadStartupConfig(ctx context.Context, clock shared.Clock, path string, seed config.SeedReader) (cfg config.Config, data []byte, boot config.Bootstrap, err error) {
	data, err = readConfigRetry(ctx, clock, path)
	if errors.Is(err, fs.ErrNotExist) {
		cfg, boot, err = config.LoadOrCreate(path, seed)
		if err != nil {
			return config.Config{}, nil, boot, err
		}
		if boot.Created {
			data, err = config.Marshal(cfg) // os mesmos bytes que config.Save gravou
			return cfg, data, boot, err
		}
		// Outro processo criou o arquivo entre as duas leituras: lê de novo
		// para guardar o hash do que está em disco.
		data, err = readConfigRetry(ctx, clock, path)
	}
	if err != nil {
		return config.Config{}, nil, boot, err
	}
	cfg, err = config.Parse(data)
	return cfg, data, boot, err
}

// aclNotes guarda as linhas do endurecimento da pasta de dados até o log
// (que fica dentro dela) abrir.
type aclNotes struct {
	mu    sync.Mutex
	lines []string
}

func (n *aclNotes) logf(format string, args ...any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lines = append(n.lines, fmt.Sprintf(format, args...))
}

func (n *aclNotes) take() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := n.lines
	n.lines = nil
	return out
}

// serve roda o serviço até ctx terminar. Ordem de subida: pipe (trava de
// instância única) → ACL da pasta → log → config.json → state.json → cofre →
// orquestrador → observadores → servidor do pipe. A parada encerra
// supervisores (discagens em curso desligam), avisa serviceStopping, grava o
// estado e fecha o pipe — sem derrubar VPNs conectadas, em até ~9 s. ready,
// se não nil, recebe o orquestrador quando tudo está de pé. Devolve erro só
// se a subida falhar (o SCM então aplica a recuperação).
func serve(ctx context.Context, p Platform, l layout, clock shared.Clock, ready func(*service.Orchestrator)) error {
	// O pipe é aberto antes de qualquer outra coisa: o go-winio cria a
	// primeira instância com FILE_FLAG_FIRST_PIPE_INSTANCE, então ele também é
	// a trava de instância única. Se outro vpnmon-svc já roda, saímos aqui sem
	// mexer na pasta dele (ACL, log), sem discar e sem tocar em
	// config.json/state.json.
	ln, err := p.Listen()
	if err != nil {
		msg := "abrindo o pipe " + ipc.PipeName + " (outro VPN Monitor em execução?): " + err.Error()
		p.Events.Error(msg)
		return errors.New(msg)
	}
	serving := false
	defer func() {
		if !serving {
			ln.Close()
		}
	}()

	// ACL da pasta antes de abrir log, config ou cofre. Quarentena (a pasta
	// não confiável foi posta de lado e recriada) é aviso; o resto é falha.
	var notes aclNotes
	changed, aclErr := p.ACL(notes.logf).EnsureDir(l.Dir)
	lines := notes.take()
	for _, n := range lines {
		p.Events.Warning("pasta de dados: " + n)
	}
	if aclErr != nil && !errors.Is(aclErr, acl.ErrQuarantined) {
		err := fmt.Errorf("protegendo a pasta de dados %s: %w", l.Dir, aclErr)
		p.Events.Error(err.Error())
		return err
	}
	if aclErr != nil {
		p.Events.Warning("pasta de dados: " + aclErr.Error())
	}
	for _, d := range []string{l.Credentials, filepath.Dir(l.LogFile)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			p.Events.Error("criando " + d + ": " + err.Error())
			return err
		}
	}
	lw, err := logging.OpenRotating(l.LogFile, int64(config.DefaultLogMaxSizeMB)<<20, config.DefaultLogMaxFiles)
	if err != nil {
		p.Events.Error("abrindo o log: " + err.Error())
		return err
	}
	defer lw.Close()
	level := new(slog.LevelVar)
	log := logging.New(lw, level)
	applyGlobals := func(c config.Config) {
		if lv, err := logging.ParseLevel(c.LogLevel); err == nil {
			level.Set(lv)
		}
		lw.SetLimits(int64(c.Log.MaxSizeMB)<<20, c.Log.MaxFiles)
	}
	for _, n := range lines {
		log.Warn("pasta de dados: " + n)
	}
	switch {
	case aclErr != nil:
		log.Warn("pasta de dados: " + aclErr.Error())
	case changed:
		log.Info("segurança da pasta de dados corrigida", "pasta", l.Dir)
	}

	cfg, cfgData, boot, cfgErr := loadStartupConfig(ctx, clock, l.ConfigFile, p.ReadSeed)
	if ctx.Err() != nil {
		// Parada pedida durante a partida (ex.: repetindo a leitura do
		// config.json): sai sem subir nada e sem o falso "inválido".
		log.Info("parada pedida durante a partida")
		return nil
	}
	if cfgErr != nil {
		// Degrada em vez de cair: sobe sem VPNs, não sobrescreve o arquivo e
		// espera ele ser corrigido (o observador recarrega).
		msg := "config.json inválido ou ilegível; o serviço segue sem VPNs até ser corrigido: " + cfgErr.Error()
		log.Error(msg)
		p.Events.Error(msg)
		cfg = config.Empty()
	} else if boot.FromSeed {
		log.Info("config.json criado a partir do seed do instalador")
	}
	if boot.SeedProblem != nil {
		log.Warn("seed do instalador ignorado", "erro", boot.SeedProblem)
		p.Events.Warning("seed do instalador ignorado: " + boot.SeedProblem.Error())
	}
	applyGlobals(cfg)

	st, err := config.LoadState(l.StateFile, clock.Now())
	var corrupt *config.CorruptStateError
	if errors.As(err, &corrupt) {
		log.Warn(corrupt.Error())
		p.Events.Warning(corrupt.Error())
	} else if err != nil {
		log.Error("lendo state.json", "erro", err)
	}

	vault := credentials.Vault{Dir: l.Credentials, DPAPI: p.DPAPI}
	creds := credBridge{credentials.Resolver{Vault: vault, RAS: p.RAS}}
	o := service.New(service.Options{
		Paths: l.Paths, Clock: clock, RAS: p.RAS, Pinger: p.Pinger,
		Link:   adapters.LinkProber{RAS: p.RAS, Net: p.Net},
		Dialer: &adapters.Dialer{RAS: p.RAS, Creds: creds, Clock: clock},
		Creds:  creds, Log: log, Events: p.Events, Rand: rand.Float64, OnGlobals: applyGlobals,
	}, cfg, st)
	if cfgErr == nil {
		// Em toda partida, não só no seed: o observador não recarrega o que
		// já está carregado e as mudanças pelo pipe podem gravar por cima.
		o.MarkWritten(cfgData)
	} else {
		o.MarkDiskInvalid(cfgErr) // não deixa "vpn add" sobrescrever o arquivo
	}
	octx, ocancel := context.WithCancel(context.Background())
	defer ocancel()
	o.Start(octx)

	var wg sync.WaitGroup
	wctx, wcancel := context.WithCancel(ctx)
	defer wcancel()
	cfgProbe := fileHash(l.ConfigFile)
	baseline := make(chan struct{})
	var baselineOnce sync.Once
	cfgWatch := shared.NewPollWatcher(clock, 250*time.Millisecond, time.Second, func() string {
		h := cfgProbe()
		baselineOnce.Do(func() { close(baseline) })
		return h
	})
	credWatch := shared.NewPollWatcher(clock, time.Second, time.Second, vault.DirFingerprint)
	rasDown, err := p.RAS.WatchDisconnects(wctx)
	if err != nil {
		log.Warn("sem aviso de desconexão do RAS; seguindo só com o temporizador", "erro", err)
	}
	netChanged := netwatch.Debounce(wctx, clock, p.Net.Changes(), netwatch.DefaultQuiet)
	reload := func() {
		err := o.ReloadFromDisk()
		for _, d := range configRetryDelays {
			if err == nil {
				return
			}
			if errors.Is(err, fs.ErrNotExist) {
				break // apagado: avisa na hora; recriado, o observador recarrega
			}
			log.Warn("lendo config.json; nova tentativa", "erro", err, "espera", d)
			if !sleepCtx(wctx, clock, d) {
				return
			}
			err = o.ReloadFromDisk()
		}
		if err != nil {
			o.ConfigUnreadable(err)
		}
	}
	wg.Add(4)
	go func() { defer wg.Done(); cfgWatch.Run(wctx) }()
	go func() { defer wg.Done(); credWatch.Run(wctx) }()
	// Recarga numa goroutine própria: as novas tentativas não atrasam os
	// avisos de rede/RAS/cofre.
	go func() {
		defer wg.Done()
		// O observador toma a linha de base na primeira amostra; uma edição
		// entre a leitura da partida e essa amostra seria perdida.
		select {
		case <-wctx.Done():
			return
		case <-baseline:
		}
		if cfgProbe() != hashOf(cfgData) {
			reload()
		}
		for {
			select {
			case <-wctx.Done():
				return
			case <-cfgWatch.C():
				reload()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-wctx.Done():
				return
			case <-credWatch.C():
				o.CredentialsChanged()
			case <-rasDown:
				o.Wake()
			case <-netChanged:
				o.Wake()
			}
		}
	}()

	srv := &ipc.Server{Backend: o, AppVersion: version, Log: log}
	sctx, scancel := context.WithCancel(context.Background())
	defer scancel()
	srvErr := make(chan error, 1)
	serving = true
	go func() { srvErr <- srv.Serve(sctx, ln) }()

	log.Info("serviço iniciado", "versao", version, "vpns", len(cfg.VPNs))
	p.Events.Info(fmt.Sprintf("VPN Monitor %s iniciado", version))
	if ready != nil {
		ready(o)
	}

	// Serve só volta sozinho se o listener morreu (falhas transitórias de
	// Accept ele mesmo repete). Recriar o pipe deixaria o nome livre para
	// outro processo criá-lo antes (squatting); então o serviço para de forma
	// ordenada e devolve erro: o svc.Loop sai ≠ 0 e a recuperação do SCM o
	// reinicia com o pipe novo.
	var fatal error
	select {
	case <-ctx.Done():
		log.Info("parando o serviço")
	case err := <-srvErr:
		fatal = fmt.Errorf("o pipe %s deixou de funcionar; parando para o SCM reiniciar o serviço: %v", ipc.PipeName, err)
		log.Error(fatal.Error())
		p.Events.Error(fatal.Error())
	}
	wcancel()
	o.Stop() // supervisores em paralelo com prazo de 7 s; grava state.json; VPNs ficam de pé
	ocancel()
	if !waitTimeout(&wg, watchersStopWait) {
		log.Warn("observadores não terminaram no prazo; seguindo com a parada")
	}
	if fatal == nil {
		scancel()
		select {
		case <-srvErr:
		case <-time.After(serverStopWait):
			log.Warn("servidor do pipe não terminou no prazo; seguindo com a parada")
		}
	}
	log.Info("serviço parado")
	p.Events.Info("VPN Monitor parado")
	return fatal
}

// serviceMain é o caminho quando o SCM inicia o processo. O código de saída
// do serviço (0 em parada pedida, mesmo estourando o prazo; ≠ 0 se Run
// falhar) é decidido por svc.Loop e informado ao SCM.
func serviceMain(e env) int {
	var orch atomic.Pointer[service.Orchestrator]
	var resuming atomic.Bool
	err := e.runService(svc.Hooks{
		Run: func(ctx context.Context) error {
			l, err := paths(e)
			if err != nil {
				return err
			}
			p, err := e.platform()
			if err != nil {
				return err
			}
			return serve(ctx, p, l, shared.RealClock{}, orch.Store)
		},
		// Rápido e idempotente: uma retomada chega duas vezes
		// (PBT_APMRESUMEAUTOMATIC e PBT_APMRESUMESUSPEND). PowerResume fala com
		// cada supervisor; roda à parte e uma vez por vez.
		OnResume: func() {
			o := orch.Load()
			if o == nil || !resuming.CompareAndSwap(false, true) {
				return
			}
			go func() {
				defer resuming.Store(false)
				o.PowerResume()
			}()
		},
		StopTimeout: svc.DefaultStopTimeout,
	})
	if err != nil {
		fmt.Fprintf(e.stderr, "erro: %v\n", err)
		return 1
	}
	return 0
}

// cmdRun roda a mesma montagem do serviço no console, até Ctrl+C.
func cmdRun(e env) error {
	l, err := paths(e)
	if err != nil {
		return err
	}
	p, err := e.platform()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fmt.Fprintf(e.stdout, "VPN Monitor %s em modo console; log em %s; Ctrl+C para parar\n", version, l.LogFile)
	return serve(ctx, p, l, shared.RealClock{}, nil)
}
