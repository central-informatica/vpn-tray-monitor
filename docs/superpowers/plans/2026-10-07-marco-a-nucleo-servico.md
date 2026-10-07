# Marco A — Núcleo e Serviço: Plano de Implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Entregar o `vpnmon-svc.exe` da v2 — serviço do Windows instalável por `vpnmon-svc install`, operável pela CLI, que mantém N VPNs RAS de pé — com testes de unidade no Linux e de integração no job Windows do CI.

**Architecture:** Feature-first sobre `core` e `shared` (§3.1). Todo acesso ao SO passa por interfaces em `internal/core/platform/*` (implementação `_windows.go`, stub `!windows`, fakes em `platform/fake`), então a lógica inteira roda no Linux. Cada VPN tem um supervisor-ator cuja política é a função pura `domain.Decide`; um orquestrador cria/recria supervisores, serializa as discagens numa fila global, persiste pausas e serve o named pipe `\\.\pipe\vpnmon` (JSON por linha) para a CLI (e, no Marco B, a bandeja).

**Tech Stack:** Go 1.26 (`go 1.26.5`), `log/slog`, `golang.org/x/sys` v0.47.0 (windows, registry, svc, svc/mgr, svc/eventlog), `github.com/Microsoft/go-winio` v0.6.2 (named pipe), GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-10-07-vpn-monitor-v2-design.md` (o Marco A está na §13; leia as §§3–6, 8, 11 e 12 antes de começar).

## Global Constraints

- Go `1.26.5` no `go.mod`; build de produção com `CGO_ENABLED=0`, `GOOS=windows GOARCH=amd64` (só x64).
- Dependências permitidas no Marco A: **só** `golang.org/x/sys` e `github.com/Microsoft/go-winio`. Nada de `walk` (Marco B).
- Estrutura da §3.1 e regras da §3.2: `features/*` dependem de `core` e `shared`; **nenhuma feature importa outra** (a montagem em `cmd/` liga `credentials` ao `monitor`); todo acesso ao SO via interfaces em `core/platform`; implementação em `_windows.go`, stub com `//go:build !windows`, fakes em `core/platform/fake`.
- Toda tarefa que toca código de plataforma termina com `go vet ./... && GOOS=windows go vet ./...` limpo (o `go vet` compila também os `*_windows_test.go`).
- RAS: funções via `windows.NewLazySystemDLL("rasapi32.dll")`; estruturas `RASDIALPARAMSW`, `RASCONNSTATUSW`, `RASCONNW` montadas byte a byte com empacotamento de 4 bytes (`pshpack4`) e deslocamentos fixados por teste; **um único** `windows.NewCallback` global, que só retorna; `RasHangUp` nunca dentro do callback; buffers do `RasDialW` vivos até o fim da discagem.
- Tabela de erros RAS num único arquivo (`internal/core/platform/ras/codes.go`), cada código como constante com o nome do `raserror.h`/`winerror.h`; código não listado é transitório.
- Backoff exponencial com jitter de ±20 %, do `intervalSeconds` até `maxBackoffSeconds`.
- Padrões e limites da config (§5.2): `check.kind` ping|tcp|link (padrão ping); `check.timeoutSeconds` 5 (1–30); `intervalSeconds` 30 (5–3600); `failuresBeforeReconnect` 3 (1–20); `graceAfterConnectSeconds` 15 (0–300); `connectTimeoutSeconds` 60 (10–300); `maxBackoffSeconds` 300 (≥ `intervalSeconds`, ≤ 3600); `name` 1–64 caracteres, único sem diferenciar maiúsculas e imutável.
- Pasta `C:\ProgramData\VPNMonitor\` com `config.json`, `state.json`, `credentials\<id>.bin`, `logs\vpnmon.log` (+ `.1`…`.5`); dono Administradores e ACL `D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)`, verificados e corrigidos a cada início.
- Pipe `\\.\pipe\vpnmon`: JSON por linha `{"v":1,"id":"…","type":"…","payload":{…}}`, mensagem máxima de 64 KB, no máximo 32 conexões, decodificação estrita, SDDL com SYSTEM/Administradores total, Usuários Interativos leitura e escrita **sem** criar instância (`0x0012019b`), Rede negada; conexão ociosa sem inscrição fecha em 2 min; cliente confere o PID do servidor contra o do serviço `VPNMonitor`.
- Serviço `VPNMonitor`: início automático, dependência `RasMan`, LocalSystem; aceita `stop`, `preshutdown`, `powerevent`; parada em ≤ 10 s (supervisores têm prazo global de 7 s e o estado é gravado mesmo que algum não volte); **parar, atualizar ou remover nunca derruba VPN conectada**.
- Nenhum segredo em log, erro ou protocolo: senhas em `shared.Secret`; o marcador da senha salva no Windows nunca é `Secret` nem é logado — só o hash dele é comparado.
- Textos ao usuário em português; commits convencionais em pt-BR; nada de dados de cliente no repositório.

## Review Focus

Entradas e condições que o spec implica, que nenhum teste "natural" das tarefas pegaria e que mais provavelmente atingiriam quem usa o serviço — cada uma já tem teste na tarefa dona:

1. **Catálogo `rasphone.pbk` em "ANSI" (Windows-1252) com acentos** (`[Conexão]` gravado como `0xE3`): o nome tem de aparecer igual ao do Windows em `listRasEntries`/`ErroConfig`, não como `Conex\uFFFDo` — `TestParsePhonebookCP1252` (Task 7).
2. **Relógio de parede ajustado para trás** (NTP, horário mudado à mão): não pode virar "retomada de suspensão" falsa nem zerar backoff à toa — `TestResumeDetector` (Task 1).
3. **`config.json` momentaneamente vazio ou pela metade** enquanto um editor grava: o serviço mantém a config anterior e não reinicia supervisores — `TestReloadFromDisk` (Task 22).
4. **Usuário ou senha acima de 256 caracteres UTF-16** em `credential set`: recusa clara na gravação, em vez de uma discagem que falha sempre depois — `TestVaultSetGetClear` (Task 16).
5. **Senha por `--password-stdin` vinda do PowerShell** (termina em `\r\n`, pode ter espaços nas pontas): remove só o fim de linha — `TestCredentialSetClearList` (Task 23).

## Mapa de arquivos

```
cmd/vpnmon-svc/            main.go, cli.go (CLI local), pipecmds.go (status/vpn/check), app.go (montagem do serviço),
                           paths.go, platform.go (+ _windows/_other)
internal/shared/           clock.go (Clock, FakeClock, ResumeDetector), backoff.go, secret.go, atomicfile.go, pollwatch.go
internal/core/config/      config.go (tipos, padrões, Parse/Save), validate.go, seed.go (+ seed_windows/_other), state.go
internal/core/logging/     rotate.go, logger.go, tail.go, eventlog.go (+ _windows/_other)
internal/core/ipc/         protocol.go, codec.go, server.go, client.go, pipe_windows.go, pipe_other.go
internal/core/platform/    platform.go (ErrNotSupported)
  ras/                     codes.go (tabela única), layout.go (pshpack4), pbk.go, client.go (interface), ras_windows.go, ras_other.go
  icmp/ dpapi/ netwatch/ acl/ svc/   interface + _windows.go + _other.go cada
  fake/                    ras.go, icmp.go, dpapi.go, netwatch_acl.go
internal/features/
  monitor/domain/          state.go, decide.go (ciclo), commands.go (comandos) — funções puras
  monitor/adapters/        checkers.go, link.go, dialer.go
  monitor/service/         queue.go, supervisor.go, bus.go, view.go, orchestrator.go, configops.go
  credentials/             vault.go, resolver.go
Makefile, .github/workflows/ci.yml, README.md
```

## Decisões tomadas neste plano (não estavam no spec)

- **A v1 sai na Task 1**, não no fim: o `go.mod` dela traz `fyne.io/systray` e `billgraziano/dpapi`, que quebrariam o `go mod tidy -diff` do lint, e dois pacotes `config`/`logging` paralelos confundiriam a implementação. O que vale reaproveitar está transcrito nas Tasks 7–9; `assets/` e `tools/geniconos` ficam para o Marco B.
- **Observação de `config.json` e `credentials\` por amostragem** (`shared.PollWatcher`), não `ReadDirectoryChangesW`.
- **Salto de relógio** detectado por tique periódico (parede andou mais que período + 2× o menor intervalo), robusto à semântica do relógio monotônico no Windows.
- **`Decide` devolve a espera dentro do estado** (`Status.NextTick`) e recebe `Env{Now, Rand}` (o aleatório do jitter mantém a função pura).
- **Pedido `logTail`** acrescentado ao protocolo (a §7 pede "Abrir log" pelo pipe, a §6.2 não o lista); respostas são `type:"ok"`/`type:"error"`.
- **Seed lido por `config.ReadSeedRegistry`** (`x/sys/windows/registry`), injetado como `config.SeedReader`.
- **Config inválida no início** (sem anterior para manter): o serviço sobe sem VPNs, registra no log e no Event Log e espera o arquivo ser corrigido — não sobrescreve.
- **`Desconectada` × `Reconectando`**: `Reconectando` cobre "discando" e "aguardando a próxima tentativa após falha" (como no menu da §7); `Desconectada` é o enlace visto caído enquanto o backoff ainda não permite discar.
- **15 min de `reconnect` em `CredencialInvalida`** contam da tentativa mais recente — manual ou a rejeição automática (a §4.7 do spec foi ajustada junto).
- **O pipe é a trava de instância única** e é aberto antes de tudo na subida.
- **Dono da pasta de dados = Administradores** (além da DACL), para quem pré-criar a pasta não manter `WRITE_DAC`.
- **Mudanças pelo pipe são recusadas enquanto o `config.json` em disco estiver inválido.**
- **Conexões ociosas não inscritas** fecham em 2 min.

## Como verificar cada tarefa

Cada tarefa traz o teste, o comando que deve falhar antes e passar depois, e o commit. Testes `*_windows_test.go` **só rodam no job
`test-windows` do CI** (o `ci.yml` da Task 1 já roda `go test -race ./...` no `windows-latest`); localmente no Linux eles só são
compilados, via `GOOS=windows go vet ./...`. Ao fim de cada tarefa: `make lint` limpo.

O código deste plano já foi executado tarefa a tarefa numa cópia do repositório (Linux, Go 1.26.5): cada teste falha antes da
implementação e passa depois, com `gofmt` e `go vet` (linux e windows) limpos e o commit de cada tarefa. Os `*_windows_test.go` só foram
compilados — o primeiro push no CI é a validação real deles; se algum falhar, corrija na tarefa dona antes de seguir.

---

### Task 1: Remover a v1 e montar o esqueleto (Makefile, CI mínimo, relógio injetável)

**Files:**
- Delete: `cmd/vpnmon/`
- Delete: `internal/config/`
- Delete: `internal/logging/`
- Delete: `internal/state/`
- Delete: `internal/supervisor/`
- Delete: `internal/trayui/`
- Delete: `internal/vpn/`
- Delete: `internal/winsys/`
- Delete: `exemplos/`
- Create: `.github/workflows/ci.yml`
- Create: `internal/shared/clock.go`
- Modify: `Makefile` (reescrito inteiro)
- Modify: `README.md` (reescrito inteiro)
- Modify: `.gitignore` (reescrito inteiro)
- Modify: `go.mod` (sem dependências da v1)
- Test: `internal/shared/clock_test.go`

**Interfaces:**
- Consumes: nada (primeira tarefa).
- Produces: `shared.Clock` (`Now() time.Time`, `NewTimer(d) Timer`), `shared.Timer` (`C() <-chan time.Time`, `Stop() bool`, `Reset(d)`),
  `shared.RealClock{}`, `shared.NewFakeClock(start) *FakeClock` com `Advance(d)`, `Suspend(d)` (só parede), `ActiveTimers() int`,
  `WaitForTimers(n, timeout) bool`, `WaitForDeadline(rel, timeout) bool`; `shared.ResumeDetector{Period, Threshold}` com `Observe(now) bool`.
  Alvos `make lint`, `make test`, `make cover`, `make build`.

- [ ] **Step 1: Remover a v1 e as dependências dela**

A v1 sai **na primeira tarefa**: o `go.mod` dela puxa `fyne.io/systray` e `github.com/billgraziano/dpapi`, que a v2 não usa, e o
`go mod tidy -diff` do lint quebraria enquanto os dois convivessem; além disso dois pacotes `config`/`logging` paralelos confundiriam quem
implementa. O que vale reaproveitar (parse do `rasphone.pbk` ANSI/UTF-16, ICMP via `iphlpapi`, notas da discagem por `RasDialW`) está
transcrito nas tarefas 7–9, e a v1 inteira continua no histórico (`997007a`). `assets/` e `tools/geniconos` ficam: são só biblioteca padrão e
o Marco B usa os ícones.

```bash
git rm -r -q cmd/vpnmon internal/config internal/logging internal/state internal/supervisor \
  internal/trayui internal/vpn internal/winsys exemplos
printf 'module github.com/guibsu/vpn-tray-monitor\n\ngo 1.26.5\n' > go.mod
rm -f go.sum
go build ./... 2>&1 | head -3   # esperado: nada a compilar além de tools/geniconos
```

- [ ] **Step 2: Escrever o teste que falha**

`internal/shared/clock_test.go`:

```go
package shared

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func TestFakeClockFiresInOrderOnAdvance(t *testing.T) {
	c := NewFakeClock(t0)
	a := c.NewTimer(10 * time.Second)
	b := c.NewTimer(5 * time.Second)

	c.Advance(5 * time.Second)
	select {
	case got := <-b.C():
		if !got.Equal(t0.Add(5 * time.Second)) {
			t.Fatalf("b disparou em %v", got)
		}
	default:
		t.Fatal("b deveria ter disparado")
	}
	select {
	case <-a.C():
		t.Fatal("a não deveria ter disparado")
	default:
	}
	c.Advance(5 * time.Second)
	select {
	case <-a.C():
	default:
		t.Fatal("a deveria ter disparado")
	}
	if n := c.ActiveTimers(); n != 0 {
		t.Fatalf("ActiveTimers = %d, quer 0", n)
	}
}

func TestFakeClockStopAndReset(t *testing.T) {
	c := NewFakeClock(t0)
	tm := c.NewTimer(time.Second)
	if !tm.Stop() {
		t.Fatal("Stop de temporizador armado deve devolver true")
	}
	c.Advance(2 * time.Second)
	select {
	case <-tm.C():
		t.Fatal("temporizador parado disparou")
	default:
	}
	tm.Reset(3 * time.Second)
	c.Advance(2 * time.Second)
	select {
	case <-tm.C():
		t.Fatal("disparou antes do prazo")
	default:
	}
	c.Advance(time.Second)
	select {
	case <-tm.C():
	default:
		t.Fatal("Reset não rearmou")
	}
}

func TestFakeClockSuspendMovesOnlyWall(t *testing.T) {
	c := NewFakeClock(t0)
	tm := c.NewTimer(time.Minute)
	c.Suspend(time.Hour)
	if got := c.Now(); !got.Equal(t0.Add(time.Hour)) {
		t.Fatalf("Now = %v", got)
	}
	select {
	case <-tm.C():
		t.Fatal("suspensão não deve disparar temporizadores")
	default:
	}
}

func TestFakeClockWaitForTimers(t *testing.T) {
	c := NewFakeClock(t0)
	go func() {
		time.Sleep(10 * time.Millisecond)
		c.NewTimer(time.Second)
	}()
	if !c.WaitForTimers(1, time.Second) {
		t.Fatal("WaitForTimers não viu o temporizador")
	}
	if c.WaitForTimers(2, 20*time.Millisecond) {
		t.Fatal("WaitForTimers deveria esgotar o prazo")
	}
}

func TestFakeClockWaitForDeadline(t *testing.T) {
	c := NewFakeClock(t0)
	tm := c.NewTimer(5 * time.Second)
	c.Advance(time.Second)
	if !c.WaitForDeadline(4*time.Second, 10*time.Millisecond) {
		t.Fatal("deveria ver o prazo restante de 4s")
	}
	go tm.Reset(10 * time.Second)
	if !c.WaitForDeadline(10*time.Second, time.Second) {
		t.Fatal("deveria ver o Reset feito por outra goroutine")
	}
}

func TestResumeDetectorIgnoresMonotonicReading(t *testing.T) {
	d := ResumeDetector{Period: 5 * time.Second, Threshold: time.Minute}
	now := time.Now() // com leitura monotônica, como o RealClock devolve
	d.Observe(now)
	if strings.Contains(d.last.String(), "m=") {
		t.Fatalf("o detector deve guardar só a parede, guardou %s", d.last)
	}
	// Após uma suspensão real, a parede pulou e a leitura monotônica não; um
	// tempo só-parede no futuro reproduz isso para a comparação feita.
	if !d.Observe(now.Round(0).Add(2 * time.Hour)) {
		t.Fatal("salto de parede de 2 h deveria ser detectado")
	}
	if d.Observe(time.Now().Add(2*time.Hour + 5*time.Second)) {
		t.Fatal("tique normal (com leitura monotônica) após o salto não é salto")
	}
}

func TestResumeDetector(t *testing.T) {
	d := ResumeDetector{Period: 5 * time.Second, Threshold: 60 * time.Second}
	if d.Observe(t0) {
		t.Fatal("primeira observação nunca é salto")
	}
	if d.Observe(t0.Add(5 * time.Second)) {
		t.Fatal("tique normal não é salto")
	}
	if d.Observe(t0.Add(70 * time.Second)) {
		t.Fatal("65s com limite de 5+60 não é salto")
	}
	if !d.Observe(t0.Add(70*time.Second + 66*time.Second)) {
		t.Fatal("66s depois deveria ser salto")
	}
	// Relógio de parede ajustado para trás (NTP, ajuste manual): não é retomada.
	if d.Observe(t0.Add(-time.Hour)) {
		t.Fatal("relógio voltando não é salto de suspensão")
	}
	if d.Observe(t0.Add(-time.Hour + 5*time.Second)) {
		t.Fatal("tique normal após o ajuste não é salto")
	}
}
```

- [ ] **Step 3: Rodar e confirmar a falha**

Run: `go test ./internal/shared/`  
Expected: FAIL — `undefined: NewFakeClock` (o pacote ainda não existe).

- [ ] **Step 4: Implementar**

`clock.go` separa relógio de parede e tempo dos temporizadores para simular suspensão. `ResumeDetector` implementa o "salto de
relógio" da §4.6 de forma robusta à semântica do relógio monotônico no Windows: num tique periódico, se o relógio de parede andou mais que
`Period + Threshold`, a máquina dormiu. A comparação descarta a leitura monotônica (`Round(0)`): com ela, `t.Sub(u)` usaria o
relógio monotônico, que não anda na suspensão, e o salto nunca apareceria com o `RealClock`. Relógio voltando (NTP) nunca é salto.

As actions do CI estão fixadas por SHA (conferido com `git ls-remote --tags`: checkout `v7.0.1` = `3d3c42e5…`, setup-go `v7.0.0` = `b7ad1dad…`).

`internal/shared/clock.go`:

```go
// Package shared reúne utilitários sem dependência de SO usados por todas as
// camadas: relógio, backoff, segredo, gravação atômica e observação de arquivos.
package shared

import (
	"sort"
	"sync"
	"time"
)

// Clock abstrai o tempo para que a lógica de espera seja testável sem dormir.
type Clock interface {
	// Now devolve o relógio de parede.
	Now() time.Time
	// NewTimer cria um temporizador que dispara uma vez após d.
	NewTimer(d time.Duration) Timer
}

// Timer é o subconjunto de *time.Timer que o código usa.
type Timer interface {
	C() <-chan time.Time
	// Stop desarma; devolve false se já tinha disparado ou parado.
	Stop() bool
	// Reset rearma para disparar após d.
	Reset(d time.Duration)
}

// RealClock usa o pacote time.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time   { return r.t.C }
func (r realTimer) Stop() bool            { return r.t.Stop() }
func (r realTimer) Reset(d time.Duration) { r.t.Reset(d) }

// FakeClock é um relógio manual para testes. Ele separa o relógio de parede
// do tempo que os temporizadores enxergam, para simular suspensão: Advance
// move os dois; Suspend move só o de parede. Now devolve tempos sem leitura
// monotônica (só parede), que é o que ResumeDetector compara.
type FakeClock struct {
	mu      sync.Mutex
	wall    time.Time
	mono    time.Duration
	timers  []*fakeTimer
	changed chan struct{}
}

// NewFakeClock cria um relógio parado em start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{wall: start, changed: make(chan struct{})}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wall
}

func (c *FakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, ch: make(chan time.Time, 1), deadline: c.mono + d, active: true}
	c.timers = append(c.timers, t)
	if d <= 0 {
		c.fireLocked()
	}
	c.notifyLocked()
	return t
}

// Advance avança parede e monotônico e dispara, em ordem, os temporizadores vencidos.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mono += d
	c.wall = c.wall.Add(d)
	c.fireLocked()
	c.notifyLocked()
}

// Suspend avança só o relógio de parede, como numa suspensão da máquina.
func (c *FakeClock) Suspend(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
	c.notifyLocked()
}

// ActiveTimers conta os temporizadores armados e ainda não disparados.
func (c *FakeClock) ActiveTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if t.active {
			n++
		}
	}
	return n
}

// WaitForTimers bloqueia até haver pelo menos n temporizadores armados ou o
// prazo real esgotar. Serve para sincronizar o teste com uma goroutine que
// ainda vai armar o próximo temporizador.
func (c *FakeClock) WaitForTimers(n int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		c.mu.Lock()
		count := 0
		for _, t := range c.timers {
			if t.active {
				count++
			}
		}
		ch := c.changed
		c.mu.Unlock()
		if count >= n {
			return true
		}
		select {
		case <-ch:
		case <-deadline:
			return false
		}
	}
}

// WaitForDeadline bloqueia até existir um temporizador armado que dispare
// exatamente daqui a rel (no relógio dos temporizadores), ou o prazo real
// esgotar. Sincroniza o teste com um Reset feito por outra goroutine.
func (c *FakeClock) WaitForDeadline(rel time.Duration, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		c.mu.Lock()
		found := false
		for _, t := range c.timers {
			if t.active && t.deadline-c.mono == rel {
				found = true
			}
		}
		ch := c.changed
		c.mu.Unlock()
		if found {
			return true
		}
		select {
		case <-ch:
		case <-deadline:
			return false
		}
	}
}

func (c *FakeClock) fireLocked() {
	sort.SliceStable(c.timers, func(i, j int) bool { return c.timers[i].deadline < c.timers[j].deadline })
	keep := c.timers[:0]
	for _, t := range c.timers {
		if t.active && t.deadline <= c.mono {
			t.active = false
			select {
			case t.ch <- c.wall:
			default:
			}
			continue
		}
		if t.active {
			keep = append(keep, t)
		}
	}
	c.timers = keep
}

func (c *FakeClock) notifyLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

type fakeTimer struct {
	clock    *FakeClock
	ch       chan time.Time
	deadline time.Duration
	active   bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	was := t.active
	t.active = false
	c.notifyLocked()
	return was
}

func (t *fakeTimer) Reset(d time.Duration) {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	select { // descarta disparo antigo não lido
	case <-t.ch:
	default:
	}
	t.deadline = c.mono + d
	if !t.active {
		t.active = true
		c.timers = append(c.timers, t)
	}
	if d <= 0 {
		c.fireLocked()
	}
	c.notifyLocked()
}

// ResumeDetector percebe que a máquina ficou suspensa: o tique periódico
// (medido pelo relógio monotônico dos temporizadores) chega e o relógio de
// parede avançou bem mais que o período. Cobre o modern standby, que nem
// sempre avisa o serviço com PBT_APMRESUMEAUTOMATIC.
//
// A comparação é só de parede: time.Now() carrega também a leitura
// monotônica, e t.Sub(u) usaria ela — que não anda durante a suspensão —
// escondendo justamente o salto. Por isso Observe descarta a leitura
// monotônica com Round(0).
type ResumeDetector struct {
	Period    time.Duration // período do tique
	Threshold time.Duration // folga além do período que caracteriza suspensão
	last      time.Time
}

// Observe registra um tique no instante de parede now e diz se houve salto.
func (d *ResumeDetector) Observe(now time.Time) bool {
	now = now.Round(0) // só relógio de parede
	if d.last.IsZero() {
		d.last = now
		return false
	}
	elapsed := now.Sub(d.last)
	d.last = now
	return elapsed > d.Period+d.Threshold
}
```

`Makefile`:

```makefile
# Espelha o CI (.github/workflows/ci.yml). Rode `make lint test` antes de abrir PR.
BUILD   := build
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -buildid= -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
WIN     := GOOS=windows GOARCH=amd64

.PHONY: all lint test cover build clean

all: lint test build

lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt pendente:"; echo "$$out"; exit 1; fi
	go mod tidy -diff
	go vet ./...
	$(WIN) go vet ./...

test:
	go test -race -shuffle=on ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

build:
	@mkdir -p $(BUILD)
	$(WIN) CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BUILD)/vpnmon-svc.exe ./cmd/vpnmon-svc
	@ls -lh $(BUILD)/*.exe

clean:
	rm -rf $(BUILD) coverage.out
```

`.github/workflows/ci.yml`:

```yaml
# CI mínimo do Marco A: lint, testes no Linux com -race e testes no Windows
# (inclui os testes de integração *_windows_test.go). O CI completo
# (golangci-lint, govulncheck, cobertura, build do MSI, e2e) vem no Marco C.
name: ci

on:
  push:
    branches: [main, "feat/**"]
  pull_request:

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true

permissions:
  contents: read

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - run: make lint

  test-linux:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - run: go test -race -shuffle=on ./...

  test-windows:
    runs-on: windows-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      # -race exige cgo: usa o gcc MinGW que o runner já traz.
      - name: go test -race (inclui integração Windows)
        shell: bash
        env:
          CGO_ENABLED: "1"
        run: go test -race ./...
      # Produção é sem cgo.
      - name: build sem cgo
        shell: bash
        env:
          CGO_ENABLED: "0"
        run: go build -trimpath -o vpnmon-svc.exe ./cmd/vpnmon-svc
```

`README.md`:

````markdown
# VPN Monitor

Mantém VPNs nativas do Windows (RAS) sempre conectadas, com ou sem usuário
logado: um serviço (`vpnmon-svc.exe`) supervisiona cada VPN e reconecta após
quedas, túneis zumbis, suspensão e trocas de rede.

> **Em desenvolvimento (v2).** O desenho completo está em
> [`docs/superpowers/specs/2026-10-07-vpn-monitor-v2-design.md`](docs/superpowers/specs/2026-10-07-vpn-monitor-v2-design.md).
> Marco A (núcleo e serviço): serviço instalável por `vpnmon-svc install` e
> operado pela CLI. A bandeja (Marco B) e o MSI/CI completo (Marco C) vêm depois.

## Desenvolvimento

```sh
make lint    # gofmt, go mod tidy, go vet (linux e windows)
make test    # go test -race -shuffle=on ./...
make build   # build/vpnmon-svc.exe (GOOS=windows, CGO_ENABLED=0)
```

Toda a lógica roda e é testada no Linux com fakes (`internal/core/platform/fake`);
os testes `*_windows_test.go` rodam no job Windows do CI.

## Uso (prompt de administrador)

```text
vpnmon-svc install                      registra o serviço VPNMonitor
sc start VPNMonitor
vpnmon-svc vpn add --name Matriz --entry "VPN Matriz" --check ping --host 10.254.1.172
vpnmon-svc credential set Matriz --user dominio\usuario
vpnmon-svc status
```

A entrada RAS precisa existir para todos os usuários
(`Add-VpnConnection -AllUserConnection`): o serviço roda como LocalSystem e só
enxerga esse catálogo.
````

`.gitignore`:

```gitignore
# Artefatos de build
build/
dist/
*.exe
coverage.out

# Dados locais
*.log

# Script da VM de testes local (contém credenciais da VM)
vm.sh
```

- [ ] **Step 5: Rodar os testes e confirmar que passam**

Run: `gofmt -l . && go vet ./... && GOOS=windows go vet ./...`  
Expected: sem saída

Run: `go test -race ./internal/shared/`  
Expected: PASS

Run: `make lint && make test`  
Expected: sem erros

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "chore: remove a v1 e cria o esqueleto da v2 (Makefile, CI mínimo, relógio injetável)"
```

---

### Task 2: shared: backoff com jitter e tipo Secret

**Files:**
- Create: `internal/shared/backoff.go`
- Create: `internal/shared/secret.go`
- Test: `internal/shared/backoff_test.go`
- Test: `internal/shared/secret_test.go`

**Interfaces:**
- Consumes: nada.
- Produces: `shared.Backoff{Base, Max time.Duration; Jitter float64}` com `Delay(n int, r float64) time.Duration` (pura; `r` em [0,1));
  `shared.Secret` com `NewSecret(s) Secret`, `Reveal() string`, `IsEmpty() bool`, `Wipe()`; `String`/`%v`/JSON/`slog` mostram `***`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/shared/backoff_test.go`:

```go
package shared

import (
	"testing"
	"time"
)

func TestBackoffGrowsAndCaps(t *testing.T) {
	b := Backoff{Base: 30 * time.Second, Max: 300 * time.Second, Jitter: 0.2}
	cases := []struct {
		n    int
		want time.Duration
	}{
		{0, 30 * time.Second}, {1, 30 * time.Second}, {2, 60 * time.Second},
		{3, 120 * time.Second}, {4, 240 * time.Second}, {5, 300 * time.Second},
		{50, 300 * time.Second},
	}
	for _, c := range cases {
		if got := b.Delay(c.n, 0.5); got != c.want { // r=0,5 → sem variação
			t.Errorf("Delay(%d) = %v, quer %v", c.n, got, c.want)
		}
	}
}

func TestBackoffJitterLimits(t *testing.T) {
	b := Backoff{Base: 30 * time.Second, Max: 300 * time.Second, Jitter: 0.2}
	if got := b.Delay(2, 0); got != 48*time.Second {
		t.Errorf("r=0 → %v, quer 48s (−20%%)", got)
	}
	if got := b.Delay(2, 0.999999); got < 71*time.Second || got > 72*time.Second {
		t.Errorf("r→1 → %v, quer ≈72s (+20%%)", got)
	}
	if got := b.Delay(10, 0.999999); got != 300*time.Second {
		t.Errorf("teto com jitter positivo = %v, quer 300s", got)
	}
	if got := b.Delay(1, 0); got != 24*time.Second {
		t.Errorf("piso = %v, quer 24s", got)
	}
}

func TestBackoffBaseAboveMax(t *testing.T) {
	b := Backoff{Base: 600 * time.Second, Max: 300 * time.Second}
	if got := b.Delay(3, 0.5); got != 600*time.Second {
		t.Errorf("base acima do teto deve valer a base, veio %v", got)
	}
}
```

`internal/shared/secret_test.go`:

```go
package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestSecretNeverLeaks(t *testing.T) {
	s := NewSecret("hunter2")
	outs := []string{
		s.String(),
		fmt.Sprint(s), fmt.Sprintf("%v %+v %#v %s %q %x", s, s, s, s, s, s),
	}
	j, _ := json.Marshal(struct{ P Secret }{s})
	outs = append(outs, string(j))
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "senha", s)
	outs = append(outs, buf.String())
	for _, o := range outs {
		if strings.Contains(o, "hunter2") {
			t.Fatalf("segredo vazou em %q", o)
		}
	}
	if s.Reveal() != "hunter2" {
		t.Fatal("Reveal deve devolver o valor")
	}
}

func TestSecretWipe(t *testing.T) {
	s := NewSecret("abc")
	s.Wipe()
	if s.Reveal() != "\x00\x00\x00" {
		t.Fatalf("Wipe não zerou: %q", s.Reveal())
	}
	if !NewSecret("").IsEmpty() || s.IsEmpty() {
		t.Fatal("IsEmpty incorreto")
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/shared/`  
Expected: FAIL — `undefined: Backoff`, `undefined: NewSecret`.

- [ ] **Step 3: Implementar**

O backoff corrige o bug da v1 ("base acima do teto"): o resultado nunca passa de `Max` e a base acima do teto vale a base.

`internal/shared/backoff.go`:

```go
package shared

import "time"

// Backoff calcula a espera antes da tentativa n (n ≥ 1): Base·2^(n-1),
// limitada a Max, com variação aleatória de ±Jitter (fração, ex.: 0,2).
// O resultado nunca passa de Max nem fica abaixo de Base·(1-Jitter).
type Backoff struct {
	Base   time.Duration
	Max    time.Duration
	Jitter float64
}

// Delay devolve a espera da tentativa n. r é um número em [0,1) — injetado
// para manter a função pura e testável.
func (b Backoff) Delay(n int, r float64) time.Duration {
	if n < 1 {
		n = 1
	}
	if b.Max < b.Base {
		b.Max = b.Base
	}
	d := b.Base
	for i := 1; i < n && d < b.Max; i++ {
		d *= 2
	}
	if d > b.Max {
		d = b.Max
	}
	factor := 1 + b.Jitter*(2*r-1)
	j := time.Duration(float64(d) * factor)
	if j > b.Max {
		j = b.Max
	}
	if lo := time.Duration(float64(b.Base) * (1 - b.Jitter)); j < lo {
		j = lo
	}
	return j
}
```

`internal/shared/secret.go`:

```go
package shared

import (
	"fmt"
	"log/slog"
)

const redacted = "***"

// Secret carrega uma senha sem deixá-la escapar por acidente: String, %v,
// %#v, JSON e slog mostram "***". Só Reveal devolve o valor.
type Secret struct {
	b []byte
}

// NewSecret copia s para um buffer próprio, que Wipe pode zerar.
func NewSecret(s string) Secret { return Secret{b: []byte(s)} }

// Reveal devolve o valor em claro. Use só no ponto de consumo (discagem, cofre).
func (s Secret) Reveal() string { return string(s.b) }

// IsEmpty diz se não há valor.
func (s Secret) IsEmpty() bool { return len(s.b) == 0 }

// Wipe zera o buffer. Cópias de Reveal já feitas não são alcançadas.
func (s Secret) Wipe() {
	for i := range s.b {
		s.b[i] = 0
	}
}

func (s Secret) String() string               { return redacted }
func (s Secret) GoString() string             { return redacted }
func (s Secret) Format(f fmt.State, _ rune)   { _, _ = f.Write([]byte(redacted)) }
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (s Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
func (s Secret) LogValue() slog.Value         { return slog.StringValue(redacted) }
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race ./internal/shared/ && go vet ./internal/shared/`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/shared && git commit -m "feat(shared): backoff com jitter limitado e tipo Secret que nunca vaza"
```

---

### Task 3: shared: gravação atômica e observador de arquivos por amostragem

**Files:**
- Create: `internal/shared/atomicfile.go`
- Create: `internal/shared/pollwatch.go`
- Test: `internal/shared/atomicfile_test.go`
- Test: `internal/shared/pollwatch_test.go`

**Interfaces:**
- Consumes: `shared.Clock`, `shared.NewFakeClock` (tarefa 1).
- Produces: `shared.WriteFile(path string, data []byte, perm os.FileMode) error` (temporário na mesma pasta, fsync, rename);
  `shared.NewPollWatcher(clock, interval, quiet time.Duration, probe func() string) *PollWatcher` com `Run(ctx)` e `C() <-chan struct{}`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/shared/atomicfile_test.go`:

```go
package shared

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileReplacesContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := WriteFile(p, []byte("um"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(p, []byte("dois"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "dois" {
		t.Fatalf("conteúdo = %q", got)
	}
	assertOnlyFile(t, dir, "config.json")
}

type failingFile struct {
	*os.File
	failSync bool
}

func (f failingFile) Sync() error {
	if f.failSync {
		return errors.New("disco cheio")
	}
	return f.File.Sync()
}

func TestWriteFileFailuresKeepOriginal(t *testing.T) {
	steps := map[string]func(ops *fileOps){
		"sync": func(ops *fileOps) {
			ops.createTemp = func(dir, pattern string) (tempFile, error) {
				f, err := os.CreateTemp(dir, pattern)
				return failingFile{File: f, failSync: true}, err
			}
		},
		"rename": func(ops *fileOps) {
			ops.rename = func(string, string) error { return errors.New("acesso negado") }
		},
		"createTemp": func(ops *fileOps) {
			ops.createTemp = func(string, string) (tempFile, error) { return nil, errors.New("sem espaço") }
		},
	}
	for name, breakIt := range steps {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "config.json")
			if err := os.WriteFile(p, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			ops := osOps
			breakIt(&ops)
			if err := writeFile(ops, p, []byte("novo"), 0o600); err == nil {
				t.Fatal("esperava erro")
			}
			got, _ := os.ReadFile(p)
			if string(got) != "original" {
				t.Fatalf("original alterado: %q", got)
			}
			assertOnlyFile(t, dir, "config.json")
		})
	}
}

func assertOnlyFile(t *testing.T, dir, name string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name() != name {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("pasta deveria ter só %s, tem %v", name, names)
	}
}
```

`internal/shared/pollwatch_test.go`:

```go
package shared

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestPollWatcherDebounces(t *testing.T) {
	clk := NewFakeClock(t0)
	var value atomic.Value
	value.Store("a")
	w := NewPollWatcher(clk, 250*time.Millisecond, time.Second, func() string { return value.Load().(string) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	// step avança um período e espera o observador processar a amostra
	// (ele rearma o temporizador ao terminar), para o teste não correr com ele.
	armed := func() {
		t.Helper()
		if !clk.WaitForTimers(1, time.Second) {
			t.Fatal("observador não armou o temporizador")
		}
	}
	step := func() {
		t.Helper()
		clk.Advance(250 * time.Millisecond)
		armed()
	}
	armed()
	expectNone := func() {
		t.Helper()
		select {
		case <-w.C():
			t.Fatal("aviso antes da hora")
		case <-time.After(20 * time.Millisecond):
		}
	}

	step() // sem mudança
	expectNone()
	value.Store("b")
	step() // mudança vista
	value.Store("c")
	step() // nova mudança reinicia a espera
	for i := 0; i < 3; i++ {
		step()
		expectNone()
	}
	step() // 1s estável
	select {
	case <-w.C():
	case <-time.After(time.Second):
		t.Fatal("aviso não chegou")
	}
	step()
	expectNone()
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/shared/`  
Expected: FAIL — `undefined: WriteFile`, `undefined: NewPollWatcher`.

- [ ] **Step 3: Implementar**

Decisão: observar `config.json` e `credentials\` por **amostragem** (hash/listagem a cada 250 ms–1 s) em vez de
`ReadDirectoryChangesW`. Roda igual no Linux, não tem buffer que transborda e cobre o "recarrega após 1 s sem novas alterações" da §5.2.

`internal/shared/atomicfile.go`:

```go
package shared

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// tempFile é o que WriteFile precisa de um arquivo temporário.
type tempFile interface {
	io.Writer
	Sync() error
	Close() error
	Name() string
}

// fileOps isola as chamadas de sistema para os testes simularem falhas.
type fileOps struct {
	createTemp func(dir, pattern string) (tempFile, error)
	chmod      func(name string, mode os.FileMode) error
	rename     func(from, to string) error
	remove     func(name string) error
}

var osOps = fileOps{
	createTemp: func(dir, pattern string) (tempFile, error) { return os.CreateTemp(dir, pattern) },
	chmod:      os.Chmod,
	rename:     os.Rename,
	remove:     os.Remove,
}

// WriteFile grava data em path de forma atômica: escreve num temporário na
// mesma pasta, faz fsync, fecha e renomeia por cima. Em qualquer falha o
// arquivo original fica intacto e o temporário é apagado.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	return writeFile(osOps, path, data, perm)
}

func writeFile(ops fileOps, path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	f, err := ops.createTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("criando temporário para %s: %w", path, err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = ops.remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("gravando %s: %w", tmp, err)
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync de %s: %w", tmp, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("fechando %s: %w", tmp, err)
	}
	if err = ops.chmod(tmp, perm); err != nil {
		return fmt.Errorf("permissões de %s: %w", tmp, err)
	}
	if err = ops.rename(tmp, path); err != nil {
		return fmt.Errorf("renomeando %s → %s: %w", tmp, path, err)
	}
	return nil
}
```

`internal/shared/pollwatch.go`:

```go
package shared

import (
	"context"
	"time"
)

// PollWatcher observa algo por amostragem: a cada Interval chama Probe, que
// devolve uma impressão digital (hash do conteúdo, listagem de pasta...).
// Quando a impressão muda e fica estável por Quiet, envia um aviso em C.
// Amostragem evita depender de ReadDirectoryChangesW e roda igual no Linux.
type PollWatcher struct {
	Clock    Clock
	Interval time.Duration
	Quiet    time.Duration
	Probe    func() string

	c chan struct{}
}

// NewPollWatcher prepara o observador; chame Run numa goroutine.
func NewPollWatcher(clock Clock, interval, quiet time.Duration, probe func() string) *PollWatcher {
	return &PollWatcher{Clock: clock, Interval: interval, Quiet: quiet, Probe: probe, c: make(chan struct{}, 1)}
}

// C entrega um aviso por rajada de mudanças (avisos pendentes se agregam).
func (w *PollWatcher) C() <-chan struct{} { return w.c }

// Run amostra até ctx terminar. A primeira amostra é a linha de base.
func (w *PollWatcher) Run(ctx context.Context) {
	last := w.Probe()
	var changedAt time.Time
	pending := false
	t := w.Clock.NewTimer(w.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
		}
		now := w.Clock.Now()
		if fp := w.Probe(); fp != last {
			last, changedAt, pending = fp, now, true
		} else if pending && now.Sub(changedAt) >= w.Quiet {
			pending = false
			select {
			case w.c <- struct{}{}:
			default:
			}
		}
		t.Reset(w.Interval)
	}
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race -count=20 ./internal/shared/`  
Expected: PASS (repetir pega corrida no teste do observador)

- [ ] **Step 5: Commit**

```bash
git add internal/shared && git commit -m "feat(shared): gravação atômica com falha simulada e observador por amostragem"
```

---

### Task 4: core/config: tipos, padrões, leitura estrita, validação e gravação

**Files:**
- Create: `internal/core/config/config.go`
- Create: `internal/core/config/validate.go`
- Test: `internal/core/config/config_test.go`
- Test: `internal/core/config/helpers_test.go`

**Interfaces:**
- Consumes: `shared.WriteFile` (tarefa 3).
- Produces: `config.Config{Version, Notifications, LogLevel, Log LogConfig, VPNs []VPN}`; `config.VPN` (comparável com `==`) e `config.Check{Kind, Host, Port, TimeoutSeconds}`;
  `config.CheckKind` (`CheckPing`, `CheckTCP`, `CheckLink`); `config.RawVPN`/`RawCheck` (campos opcionais como ponteiro) com `Normalize() VPN`;
  `config.Empty()`, `Parse([]byte) (Config, error)`, `DecodeStrict(data, out) error`, `Marshal(Config)`, `Load(path)`,
  `Save(path, Config) ([]byte, error)` (valida antes; devolve os bytes gravados), `Validate(Config) error` (`*ValidationError{Problems []FieldError{Field, Message}}`),
  `ValidateVPN(VPN) []FieldError` (campos sem prefixo), `NameKey(name) string`; constantes `Default*` da §5.2.

- [ ] **Step 1: Escrever o teste que falha**

`internal/core/config/config_test.go`:

```go
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minimal = `{"version":2,"vpns":[{"name":"Matriz","rasEntry":"VPN Matriz","check":{"kind":"ping","host":"10.254.1.172"}}]}`

func TestParseAppliesDefaults(t *testing.T) {
	c, err := Parse([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	want := VPN{
		Name: "Matriz", RasEntry: "VPN Matriz", Enabled: true,
		Check:           Check{Kind: CheckPing, Host: "10.254.1.172", TimeoutSeconds: 5},
		IntervalSeconds: 30, FailuresBeforeReconnect: 3, GraceAfterConnectSeconds: 15,
		ConnectTimeoutSeconds: 60, MaxBackoffSeconds: 300,
	}
	if len(c.VPNs) != 1 || c.VPNs[0] != want {
		t.Fatalf("VPN = %+v\nquer %+v", c.VPNs, want)
	}
	if !c.Notifications || c.LogLevel != "info" || c.Log != (LogConfig{1, 5}) {
		t.Fatalf("globais = %+v", c)
	}
}

func TestParseAcceptsBOMAndSpecExample(t *testing.T) {
	spec := `{
  "version": 2, "notifications": true, "logLevel": "info",
  "log": { "maxSizeMB": 1, "maxFiles": 5 },
  "vpns": [{ "name": "Matriz", "rasEntry": "VPN Matriz", "enabled": true,
    "check": { "kind": "ping", "host": "10.254.1.172", "timeoutSeconds": 5 },
    "intervalSeconds": 30, "failuresBeforeReconnect": 3, "graceAfterConnectSeconds": 15,
    "connectTimeoutSeconds": 60, "maxBackoffSeconds": 300 }]}`
	if _, err := Parse(append([]byte("\xEF\xBB\xBF"), spec...)); err != nil {
		t.Fatal(err)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"campo desconhecido":   `{"version":2,"vpns":[],"extra":1}`,
		"campo desconhecido 2": `{"version":2,"vpns":[{"name":"a","rasEntry":"a","check":{"kind":"link"},"connector":"x"}]}`,
		"lixo depois":          `{"version":2,"vpns":[]} {}`,
		"sem versão":           `{"vpns":[]}`,
		"versão 1":             `{"version":1,"vpns":[]}`,
		"malformado":           `{"version":2,`,
	}
	for name, in := range cases {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: esperava erro", name)
		}
	}
}

func vpn(mut func(*VPN)) Config {
	c := Empty()
	v := RawVPN{Name: "Matriz", RasEntry: "VPN Matriz", Check: &RawCheck{Kind: CheckPing, Host: "10.0.0.1"}}.Normalize()
	mut(&v)
	c.VPNs = []VPN{v}
	return c
}

func TestValidateFieldErrors(t *testing.T) {
	cases := []struct {
		field string
		mut   func(*VPN)
	}{
		{"vpns[0].name", func(v *VPN) { v.Name = "" }},
		{"vpns[0].name", func(v *VPN) { v.Name = strings.Repeat("é", 65) }},
		{"vpns[0].name", func(v *VPN) { v.Name = " Matriz" }},
		{"vpns[0].name", func(v *VPN) { v.Name = "a\nb" }},
		{"vpns[0].rasEntry", func(v *VPN) { v.RasEntry = "" }},
		{"vpns[0].check.kind", func(v *VPN) { v.Check.Kind = "icmp" }},
		{"vpns[0].check.host", func(v *VPN) { v.Check.Host = "" }},
		{"vpns[0].check.host", func(v *VPN) { v.Check.Host = "fe80::1" }},
		{"vpns[0].check.host", func(v *VPN) { v.Check.Host = "999.1.1.1" }},
		{"vpns[0].check.host", func(v *VPN) { v.Check.Host = "-x.example" }},
		{"vpns[0].check.port", func(v *VPN) { v.Check.Kind = CheckTCP }},
		{"vpns[0].check.port", func(v *VPN) { v.Check.Kind = CheckTCP; v.Check.Port = 70000 }},
		{"vpns[0].check.port", func(v *VPN) { v.Check.Port = 443 }},
		{"vpns[0].check.timeoutSeconds", func(v *VPN) { v.Check.TimeoutSeconds = 31 }},
		{"vpns[0].intervalSeconds", func(v *VPN) { v.IntervalSeconds = 4 }},
		{"vpns[0].failuresBeforeReconnect", func(v *VPN) { v.FailuresBeforeReconnect = 21 }},
		{"vpns[0].graceAfterConnectSeconds", func(v *VPN) { v.GraceAfterConnectSeconds = -1 }},
		{"vpns[0].connectTimeoutSeconds", func(v *VPN) { v.ConnectTimeoutSeconds = 9 }},
		{"vpns[0].maxBackoffSeconds", func(v *VPN) { v.IntervalSeconds = 600; v.MaxBackoffSeconds = 300 }},
		{"vpns[0].maxBackoffSeconds", func(v *VPN) { v.MaxBackoffSeconds = 3601 }},
	}
	for _, tc := range cases {
		err := Validate(vpn(tc.mut))
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: esperava ValidationError, veio %v", tc.field, err)
			continue
		}
		if ve.Problems[0].Field != tc.field {
			t.Errorf("campo = %q, quer %q (%v)", ve.Problems[0].Field, tc.field, ve)
		}
	}
	if err := Validate(vpn(func(v *VPN) { v.Check = Check{Kind: CheckLink, TimeoutSeconds: 5} })); err != nil {
		t.Errorf("link sem host deve valer: %v", err)
	}
	if err := Validate(vpn(func(v *VPN) { v.Check.Host = "vpn.empresa.local" })); err != nil {
		t.Errorf("nome DNS deve valer: %v", err)
	}
}

func TestValidateDuplicateNamesIgnoreCase(t *testing.T) {
	c := vpn(func(*VPN) {})
	dup := c.VPNs[0]
	dup.Name = "MATRIZ"
	c.VPNs = append(c.VPNs, dup)
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "vpns[1].name") {
		t.Fatalf("esperava nome repetido em vpns[1], veio %v", err)
	}
}

func TestSaveRoundTripAndRefusesInvalid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	c := vpn(func(*VPN) {})
	if _, err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.VPNs[0] != c.VPNs[0] {
		t.Fatalf("ida e volta: %+v", got.VPNs[0])
	}
	bad := vpn(func(v *VPN) { v.IntervalSeconds = 1 })
	if _, err := Save(p, bad); err == nil {
		t.Fatal("config inválida não pode ser gravada")
	}
	after, _ := os.ReadFile(p)
	if !strings.Contains(string(after), `"intervalSeconds": 30`) {
		t.Fatal("arquivo foi alterado por config inválida")
	}
}

var t0 = mustTime("2026-10-07T12:00:00Z")
```

`internal/core/config/helpers_test.go`:

```go
package config

import "time"

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/core/config/`  
Expected: FAIL — `undefined: Parse`, `undefined: Empty`…

- [ ] **Step 3: Implementar**

`FieldError.Field` usa caminho estilo JSON (`vpns[0].check.port`); a bandeja (Marco B) e a CLI mostram o erro junto ao campo.
`DecodeStrict` é reaproveitado pelo `state.json` e pelo protocolo IPC.

`internal/core/config/config.go`:

```go
// Package config define a configuração v2 (config.json): tipos, padrões,
// leitura estrita, validação e gravação atômica.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// Version é a única versão de config aceita.
const Version = 2

// CheckKind é o tipo de verificação de alcance.
type CheckKind string

const (
	CheckPing CheckKind = "ping"
	CheckTCP  CheckKind = "tcp"
	CheckLink CheckKind = "link"
)

// Padrões da §5.2 do spec.
const (
	DefaultCheckTimeout    = 5
	DefaultInterval        = 30
	DefaultFailures        = 3
	DefaultGrace           = 15
	DefaultConnectTimeout  = 60
	DefaultMaxBackoff      = 300
	DefaultLogLevel        = "info"
	DefaultLogMaxSizeMB    = 1
	DefaultLogMaxFiles     = 5
	DefaultNotificationsOn = true
	DefaultVPNEnabled      = true
	DefaultCheckKind       = CheckPing
	maxNameRunes           = 64
)

// Config é a configuração já normalizada (padrões aplicados).
type Config struct {
	Version       int       `json:"version"`
	Notifications bool      `json:"notifications"`
	LogLevel      string    `json:"logLevel"`
	Log           LogConfig `json:"log"`
	VPNs          []VPN     `json:"vpns"`
}

// LogConfig controla a rotação do log.
type LogConfig struct {
	MaxSizeMB int `json:"maxSizeMB"`
	MaxFiles  int `json:"maxFiles"`
}

// VPN é a config de uma VPN. É comparável com ==, o que o orquestrador usa
// para saber se o trecho mudou.
type VPN struct {
	Name                     string `json:"name"`
	RasEntry                 string `json:"rasEntry"`
	Enabled                  bool   `json:"enabled"`
	Check                    Check  `json:"check"`
	IntervalSeconds          int    `json:"intervalSeconds"`
	FailuresBeforeReconnect  int    `json:"failuresBeforeReconnect"`
	GraceAfterConnectSeconds int    `json:"graceAfterConnectSeconds"`
	ConnectTimeoutSeconds    int    `json:"connectTimeoutSeconds"`
	MaxBackoffSeconds        int    `json:"maxBackoffSeconds"`
}

// Check descreve a verificação de alcance.
type Check struct {
	Kind           CheckKind `json:"kind"`
	Host           string    `json:"host,omitempty"`
	Port           int       `json:"port,omitempty"`
	TimeoutSeconds int       `json:"timeoutSeconds"`
}

// Empty é a config válida sem VPNs, gerada quando não há seed nem arquivo.
func Empty() Config {
	return Config{
		Version:       Version,
		Notifications: DefaultNotificationsOn,
		LogLevel:      DefaultLogLevel,
		Log:           LogConfig{MaxSizeMB: DefaultLogMaxSizeMB, MaxFiles: DefaultLogMaxFiles},
		VPNs:          []VPN{},
	}
}

// Formas cruas, com ponteiros, para distinguir "omitido" de "zero".
type rawConfig struct {
	Version       *int     `json:"version"`
	Notifications *bool    `json:"notifications"`
	LogLevel      *string  `json:"logLevel"`
	Log           *rawLog  `json:"log"`
	VPNs          []RawVPN `json:"vpns"`
}

type rawLog struct {
	MaxSizeMB *int `json:"maxSizeMB"`
	MaxFiles  *int `json:"maxFiles"`
}

// RawVPN é uma VPN como chega de fora (arquivo, pipe, CLI): campos omitidos
// ficam nil e recebem o padrão em Normalize.
type RawVPN struct {
	Name                     string    `json:"name"`
	RasEntry                 string    `json:"rasEntry"`
	Enabled                  *bool     `json:"enabled,omitempty"`
	Check                    *RawCheck `json:"check,omitempty"`
	IntervalSeconds          *int      `json:"intervalSeconds,omitempty"`
	FailuresBeforeReconnect  *int      `json:"failuresBeforeReconnect,omitempty"`
	GraceAfterConnectSeconds *int      `json:"graceAfterConnectSeconds,omitempty"`
	ConnectTimeoutSeconds    *int      `json:"connectTimeoutSeconds,omitempty"`
	MaxBackoffSeconds        *int      `json:"maxBackoffSeconds,omitempty"`
}

// RawCheck é a verificação como chega de fora.
type RawCheck struct {
	Kind           CheckKind `json:"kind,omitempty"`
	Host           string    `json:"host,omitempty"`
	Port           int       `json:"port,omitempty"`
	TimeoutSeconds *int      `json:"timeoutSeconds,omitempty"`
}

func intOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// Normalize aplica os padrões da §5.2 a uma VPN crua.
func (r RawVPN) Normalize() VPN {
	v := VPN{
		Name:                     r.Name,
		RasEntry:                 r.RasEntry,
		Enabled:                  boolOr(r.Enabled, DefaultVPNEnabled),
		IntervalSeconds:          intOr(r.IntervalSeconds, DefaultInterval),
		FailuresBeforeReconnect:  intOr(r.FailuresBeforeReconnect, DefaultFailures),
		GraceAfterConnectSeconds: intOr(r.GraceAfterConnectSeconds, DefaultGrace),
		ConnectTimeoutSeconds:    intOr(r.ConnectTimeoutSeconds, DefaultConnectTimeout),
		MaxBackoffSeconds:        intOr(r.MaxBackoffSeconds, DefaultMaxBackoff),
		Check:                    Check{Kind: DefaultCheckKind, TimeoutSeconds: DefaultCheckTimeout},
	}
	if r.Check != nil {
		if r.Check.Kind != "" {
			v.Check.Kind = r.Check.Kind
		}
		v.Check.Host = r.Check.Host
		v.Check.Port = r.Check.Port
		v.Check.TimeoutSeconds = intOr(r.Check.TimeoutSeconds, DefaultCheckTimeout)
	}
	return v
}

// DecodeStrict decodifica JSON recusando campos desconhecidos e lixo após o
// valor. Aceita BOM UTF-8 no início.
func DecodeStrict(data []byte, out any) error {
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("JSON inválido: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("JSON inválido: conteúdo extra após o objeto")
	}
	return nil
}

// Parse lê um config.json: estrito, com padrões aplicados e validado.
func Parse(data []byte) (Config, error) {
	var raw rawConfig
	if err := DecodeStrict(data, &raw); err != nil {
		return Config{}, err
	}
	if raw.Version == nil {
		return Config{}, &ValidationError{Problems: []FieldError{{Field: "version", Message: "campo obrigatório (use 2)"}}}
	}
	c := Empty()
	c.Version = *raw.Version
	c.Notifications = boolOr(raw.Notifications, DefaultNotificationsOn)
	if raw.LogLevel != nil {
		c.LogLevel = *raw.LogLevel
	}
	if raw.Log != nil {
		c.Log.MaxSizeMB = intOr(raw.Log.MaxSizeMB, DefaultLogMaxSizeMB)
		c.Log.MaxFiles = intOr(raw.Log.MaxFiles, DefaultLogMaxFiles)
	}
	for _, rv := range raw.VPNs {
		c.VPNs = append(c.VPNs, rv.Normalize())
	}
	if err := Validate(c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Marshal serializa com indentação, pronto para gravar.
func Marshal(c Config) ([]byte, error) {
	if c.VPNs == nil {
		c.VPNs = []VPN{}
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Load lê e valida o arquivo.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return Parse(data)
}

// Save valida e grava de forma atômica. Config inválida nunca é gravada.
// Devolve os bytes gravados (o chamador guarda o hash para ignorar a
// própria gravação ao observar o arquivo).
func Save(path string, c Config) ([]byte, error) {
	if err := Validate(c); err != nil {
		return nil, err
	}
	data, err := Marshal(c)
	if err != nil {
		return nil, err
	}
	if err := shared.WriteFile(path, data, 0o600); err != nil {
		return nil, err
	}
	return data, nil
}
```

`internal/core/config/validate.go`:

```go
package config

import (
	"fmt"
	"net"
	"strings"
	"unicode"
	"unicode/utf8"
)

// FieldError aponta um problema num campo, com caminho estilo JSON
// ("vpns[0].check.port"), para a interface mostrar junto ao campo.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError agrupa todos os problemas encontrados.
type ValidationError struct {
	Problems []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = p.Field + ": " + p.Message
	}
	return "config inválida: " + strings.Join(parts, "; ")
}

var logLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

// Validate confere a config inteira e devolve *ValidationError ou nil.
func Validate(c Config) error {
	var probs []FieldError
	add := func(field, format string, args ...any) {
		probs = append(probs, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
	}
	if c.Version != Version {
		add("version", "versão %d não suportada (use %d)", c.Version, Version)
	}
	if !logLevels[c.LogLevel] {
		add("logLevel", "use debug, info, warn ou error")
	}
	if c.Log.MaxSizeMB < 1 || c.Log.MaxSizeMB > 100 {
		add("log.maxSizeMB", "deve estar entre 1 e 100")
	}
	if c.Log.MaxFiles < 1 || c.Log.MaxFiles > 20 {
		add("log.maxFiles", "deve estar entre 1 e 20")
	}
	seen := map[string]int{}
	for i, v := range c.VPNs {
		prefix := fmt.Sprintf("vpns[%d].", i)
		for _, p := range ValidateVPN(v) {
			add(prefix+p.Field, "%s", p.Message)
		}
		key := NameKey(v.Name)
		if j, dup := seen[key]; dup && v.Name != "" {
			add(prefix+"name", "nome repetido (igual a vpns[%d], sem diferenciar maiúsculas)", j)
		}
		seen[key] = i
	}
	if len(probs) > 0 {
		return &ValidationError{Problems: probs}
	}
	return nil
}

// NameKey é a chave de identidade de uma VPN: o nome sem diferenciar maiúsculas.
func NameKey(name string) string { return strings.ToLower(name) }

// ValidateVPN confere uma VPN isolada. Os campos vêm sem prefixo.
func ValidateVPN(v VPN) []FieldError {
	var probs []FieldError
	add := func(field, format string, args ...any) {
		probs = append(probs, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
	}
	if n := utf8.RuneCountInString(v.Name); n < 1 || n > maxNameRunes {
		add("name", "deve ter de 1 a %d caracteres", maxNameRunes)
	} else if strings.TrimSpace(v.Name) != v.Name {
		add("name", "não pode começar nem terminar com espaço")
	} else if strings.ContainsFunc(v.Name, unicode.IsControl) {
		add("name", "não pode ter caracteres de controle")
	}
	if n := utf8.RuneCountInString(v.RasEntry); n < 1 || n > 256 {
		add("rasEntry", "deve ter de 1 a 256 caracteres")
	}
	switch v.Check.Kind {
	case CheckPing, CheckTCP:
		if err := validHost(v.Check.Host); err != "" {
			add("check.host", "%s", err)
		}
	case CheckLink:
	default:
		add("check.kind", "use ping, tcp ou link")
	}
	if v.Check.Kind == CheckTCP && (v.Check.Port < 1 || v.Check.Port > 65535) {
		add("check.port", "obrigatória em tcp, entre 1 e 65535")
	}
	if v.Check.Kind != CheckTCP && v.Check.Port != 0 {
		add("check.port", "só vale para tcp")
	}
	rng := func(field string, val, lo, hi int) {
		if val < lo || val > hi {
			add(field, "deve estar entre %d e %d", lo, hi)
		}
	}
	rng("check.timeoutSeconds", v.Check.TimeoutSeconds, 1, 30)
	rng("intervalSeconds", v.IntervalSeconds, 5, 3600)
	rng("failuresBeforeReconnect", v.FailuresBeforeReconnect, 1, 20)
	rng("graceAfterConnectSeconds", v.GraceAfterConnectSeconds, 0, 300)
	rng("connectTimeoutSeconds", v.ConnectTimeoutSeconds, 10, 300)
	if v.MaxBackoffSeconds < v.IntervalSeconds || v.MaxBackoffSeconds > 3600 {
		add("maxBackoffSeconds", "deve ser ≥ intervalSeconds e ≤ 3600")
	}
	return probs
}

// validHost aceita IPv4 literal ou nome DNS; devolve a mensagem de erro ou "".
func validHost(h string) string {
	if h == "" {
		return "obrigatório em ping e tcp"
	}
	if ip := net.ParseIP(h); ip != nil {
		if ip.To4() == nil {
			return "só IPv4 é suportado"
		}
		return ""
	}
	if len(h) > 253 {
		return "nome longo demais"
	}
	if strings.Trim(h, "0123456789.") == "" {
		return "endereço IPv4 inválido"
	}
	for _, label := range strings.Split(strings.TrimSuffix(h, "."), ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "nome de host inválido"
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return "nome de host inválido"
			}
		}
	}
	return ""
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race ./internal/core/config/ && go vet ./internal/core/config/`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/config && git commit -m "feat(config): config v2 com padrões, leitura estrita, validação por campo e gravação atômica"
```

---

### Task 5: core/config: seed do instalador e state.json

**Files:**
- Create: `internal/core/config/seed.go`
- Create: `internal/core/config/seed_windows.go`
- Create: `internal/core/config/seed_other.go`
- Create: `internal/core/config/state.go`
- Test: `internal/core/config/seed_test.go`

**Interfaces:**
- Consumes: `config.Empty`, `config.Save`, `config.Load`, `config.DecodeStrict`, `config.RawVPN.Normalize`, `config.Validate` (tarefa 4); `shared.WriteFile`.
- Produces: `config.Seed{VPNEntry, VPNName, CheckKind, CheckHost, CheckPort, Interval string}`; `type SeedReader func() (Seed, bool, error)`;
  `config.FromSeed(Seed) (Config, error)`; `config.LoadOrCreate(path, SeedReader) (Config, Bootstrap, error)` com
  `Bootstrap{Created, FromSeed bool; SeedProblem error}`; `config.ReadSeedRegistry() (Seed, bool, error)` (stub fora do Windows);
  `config.State{Pauses map[string]Pause}`, `config.Pause{UntilUnix int64; Indefinite bool}`, `config.LoadState(path, now) (State, error)`
  (corrompido → `*CorruptStateError{MovedTo, Cause}` não fatal), `config.SaveState(path, State) error`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/core/config/seed_test.go`:

```go
package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFromSeed(t *testing.T) {
	c, err := FromSeed(Seed{VPNEntry: "VPN Matriz", CheckHost: "10.254.1.172", Interval: "20"})
	if err != nil {
		t.Fatal(err)
	}
	v := c.VPNs[0]
	if v.Name != "VPN Matriz" || v.Check.Kind != CheckPing || v.IntervalSeconds != 20 {
		t.Fatalf("seed gerou %+v", v)
	}

	c, _ = FromSeed(Seed{VPNEntry: "X", VPNName: "Filial"})
	if c.VPNs[0].Name != "Filial" || c.VPNs[0].Check.Kind != CheckLink {
		t.Fatalf("sem host deve virar link: %+v", c.VPNs[0])
	}

	c, _ = FromSeed(Seed{VPNEntry: "X", CheckKind: "TCP", CheckHost: "h.local", CheckPort: "443", Interval: "900"})
	if v := c.VPNs[0]; v.Check.Kind != CheckTCP || v.Check.Port != 443 || v.MaxBackoffSeconds != 900 {
		t.Fatalf("tcp: %+v", v)
	}

	if c, err := FromSeed(Seed{}); err != nil || len(c.VPNs) != 0 {
		t.Fatalf("seed vazio deve gerar config vazia: %v %v", c, err)
	}
	for _, bad := range []Seed{
		{VPNEntry: "X", CheckPort: "abc"},
		{VPNEntry: "X", Interval: "1"},
		{VPNEntry: "X", CheckKind: "tcp", CheckHost: "h"},
	} {
		if _, err := FromSeed(bad); err == nil {
			t.Errorf("seed %+v deveria falhar", bad)
		}
	}
}

func TestLoadOrCreate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	seed := func() (Seed, bool, error) { return Seed{VPNEntry: "VPN Matriz", CheckHost: "10.0.0.1"}, true, nil }

	c, b, err := LoadOrCreate(p, seed)
	if err != nil || !b.Created || !b.FromSeed || len(c.VPNs) != 1 {
		t.Fatalf("primeiro início: %+v %+v %v", c, b, err)
	}
	// Segundo início: lê o arquivo, ignora o seed.
	c, b, err = LoadOrCreate(p, func() (Seed, bool, error) { t.Fatal("não deve ler seed"); return Seed{}, false, nil })
	if err != nil || b.Created || len(c.VPNs) != 1 {
		t.Fatalf("segundo início: %+v %+v %v", c, b, err)
	}

	p2 := filepath.Join(dir, "outra.json")
	c, b, err = LoadOrCreate(p2, func() (Seed, bool, error) { return Seed{VPNEntry: "X", Interval: "x"}, true, nil })
	if err != nil || b.SeedProblem == nil || len(c.VPNs) != 0 {
		t.Fatalf("seed inválido deve gerar vazia e relatar: %+v %+v %v", c, b, err)
	}
	if _, err := os.Stat(p2); err != nil {
		t.Fatal("config vazia deveria ter sido gravada")
	}

	p3 := filepath.Join(dir, "invalida.json")
	_ = os.WriteFile(p3, []byte(`{"version":7}`), 0o600)
	if _, _, err := LoadOrCreate(p3, seed); err == nil {
		t.Fatal("arquivo existente inválido deve dar erro (não sobrescrever)")
	}

}

func TestStateCorruptIsQuarantined(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	s, err := LoadState(p, t0)
	if err != nil || len(s.Pauses) != 0 {
		t.Fatalf("ausente: %v %v", s, err)
	}
	s.Pauses["matriz"] = Pause{UntilUnix: 1700000000}
	s.Pauses["filial"] = Pause{Indefinite: true}
	if err := SaveState(p, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(p, t0)
	if err != nil || got.Pauses["matriz"].UntilUnix != 1700000000 || !got.Pauses["filial"].Indefinite {
		t.Fatalf("ida e volta: %+v %v", got, err)
	}

	_ = os.WriteFile(p, []byte("{lixo"), 0o600)
	got, err = LoadState(p, t0)
	var ce *CorruptStateError
	if !errors.As(err, &ce) || len(got.Pauses) != 0 {
		t.Fatalf("corrompido: %+v %v", got, err)
	}
	if filepath.Base(ce.MovedTo) != "state.json.corrompido-20261007-120000" {
		t.Fatalf("movido para %s", ce.MovedTo)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("state.json corrompido deveria ter saído do lugar")
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/core/config/`  
Expected: FAIL — `undefined: FromSeed`, `undefined: LoadState`…

- [ ] **Step 3: Implementar**

Seed inválido (ex.: `INTERVAL=x`) não derruba o serviço: gera config vazia e devolve o problema em `Bootstrap.SeedProblem` para o
log e o Event Log. Um `config.json` existente e inválido **não** é sobrescrito.

`internal/core/config/seed.go`:

```go
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

// Seed são as propriedades que o MSI grava em HKLM\SOFTWARE\VPNMonitor\Seed
// (§5.3). Tudo chega como texto.
type Seed struct {
	VPNEntry  string // VPN_ENTRY
	VPNName   string // VPN_NAME (padrão: igual à entrada)
	CheckKind string // CHECK_KIND (padrão: ping com host, link sem host)
	CheckHost string // CHECK_HOST
	CheckPort string // CHECK_PORT
	Interval  string // INTERVAL
}

// SeedReader lê o seed; found=false quando a chave não existe.
type SeedReader func() (seed Seed, found bool, err error)

// FromSeed gera a config inicial. Sem VPN_ENTRY, gera a config vazia.
func FromSeed(s Seed) (Config, error) {
	c := Empty()
	entry := strings.TrimSpace(s.VPNEntry)
	if entry == "" {
		return c, nil
	}
	raw := RawVPN{Name: strings.TrimSpace(s.VPNName), RasEntry: entry, Check: &RawCheck{}}
	if raw.Name == "" {
		raw.Name = entry
	}
	raw.Check.Host = strings.TrimSpace(s.CheckHost)
	switch kind := strings.ToLower(strings.TrimSpace(s.CheckKind)); {
	case kind != "":
		raw.Check.Kind = CheckKind(kind)
	case raw.Check.Host == "":
		raw.Check.Kind = CheckLink
	default:
		raw.Check.Kind = CheckPing
	}
	if p := strings.TrimSpace(s.CheckPort); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Config{}, fmt.Errorf("CHECK_PORT %q não é número", p)
		}
		raw.Check.Port = n
	}
	if iv := strings.TrimSpace(s.Interval); iv != "" {
		n, err := strconv.Atoi(iv)
		if err != nil {
			return Config{}, fmt.Errorf("INTERVAL %q não é número", iv)
		}
		raw.IntervalSeconds = &n
		if n > DefaultMaxBackoff {
			raw.MaxBackoffSeconds = &n // mantém maxBackoff ≥ interval
		}
	}
	c.VPNs = []VPN{raw.Normalize()}
	if err := Validate(c); err != nil {
		return Config{}, fmt.Errorf("seed do instalador inválido: %w", err)
	}
	return c, nil
}

// Bootstrap conta de onde veio a config carregada por LoadOrCreate.
type Bootstrap struct {
	Created     bool  // o arquivo não existia e foi gravado agora
	FromSeed    bool  // gerado a partir do seed
	SeedProblem error // seed presente mas inválido (gerou config vazia)
}

// LoadOrCreate carrega path; se não existir, gera pelo seed (ou vazia) e grava.
func LoadOrCreate(path string, readSeed SeedReader) (Config, Bootstrap, error) {
	c, err := Load(path)
	if err == nil {
		return c, Bootstrap{}, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return Config{}, Bootstrap{}, err
	}
	b := Bootstrap{Created: true}
	c = Empty()
	if readSeed != nil {
		s, found, rerr := readSeed()
		switch {
		case rerr != nil:
			b.SeedProblem = rerr
		case found:
			if sc, serr := FromSeed(s); serr != nil {
				b.SeedProblem = serr
			} else {
				c, b.FromSeed = sc, len(sc.VPNs) > 0
			}
		}
	}
	if _, err := Save(path, c); err != nil {
		return Config{}, b, err
	}
	return c, b, nil
}
```

`internal/core/config/state.go`:

```go
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// State é o conteúdo de state.json: pausas por VPN (chave = NameKey).
type State struct {
	Pauses map[string]Pause `json:"pauses"`
}

// Pause é uma pausa temporária (UntilUnix) ou indefinida.
type Pause struct {
	UntilUnix  int64 `json:"untilUnix,omitempty"`
	Indefinite bool  `json:"indefinite,omitempty"`
}

// CorruptStateError avisa que state.json estava corrompido e foi isolado.
type CorruptStateError struct {
	MovedTo string
	Cause   error
}

func (e *CorruptStateError) Error() string {
	return fmt.Sprintf("state.json corrompido (%v); movido para %s", e.Cause, e.MovedTo)
}

// LoadState lê state.json. Ausente → vazio, sem erro. Corrompido → renomeia
// para state.json.corrompido-<data>, devolve vazio e *CorruptStateError
// (não fatal: o chamador registra e segue).
func LoadState(path string, now time.Time) (State, error) {
	empty := State{Pauses: map[string]Pause{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, err
	}
	var s State
	if derr := DecodeStrict(data, &s); derr != nil {
		moved := path + ".corrompido-" + now.Format("20060102-150405")
		if rerr := os.Rename(path, moved); rerr != nil {
			return empty, fmt.Errorf("isolando state.json corrompido: %w", rerr)
		}
		return empty, &CorruptStateError{MovedTo: moved, Cause: derr}
	}
	if s.Pauses == nil {
		s.Pauses = map[string]Pause{}
	}
	return s, nil
}

// SaveState grava state.json de forma atômica.
func SaveState(path string, s State) error {
	if s.Pauses == nil {
		s.Pauses = map[string]Pause{}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return shared.WriteFile(path, append(data, '\n'), 0o600)
}
```

`internal/core/config/seed_windows.go`: Lê `HKLM\SOFTWARE\VPNMonitor\Seed` (visão 64 bits). Só compila no Windows; verificado por `GOOS=windows go vet`. O e2e do MSI (Marco C) exercita a leitura real.

```go
//go:build windows

package config

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

// SeedRegistryPath é a chave que o MSI grava.
const SeedRegistryPath = `SOFTWARE\VPNMonitor\Seed`

// ReadSeedRegistry lê o seed do HKLM (visão de 64 bits).
func ReadSeedRegistry() (Seed, bool, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, SeedRegistryPath, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, registry.ErrNotExist) {
		return Seed{}, false, nil
	}
	if err != nil {
		return Seed{}, false, err
	}
	defer k.Close()
	get := func(name string) string {
		v, _, err := k.GetStringValue(name)
		if err != nil {
			return ""
		}
		return v
	}
	return Seed{
		VPNEntry:  get("VPN_ENTRY"),
		VPNName:   get("VPN_NAME"),
		CheckKind: get("CHECK_KIND"),
		CheckHost: get("CHECK_HOST"),
		CheckPort: get("CHECK_PORT"),
		Interval:  get("INTERVAL"),
	}, true, nil
}
```

`internal/core/config/seed_other.go`:

```go
//go:build !windows

package config

// ReadSeedRegistry fora do Windows não há registro: nunca há seed.
func ReadSeedRegistry() (Seed, bool, error) { return Seed{}, false, nil }
```

- [ ] **Step 4: Dependências**

```bash
go get golang.org/x/sys@v0.47.0
go mod tidy
```

- [ ] **Step 5: Rodar os testes e confirmar que passam**

Run: `go test -race ./internal/core/config/`  
Expected: PASS

Run: `go vet ./... && GOOS=windows go vet ./...`  
Expected: sem saída

Run: `make lint`  
Expected: sem erros (go.mod agora requer golang.org/x/sys v0.47.0)

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/core/config && git commit -m "feat(config): seed do instalador no registro e state.json com isolamento de arquivo corrompido"
```

---

### Task 6: core/logging: slog com rotação, redação, nível em execução, Tail e Event Log

**Files:**
- Create: `internal/core/logging/rotate.go`
- Create: `internal/core/logging/logger.go`
- Create: `internal/core/logging/tail.go`
- Create: `internal/core/logging/eventlog.go`
- Create: `internal/core/logging/eventlog_windows.go`
- Create: `internal/core/logging/eventlog_other.go`
- Test: `internal/core/logging/logging_test.go`

**Interfaces:**
- Consumes: nada além da biblioteca padrão e `x/sys` (tarefa 5).
- Produces: `logging.OpenRotating(path, maxBytes int64, maxFiles int) (*RotatingWriter, error)` com `Write`, `SetLimits(maxBytes, maxFiles)`, `Close`;
  `logging.New(w io.Writer, level *slog.LevelVar) *slog.Logger` (redige chaves `password`, `senha`, `secret`, `segredo`, `token`, `pass`);
  `logging.ParseLevel(string) (slog.Level, error)`; `logging.ForVPN(l, name) *slog.Logger` (põe `vpn=<nome>`);
  `logging.Tail(path, maxBytes int64) (string, error)`; `logging.EventSink` (`Info`, `Warning`, `Error`, `Close`), `logging.NopSink`,
  `logging.RecordingSink` (com `Snapshot() []RecordedEvent{Level, Msg}`), `logging.OpenEventSink() (EventSink, error)`, `logging.EventSourceName = "VPNMonitor"`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/core/logging/logging_test.go`:

```go
package logging

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotationKeepsMaxFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vpnmon.log")
	w, err := OpenRotating(p, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, s := range []string{"aaaaaaaa\n", "bbbbbbbb\n", "cccccccc\n", "dddddddd\n"} {
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "<ausente>"
		}
		return string(b)
	}
	if read("vpnmon.log") != "dddddddd\n" || read("vpnmon.1.log") != "cccccccc\n" || read("vpnmon.2.log") != "bbbbbbbb\n" {
		t.Fatalf("rotação errada: %q %q %q", read("vpnmon.log"), read("vpnmon.1.log"), read("vpnmon.2.log"))
	}
	if read("vpnmon.3.log") != "<ausente>" {
		t.Fatal("arquivo além de maxFiles não deveria existir")
	}
}

func TestRotationAppendsToExisting(t *testing.T) {
	p := filepath.Join(t.TempDir(), "vpnmon.log")
	_ = os.WriteFile(p, []byte("12345"), 0o600)
	w, _ := OpenRotating(p, 8, 1)
	_, _ = w.Write([]byte("678"))
	_, _ = w.Write([]byte("9"))
	w.Close()
	if b, _ := os.ReadFile(p); string(b) != "9" {
		t.Fatalf("tamanho existente ignorado: %q", b)
	}
}

func TestLoggerRedactsAndLevels(t *testing.T) {
	var buf bytes.Buffer
	lv := new(slog.LevelVar)
	l := ForVPN(New(&buf, lv), "Matriz")
	l.Info("discando", "senha", "hunter2", "Password", "x1", "user", "ana")
	l.Debug("invisível")
	lv.Set(slog.LevelDebug)
	l.Debug("visível")
	out := buf.String()
	for _, bad := range []string{"hunter2", "x1", "invisível"} {
		if strings.Contains(out, bad) {
			t.Fatalf("%q não deveria aparecer em:\n%s", bad, out)
		}
	}
	for _, good := range []string{"vpn=Matriz", "user=ana", "visível", "senha=***"} {
		if !strings.Contains(out, good) {
			t.Fatalf("%q deveria aparecer em:\n%s", good, out)
		}
	}
}

func TestParseLevel(t *testing.T) {
	if l, err := ParseLevel("WARN"); err != nil || l != slog.LevelWarn {
		t.Fatal(l, err)
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestTailStartsAtLineBoundary(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.log")
	_ = os.WriteFile(p, []byte("linha um\nlinha dois\nlinha três\n"), 0o600)
	got, err := Tail(p, 16)
	if err != nil {
		t.Fatal(err)
	}
	if got != "linha três\n" {
		t.Fatalf("Tail = %q", got)
	}
	all, _ := Tail(p, 1<<20)
	if !strings.HasPrefix(all, "linha um") {
		t.Fatalf("Tail inteiro = %q", all)
	}
}

func TestRecordingSink(t *testing.T) {
	var s RecordingSink
	var sink EventSink = &s
	sink.Error("panic recuperado")
	if ev := s.Snapshot(); len(ev) != 1 || ev[0].Level != "error" {
		t.Fatalf("%+v", ev)
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/core/logging/`  
Expected: FAIL — `undefined: OpenRotating`…

- [ ] **Step 3: Implementar**

Rotação: `vpnmon.log` → `vpnmon.1.log` … `vpnmon.<maxFiles>.log` (§5.1). `Tail` alimenta o pedido `logTail` do pipe ("Abrir log", §7).

`internal/core/logging/rotate.go`:

```go
// Package logging monta o slog do serviço: texto, rotação por tamanho,
// redação de segredos, nível ajustável em execução e Event Log.
package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// RotatingWriter grava em path e, ao passar de MaxBytes, renomeia
// vpnmon.log → vpnmon.1.log → … → vpnmon.<MaxFiles>.log (o mais velho some).
type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	maxFiles int
	f        *os.File
	size     int64
}

// OpenRotating abre (ou cria) o log em modo append.
func OpenRotating(path string, maxBytes int64, maxFiles int) (*RotatingWriter, error) {
	w := &RotatingWriter{path: path, maxBytes: maxBytes, maxFiles: maxFiles}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

// SetLimits muda os limites (recarga da config) sem reabrir.
func (w *RotatingWriter) SetLimits(maxBytes int64, maxFiles int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.maxBytes, w.maxFiles = maxBytes, maxFiles
}

func (w *RotatingWriter) open() error {
	if err := os.MkdirAll(filepath.Dir(w.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f, w.size = f, st.Size()
	return nil
}

// rotatedName devolve vpnmon.<n>.log para n ≥ 1.
func rotatedName(path string, n int) string {
	ext := filepath.Ext(path)
	return fmt.Sprintf("%s.%d%s", strings.TrimSuffix(path, ext), n, ext)
}

func (w *RotatingWriter) rotate() error {
	if err := w.f.Close(); err != nil {
		return err
	}
	_ = os.Remove(rotatedName(w.path, w.maxFiles))
	for i := w.maxFiles - 1; i >= 1; i-- {
		_ = os.Rename(rotatedName(w.path, i), rotatedName(w.path, i+1))
	}
	if err := os.Rename(w.path, rotatedName(w.path, 1)); err != nil {
		return err
	}
	return w.open()
}

// Write grava p, girando antes se p não couber no arquivo atual.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, fmt.Errorf("girando log: %w", err)
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// Close fecha o arquivo.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
```

`internal/core/logging/logger.go`:

```go
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// sensitiveKeys são chaves de atributo cujo valor nunca vai para o log,
// mesmo que alguém passe uma string em vez de shared.Secret.
var sensitiveKeys = map[string]bool{
	"password": true, "senha": true, "secret": true, "segredo": true, "token": true, "pass": true,
}

func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if sensitiveKeys[strings.ToLower(a.Key)] {
		return slog.String(a.Key, "***")
	}
	return a
}

// New cria o logger em texto sobre w, com o nível controlado por level.
func New(w io.Writer, level *slog.LevelVar) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: redactAttr}))
}

// ParseLevel converte o logLevel da config.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return slog.LevelInfo, fmt.Errorf("nível de log desconhecido %q", s)
}

// ForVPN devolve um logger que põe vpn=<nome> em toda linha.
func ForVPN(l *slog.Logger, name string) *slog.Logger { return l.With("vpn", name) }
```

`internal/core/logging/tail.go`:

```go
package logging

import (
	"bytes"
	"io"
	"os"
)

// Tail devolve no máximo maxBytes do fim do arquivo, começando numa linha
// inteira. Serve para a bandeja mostrar o log sem acesso à pasta.
func Tail(path string, maxBytes int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	start := st.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	buf := make([]byte, st.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return "", err
	}
	if start > 0 {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	return string(buf), nil
}
```

`internal/core/logging/eventlog.go`:

```go
package logging

import "sync"

// EventSourceName é a origem registrada no Event Log (§5.6).
const EventSourceName = "VPNMonitor"

// EventSink recebe os eventos que vão para o Event Log do Windows: início e
// parada do serviço, erros fatais, config inválida e panics recuperados.
type EventSink interface {
	Info(msg string)
	Warning(msg string)
	Error(msg string)
	Close() error
}

// NopSink descarta tudo (modo console, Linux).
type NopSink struct{}

func (NopSink) Info(string)    {}
func (NopSink) Warning(string) {}
func (NopSink) Error(string)   {}
func (NopSink) Close() error   { return nil }

// RecordingSink guarda os eventos em memória; para testes.
type RecordingSink struct {
	mu     sync.Mutex
	Events []RecordedEvent
}

// RecordedEvent é um evento gravado por RecordingSink.
type RecordedEvent struct {
	Level string
	Msg   string
}

func (r *RecordingSink) add(level, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Events = append(r.Events, RecordedEvent{level, msg})
}

func (r *RecordingSink) Info(m string)    { r.add("info", m) }
func (r *RecordingSink) Warning(m string) { r.add("warning", m) }
func (r *RecordingSink) Error(m string)   { r.add("error", m) }
func (r *RecordingSink) Close() error     { return nil }

// Snapshot devolve uma cópia dos eventos.
func (r *RecordingSink) Snapshot() []RecordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]RecordedEvent(nil), r.Events...)
}
```

`internal/core/logging/eventlog_windows.go`: Usa `golang.org/x/sys/windows/svc/eventlog`. Só compila no Windows (verificado por `GOOS=windows go vet`); a origem `VPNMonitor` é registrada por `vpnmon-svc install` (tarefa 11).

```go
//go:build windows

package logging

import "golang.org/x/sys/windows/svc/eventlog"

// IDs de evento: um por nível, suficiente para filtrar no Visualizador.
const (
	eventIDInfo    = 1
	eventIDWarning = 2
	eventIDError   = 3
)

type winSink struct{ l *eventlog.Log }

// OpenEventSink abre a origem VPNMonitor (registrada pelo install/MSI).
func OpenEventSink() (EventSink, error) {
	l, err := eventlog.Open(EventSourceName)
	if err != nil {
		return nil, err
	}
	return winSink{l}, nil
}

func (s winSink) Info(m string)    { _ = s.l.Info(eventIDInfo, m) }
func (s winSink) Warning(m string) { _ = s.l.Warning(eventIDWarning, m) }
func (s winSink) Error(m string)   { _ = s.l.Error(eventIDError, m) }
func (s winSink) Close() error     { return s.l.Close() }
```

`internal/core/logging/eventlog_other.go`:

```go
//go:build !windows

package logging

// OpenEventSink fora do Windows não há Event Log.
func OpenEventSink() (EventSink, error) { return NopSink{}, nil }
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race ./internal/core/logging/`  
Expected: PASS

Run: `go vet ./... && GOOS=windows go vet ./...`  
Expected: sem saída

- [ ] **Step 5: Commit**

```bash
git add internal/core/logging && git commit -m "feat(logging): slog com rotação por tamanho, redação de segredos, Tail e Event Log"
```

---

### Task 7: core/platform/ras: tabela de erros, layout pshpack4 e catálogo rasphone.pbk

**Files:**
- Create: `internal/core/platform/ras/codes.go`
- Create: `internal/core/platform/ras/layout.go`
- Create: `internal/core/platform/ras/pbk.go`
- Test: `internal/core/platform/ras/codes_test.go`
- Test: `internal/core/platform/ras/layout_test.go`
- Test: `internal/core/platform/ras/pbk_test.go`

**Interfaces:**
- Consumes: nada.
- Produces: Constantes `ras.ERROR_*` (raserror.h/winerror.h) e `ras.Class` (`ClassTransitorio`, `ClassCredencial`, `ClassConfiguracao`, `ClassJaDiscando`, com `String()`);
  `ras.Classify(code uint32) Class`; `ras.Error{Op string; Code uint32}`;
  `ras.DialParams` (`[]byte`) com `NewDialParams(size)`, `Size()`, `SetEntry/Entry`, `SetUser/User`, `SetDomain`, `SetPassword`, `PasswordFingerprint() string`, `Wipe()`, `Clone()`;
  `ras.DialParamsSizes = []uint32{2120, 2112, 2108}`, `ras.ConnStatusSizes = {608, 564}`, `ras.ConnSizes = {1388, 1372}`;
  `ras.NewConnStatusBuffer(size)`, `ras.DecodeConnStatus([]byte) ConnStatus{State, Error, DeviceType, DeviceName}`;
  `ras.NewConnArray(size, n)`, `ras.DecodeConns(b, size, count) []RasConn{Handle uintptr; Entry string}`, `ras.EncodeConnForTest(...)`;
  `ras.RASCS_Connected`, `ras.RASCS_Disconnected`; `ras.ParsePhonebook([]byte) []string`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/core/platform/ras/codes_test.go`:

```go
package ras

import "testing"

// A tabela fixa código → constante → classe. Mudar um valor aqui exige
// conferir o raserror.h e a §4.5 do spec.
func TestClassifyTable(t *testing.T) {
	cases := []struct {
		code   uint32
		const_ uint32
		class  Class
	}{
		{718, ERROR_PPP_TIMEOUT, ClassTransitorio},
		{800, ERROR_AUTOMATIC_VPN_FAILED, ClassTransitorio},
		{809, ERROR_VPN_TIMEOUT, ClassTransitorio},
		{868, ERROR_DNSNAME_NOT_RESOLVABLE, ClassTransitorio},
		{691, ERROR_AUTHENTICATION_FAILURE, ClassCredencial},
		{646, ERROR_RESTRICTED_LOGON_HOURS, ClassCredencial},
		{647, ERROR_ACCT_DISABLED, ClassCredencial},
		{648, ERROR_PASSWD_EXPIRED, ClassCredencial},
		{649, ERROR_NO_DIALIN_PERMISSION, ClassCredencial},
		{812, ERROR_AUTH_PROTOCOL_RESTRICTED, ClassCredencial},
		{623, ERROR_CANNOT_FIND_PHONEBOOK_ENTRY, ClassConfiguracao},
		{703, ERROR_INTERACTIVE_MODE, ClassConfiguracao},
		{720, ERROR_PPP_NO_PROTOCOLS_CONFIGURED, ClassConfiguracao},
		{735, ERROR_PPP_REQUIRED_ADDRESS_REJECTED, ClassConfiguracao},
		{13801, ERROR_IPSEC_IKE_AUTH_FAIL, ClassConfiguracao},
		{13806, ERROR_IPSEC_IKE_NO_CERT, ClassConfiguracao},
		{13868, ERROR_IPSEC_IKE_POLICY_MATCH, ClassConfiguracao},
		{756, ERROR_DIAL_ALREADY_IN_PROGRESS, ClassJaDiscando},
		{711, ERROR_RASMAN_CANNOT_INITIALIZE, ClassTransitorio},
	}
	for _, c := range cases {
		if c.code != c.const_ {
			t.Errorf("constante de %d vale %d", c.code, c.const_)
		}
		if got := Classify(c.code); got != c.class {
			t.Errorf("Classify(%d) = %v, quer %v", c.code, got, c.class)
		}
	}
	for _, unknown := range []uint32{0, 1, 619, 651, 99999} {
		if Classify(unknown) != ClassTransitorio {
			t.Errorf("código %d não listado deve ser transitório", unknown)
		}
	}
}
```

`internal/core/platform/ras/layout_test.go`:

```go
package ras

import (
	"encoding/binary"
	"strings"
	"testing"
)

// Deslocamentos calculados à mão a partir do ras.h com pshpack4 (x64).
func TestLayoutOffsets(t *testing.T) {
	cases := []struct {
		name      string
		got, want int
	}{
		{"RASDIALPARAMSW.szEntryName", dpOffEntryName, 4},
		{"RASDIALPARAMSW.szPhoneNumber", dpOffPhoneNumber, 518},
		{"RASDIALPARAMSW.szCallbackNumber", dpOffCallbackNumber, 776},
		{"RASDIALPARAMSW.szUserName", dpOffUserName, 1034},
		{"RASDIALPARAMSW.szPassword", dpOffPassword, 1548},
		{"RASDIALPARAMSW.szDomain", dpOffDomain, 2062},
		{"RASDIALPARAMSW.dwSubEntry", dpOffSubEntry, 2096},
		{"RASDIALPARAMSW.dwCallbackId", dpOffCallbackID, 2100},
		{"RASDIALPARAMSW.dwIfIndex", dpOffIfIndex, 2108},
		{"RASDIALPARAMSW.szEncPassword", dpOffEncPassword, 2112},
		{"RASCONNSTATUSW.rasconnstate", csOffState, 4},
		{"RASCONNSTATUSW.dwError", csOffError, 8},
		{"RASCONNSTATUSW.szDeviceType", csOffDeviceType, 12},
		{"RASCONNSTATUSW.szDeviceName", csOffDeviceName, 46},
		{"RASCONNSTATUSW.szPhoneNumber", csOffPhone, 304},
		{"RASCONNSTATUSW.localEndPoint", csOffLocalEP, 564},
		{"RASCONNSTATUSW.remoteEndPoint", csOffRemoteEP, 584},
		{"RASCONNSTATUSW.rasconnsubstate", csOffSubState, 604},
		{"RASCONNW.hrasconn", cnOffHandle, 4},
		{"RASCONNW.szEntryName", cnOffEntryName, 12},
		{"RASCONNW.szDeviceType", cnOffDeviceType, 526},
		{"RASCONNW.szDeviceName", cnOffDeviceName, 560},
		{"RASCONNW.szPhonebook", cnOffPhonebook, 818},
		{"RASCONNW.dwSubEntry", cnOffSubEntry, 1340},
		{"RASCONNW.guidEntry", cnOffGUIDEntry, 1344},
		{"RASCONNW.dwFlags", cnOffFlags, 1360},
		{"RASCONNW.luid", cnOffLUID, 1364},
		{"RASCONNW.guidCorrelationId", cnOffCorrelation, 1372},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s em %d, quer %d", c.name, c.got, c.want)
		}
	}
	sizes := []struct {
		name string
		got  []uint32
		want []uint32
	}{
		{"RASDIALPARAMSW", DialParamsSizes, []uint32{2120, 2112, 2108}},
		{"RASCONNSTATUSW", ConnStatusSizes, []uint32{608, 564}},
		{"RASCONNW", ConnSizes, []uint32{1388, 1372}},
	}
	for _, s := range sizes {
		for i := range s.want {
			if s.got[i] != s.want[i] {
				t.Errorf("%s tamanhos %v, quer %v", s.name, s.got, s.want)
			}
		}
	}
}

func TestDialParamsFields(t *testing.T) {
	p := NewDialParams(2112)
	if p.Size() != 2112 || len(p) != 2112 {
		t.Fatal("dwSize errado")
	}
	if err := p.SetEntry("VPN Matriz ção"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetUser("ana"); err != nil {
		t.Fatal(err)
	}
	if p.Entry() != "VPN Matriz ção" || p.User() != "ana" {
		t.Fatalf("leitura: %q %q", p.Entry(), p.User())
	}
	if binary.LittleEndian.Uint16(p[dpOffEntryName:]) != 'V' {
		t.Fatal("entrada não está no deslocamento 4")
	}
	if binary.LittleEndian.Uint16(p[dpOffUserName:]) != 'a' {
		t.Fatal("usuário não está no deslocamento 1034")
	}
	before := p.PasswordFingerprint()
	_ = p.SetPassword("s3nha")
	if binary.LittleEndian.Uint16(p[dpOffPassword:]) != 's' {
		t.Fatal("senha não está no deslocamento 1548")
	}
	if p.PasswordFingerprint() == before {
		t.Fatal("impressão digital deveria mudar com a senha")
	}
	p.Wipe()
	if p.PasswordFingerprint() != before {
		t.Fatal("Wipe deveria zerar o campo de senha")
	}
	if err := p.SetEntry(strings.Repeat("x", 257)); err == nil {
		t.Fatal("entrada longa demais deveria falhar em vez de truncar")
	}
	if err := p.SetEntry(strings.Repeat("x", 256)); err != nil {
		t.Fatal("256 caracteres cabem")
	}
}

func TestDecodeConnStatus(t *testing.T) {
	b := NewConnStatusBuffer(608)
	binary.LittleEndian.PutUint32(b[4:], RASCS_Disconnected)
	binary.LittleEndian.PutUint32(b[8:], 691)
	_ = putUTF16(b, 12, maxDeviceType, "vpn")
	_ = putUTF16(b, 46, maxDeviceName, "WAN Miniport (IKEv2)")
	st := DecodeConnStatus(b)
	if st.State != RASCS_Disconnected || st.Error != 691 || st.DeviceType != "vpn" || st.DeviceName != "WAN Miniport (IKEv2)" {
		t.Fatalf("%+v", st)
	}
}

func TestDecodeConns(t *testing.T) {
	b := NewConnArray(1388, 2)
	EncodeConnForTest(b, 1388, 0, 0x1234, "Matriz")
	EncodeConnForTest(b, 1388, 1, 0xdeadbeefcafe, "Filial")
	got := DecodeConns(b, 1388, 2)
	if len(got) != 2 || got[0] != (RasConn{0x1234, "Matriz"}) || got[1] != (RasConn{0xdeadbeefcafe, "Filial"}) {
		t.Fatalf("%+v", got)
	}
}
```

`internal/core/platform/ras/pbk_test.go`:

```go
package ras

import (
	"reflect"
	"testing"
	"unicode/utf16"
)

const pbkANSI = "[VPN Matriz]\r\nEncoding=1\r\nType=2\r\n\r\n[ Filial ]\r\nType=2\r\n[]\r\n[VPN Matriz]\r\n"

func TestParsePhonebookANSI(t *testing.T) {
	got := ParsePhonebook([]byte(pbkANSI))
	if want := []string{"VPN Matriz", "Filial"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%v, quer %v", got, want)
	}
}

func TestParsePhonebookUTF8BOM(t *testing.T) {
	got := ParsePhonebook(append([]byte("\xEF\xBB\xBF"), "[Conexão]\n"...))
	if !reflect.DeepEqual(got, []string{"Conexão"}) {
		t.Fatalf("%v", got)
	}
}

func TestParsePhonebookCP1252(t *testing.T) {
	// "[Conexão – Matriz]" gravado em ANSI (Windows-1252), não UTF-8.
	raw := []byte("[Conex\xe3o \x96 Matriz]\r\nType=2\r\n")
	got := ParsePhonebook(raw)
	if !reflect.DeepEqual(got, []string{"Conexão – Matriz"}) {
		t.Fatalf("%q", got)
	}
}

func TestParsePhonebookUTF16(t *testing.T) {
	u := utf16.Encode([]rune("[Conexão São Paulo]\r\nType=2\r\n"))
	raw := []byte{0xFF, 0xFE}
	for _, c := range u {
		raw = append(raw, byte(c), byte(c>>8))
	}
	got := ParsePhonebook(raw)
	if !reflect.DeepEqual(got, []string{"Conexão São Paulo"}) {
		t.Fatalf("%v", got)
	}
}

func TestParsePhonebookEmpty(t *testing.T) {
	if got := ParsePhonebook(nil); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/core/platform/ras/`  
Expected: FAIL — `undefined: Classify`, `undefined: NewDialParams`…

- [ ] **Step 3: Implementar**

Os deslocamentos foram calculados à mão a partir do `ras.h` com `pshpack4` em x64; o teste os fixa um a um. Como o
`RASDIALPARAMSW` cresceu entre versões do SDK (`dwIfIndex`, `szEncPassword`), a API real tenta os tamanhos em ordem e recua em
`ERROR_INVALID_SIZE` (632) — o teste Windows da tarefa 8 confirma qual é aceito.

`ParsePhonebook` reaproveita o parser da v1 (ANSI/UTF-16LE com BOM) e acrescenta Windows-1252 quando os bytes não são UTF-8 válido
(item 1 do Review Focus): um catálogo gravado em "ANSI" num Windows em português tem `ã` como `0xE3`.

`internal/core/platform/ras/codes.go`:

```go
package ras

import "fmt"

// Tabela única de códigos RAS/Win32 e sua classificação (§4.5).
// Nomes iguais aos do raserror.h / winerror.h.
const (
	ERROR_INVALID_HANDLE                = 6
	ERROR_BUFFER_TOO_SMALL              = 603
	ERROR_CANNOT_FIND_PHONEBOOK_ENTRY   = 623
	ERROR_INVALID_SIZE                  = 632
	ERROR_RESTRICTED_LOGON_HOURS        = 646
	ERROR_ACCT_DISABLED                 = 647
	ERROR_PASSWD_EXPIRED                = 648
	ERROR_NO_DIALIN_PERMISSION          = 649
	ERROR_NO_CONNECTION                 = 668
	ERROR_AUTHENTICATION_FAILURE        = 691
	ERROR_RASMAN_CANNOT_INITIALIZE      = 711
	ERROR_INTERACTIVE_MODE              = 703
	ERROR_PPP_TIMEOUT                   = 718
	ERROR_PPP_NO_PROTOCOLS_CONFIGURED   = 720
	ERROR_PPP_REQUIRED_ADDRESS_REJECTED = 735
	ERROR_DIAL_ALREADY_IN_PROGRESS      = 756
	ERROR_AUTOMATIC_VPN_FAILED          = 800
	ERROR_VPN_TIMEOUT                   = 809
	ERROR_AUTH_PROTOCOL_RESTRICTED      = 812
	ERROR_DNSNAME_NOT_RESOLVABLE        = 868
	ERROR_IPSEC_IKE_AUTH_FAIL           = 13801
	ERROR_IPSEC_IKE_NO_CERT             = 13806
	ERROR_IPSEC_IKE_POLICY_MATCH        = 13868
)

// Class é a classe de um erro de discagem.
type Class int

const (
	// ClassTransitorio: backoff exponencial com jitter. Vale para todo código não listado.
	ClassTransitorio Class = iota
	// ClassCredencial: credencial/autorização rejeitada; para de tentar.
	ClassCredencial
	// ClassConfiguracao: entrada inexistente, exige interação, protocolo, certificado.
	ClassConfiguracao
	// ClassJaDiscando: outro processo está discando a mesma entrada.
	ClassJaDiscando
)

func (c Class) String() string {
	switch c {
	case ClassCredencial:
		return "credencial"
	case ClassConfiguracao:
		return "configuracao"
	case ClassJaDiscando:
		return "ja_discando"
	}
	return "transitorio"
}

var classes = map[uint32]Class{
	ERROR_AUTHENTICATION_FAILURE:        ClassCredencial,
	ERROR_RESTRICTED_LOGON_HOURS:        ClassCredencial,
	ERROR_ACCT_DISABLED:                 ClassCredencial,
	ERROR_PASSWD_EXPIRED:                ClassCredencial,
	ERROR_NO_DIALIN_PERMISSION:          ClassCredencial,
	ERROR_AUTH_PROTOCOL_RESTRICTED:      ClassCredencial,
	ERROR_CANNOT_FIND_PHONEBOOK_ENTRY:   ClassConfiguracao,
	ERROR_INTERACTIVE_MODE:              ClassConfiguracao,
	ERROR_PPP_NO_PROTOCOLS_CONFIGURED:   ClassConfiguracao,
	ERROR_PPP_REQUIRED_ADDRESS_REJECTED: ClassConfiguracao,
	ERROR_IPSEC_IKE_AUTH_FAIL:           ClassConfiguracao,
	ERROR_IPSEC_IKE_NO_CERT:             ClassConfiguracao,
	ERROR_IPSEC_IKE_POLICY_MATCH:        ClassConfiguracao,
	ERROR_DIAL_ALREADY_IN_PROGRESS:      ClassJaDiscando,
}

// Classify devolve a classe do código; desconhecidos são transitórios.
func Classify(code uint32) Class {
	if c, ok := classes[code]; ok {
		return c
	}
	return ClassTransitorio
}

// Error é um código devolvido por uma função RAS.
type Error struct {
	Op   string // função ou etapa (ex.: "RasDialW")
	Code uint32
}

func (e *Error) Error() string { return fmt.Sprintf("%s: erro RAS %d", e.Op, e.Code) }
```

`internal/core/platform/ras/layout.go`:

```go
package ras

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"unicode/utf16"
)

// Layout das estruturas RAS em x64 com empacotamento de 4 bytes (pshpack4
// do ras.h). Montamos byte a byte em vez de usar structs Go porque o Go
// alinharia ULONG_PTR/HRASCONN em 8 e deslocaria os campos seguintes.
// Os testes de layout fixam cada deslocamento.

// Tamanhos de cadeia (em WCHARs, sem o terminador) do ras.h / lmcons.h.
const (
	maxEntryName      = 256 // RAS_MaxEntryName
	maxPhoneNumber    = 128 // RAS_MaxPhoneNumber
	maxCallbackNumber = 128 // RAS_MaxCallbackNumber
	maxUserName       = 256 // UNLEN
	maxPassword       = 256 // PWLEN
	maxDomain         = 15  // DNLEN
	maxDeviceType     = 16  // RAS_MaxDeviceType
	maxDeviceName     = 128 // RAS_MaxDeviceName
	maxPath           = 260 // MAX_PATH
)

// RASDIALPARAMSW.
const (
	dpOffSize           = 0
	dpOffEntryName      = 4
	dpOffPhoneNumber    = dpOffEntryName + (maxEntryName+1)*2           // 518
	dpOffCallbackNumber = dpOffPhoneNumber + (maxPhoneNumber+1)*2       // 776
	dpOffUserName       = dpOffCallbackNumber + (maxCallbackNumber+1)*2 // 1034
	dpOffPassword       = dpOffUserName + (maxUserName+1)*2             // 1548
	dpOffDomain         = dpOffPassword + (maxPassword+1)*2             // 2062
	dpOffSubEntry       = 2096                                          // 2094 alinhado a 4
	dpOffCallbackID     = dpOffSubEntry + 4                             // 2100 (ULONG_PTR, 8 bytes)
	dpOffIfIndex        = dpOffCallbackID + 8                           // 2108 (WINVER ≥ 0x601)
	dpOffEncPassword    = dpOffIfIndex + 4                              // 2112 (LPWSTR, SDKs novos)
)

// Tamanhos aceitos de RASDIALPARAMSW, do mais novo ao mais antigo. A API
// devolve ERROR_INVALID_SIZE (632) quando não reconhece; tentamos o próximo.
var DialParamsSizes = []uint32{dpOffEncPassword + 8, dpOffEncPassword, dpOffIfIndex}

// RASCONNSTATUSW.
const (
	csOffSize       = 0
	csOffState      = 4
	csOffError      = 8
	csOffDeviceType = 12
	csOffDeviceName = csOffDeviceType + (maxDeviceType+1)*2 // 46
	csOffPhone      = csOffDeviceName + (maxDeviceName+1)*2 // 304
	csOffLocalEP    = 564                                   // 562 alinhado a 4
	csOffRemoteEP   = csOffLocalEP + 20                     // RASTUNNELENDPOINT = 20 bytes
	csOffSubState   = csOffRemoteEP + 20                    // 604
)

// ConnStatusSizes: Win7+ (com endpoints e subestado) e o formato antigo.
var ConnStatusSizes = []uint32{csOffSubState + 4, csOffLocalEP}

// RASCONNW.
const (
	cnOffSize        = 0
	cnOffHandle      = 4 // HRASCONN, 8 bytes, alinhado a 4
	cnOffEntryName   = 12
	cnOffDeviceType  = cnOffEntryName + (maxEntryName+1)*2   // 526
	cnOffDeviceName  = cnOffDeviceType + (maxDeviceType+1)*2 // 560
	cnOffPhonebook   = cnOffDeviceName + (maxDeviceName+1)*2 // 818
	cnOffSubEntry    = 1340                                  // 1338 alinhado a 4
	cnOffGUIDEntry   = cnOffSubEntry + 4                     // 1344
	cnOffFlags       = cnOffGUIDEntry + 16                   // 1360
	cnOffLUID        = cnOffFlags + 4                        // 1364
	cnOffCorrelation = cnOffLUID + 8                         // 1372 (WINVER ≥ 0x601)
)

// ConnSizes: Win7+ e Vista.
var ConnSizes = []uint32{cnOffCorrelation + 16, cnOffCorrelation}

// RASCONNSTATE relevantes.
const (
	RASCS_PAUSED       = 0x1000
	RASCS_DONE         = 0x2000
	RASCS_Connected    = RASCS_DONE
	RASCS_Disconnected = RASCS_DONE + 1
)

func putUTF16(buf []byte, off, maxChars int, s string) error {
	u := utf16.Encode([]rune(s))
	if len(u) > maxChars {
		return fmt.Errorf("texto com %d unidades UTF-16 excede o limite de %d", len(u), maxChars)
	}
	field := buf[off : off+(maxChars+1)*2]
	clear(field)
	for i, c := range u {
		binary.LittleEndian.PutUint16(field[i*2:], c)
	}
	return nil
}

func getUTF16(buf []byte, off, maxChars int) string {
	u := make([]uint16, 0, maxChars)
	for i := 0; i <= maxChars; i++ {
		c := binary.LittleEndian.Uint16(buf[off+i*2:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}

// DialParams é um RASDIALPARAMSW montado em bytes.
type DialParams []byte

// NewDialParams cria a estrutura zerada com dwSize = size.
func NewDialParams(size uint32) DialParams {
	p := make(DialParams, size)
	binary.LittleEndian.PutUint32(p[dpOffSize:], size)
	return p
}

func (p DialParams) Size() uint32               { return binary.LittleEndian.Uint32(p[dpOffSize:]) }
func (p DialParams) SetEntry(s string) error    { return putUTF16(p, dpOffEntryName, maxEntryName, s) }
func (p DialParams) Entry() string              { return getUTF16(p, dpOffEntryName, maxEntryName) }
func (p DialParams) SetUser(s string) error     { return putUTF16(p, dpOffUserName, maxUserName, s) }
func (p DialParams) User() string               { return getUTF16(p, dpOffUserName, maxUserName) }
func (p DialParams) SetDomain(s string) error   { return putUTF16(p, dpOffDomain, maxDomain, s) }
func (p DialParams) SetPassword(s string) error { return putUTF16(p, dpOffPassword, maxPassword, s) }

// PasswordFingerprint é o hash do campo de senha como está (para a
// credencial salva no Windows, é o hash do marcador). Nunca vai para log.
func (p DialParams) PasswordFingerprint() string {
	sum := sha256.Sum256(p[dpOffPassword : dpOffPassword+(maxPassword+1)*2])
	return hex.EncodeToString(sum[:])
}

// Wipe zera o campo de senha.
func (p DialParams) Wipe() { clear(p[dpOffPassword : dpOffPassword+(maxPassword+1)*2]) }

// Clone copia o buffer.
func (p DialParams) Clone() DialParams { return append(DialParams(nil), p...) }

// ConnStatus é o RASCONNSTATUSW decodificado.
type ConnStatus struct {
	State      uint32
	Error      uint32
	DeviceType string
	DeviceName string
}

// NewConnStatusBuffer cria o buffer com dwSize preenchido.
func NewConnStatusBuffer(size uint32) []byte {
	b := make([]byte, size)
	binary.LittleEndian.PutUint32(b, size)
	return b
}

// DecodeConnStatus lê o buffer preenchido por RasGetConnectStatusW.
func DecodeConnStatus(b []byte) ConnStatus {
	return ConnStatus{
		State:      binary.LittleEndian.Uint32(b[csOffState:]),
		Error:      binary.LittleEndian.Uint32(b[csOffError:]),
		DeviceType: getUTF16(b, csOffDeviceType, maxDeviceType),
		DeviceName: getUTF16(b, csOffDeviceName, maxDeviceName),
	}
}

// RasConn é um RASCONNW decodificado.
type RasConn struct {
	Handle uintptr
	Entry  string
}

// NewConnArray cria um vetor de n RASCONNW com dwSize preenchido no primeiro
// (é o que RasEnumConnections confere).
func NewConnArray(size uint32, n int) []byte {
	b := make([]byte, int(size)*n)
	binary.LittleEndian.PutUint32(b, size)
	return b
}

// DecodeConns lê count elementos de tamanho size.
func DecodeConns(b []byte, size uint32, count int) []RasConn {
	out := make([]RasConn, 0, count)
	for i := 0; i < count; i++ {
		e := b[i*int(size):]
		out = append(out, RasConn{
			Handle: uintptr(binary.LittleEndian.Uint64(e[cnOffHandle:])),
			Entry:  getUTF16(e, cnOffEntryName, maxEntryName),
		})
	}
	return out
}

// EncodeConnForTest monta um RASCONNW (usado por testes e pelo fake).
func EncodeConnForTest(b []byte, size uint32, i int, h uintptr, entry string) {
	e := b[i*int(size):]
	binary.LittleEndian.PutUint32(e, size)
	binary.LittleEndian.PutUint64(e[cnOffHandle:], uint64(h))
	_ = putUTF16(e, cnOffEntryName, maxEntryName, entry)
}
```

`internal/core/platform/ras/pbk.go`:

```go
package ras

import (
	"bufio"
	"bytes"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// ParsePhonebook extrai os nomes de entrada de um rasphone.pbk (INI: cada
// seção [Nome] é uma entrada). O Windows grava ora em ANSI/UTF-8, ora em
// UTF-16LE com BOM; os dois são aceitos. Nomes repetidos aparecem uma vez.
func ParsePhonebook(raw []byte) []string {
	text := decodePhonebook(raw)
	var names []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if len(line) < 3 || line[0] != '[' || line[len(line)-1] != ']' {
			continue
		}
		name := strings.TrimSpace(line[1 : len(line)-1])
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

func decodePhonebook(raw []byte) string {
	if !bytes.HasPrefix(raw, []byte{0xFF, 0xFE}) {
		raw = bytes.TrimPrefix(raw, []byte("\xEF\xBB\xBF"))
		if utf8.Valid(raw) {
			return string(raw)
		}
		return decodeCP1252(raw) // "ANSI" do Windows em português
	}
	body := raw[2:]
	u := make([]uint16, 0, len(body)/2)
	for i := 0; i+1 < len(body); i += 2 {
		u = append(u, uint16(body[i])|uint16(body[i+1])<<8)
	}
	return string(utf16.Decode(u))
}

// cp1252High mapeia 0x80–0x9F do Windows-1252 (o resto coincide com Latin-1).
var cp1252High = [32]rune{
	'€', 0x81, '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', 0x8D, 'Ž', 0x8F,
	0x90, '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', 0x9D, 'ž', 'Ÿ',
}

func decodeCP1252(raw []byte) string {
	var b strings.Builder
	for _, c := range raw {
		switch {
		case c >= 0x80 && c <= 0x9F:
			b.WriteRune(cp1252High[c-0x80])
		default:
			b.WriteRune(rune(c))
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race ./internal/core/platform/ras/`  
Expected: PASS

Run: `go vet ./... && GOOS=windows go vet ./...`  
Expected: sem saída

- [ ] **Step 5: Commit**

```bash
git add internal/core/platform/ras && git commit -m "feat(ras): tabela de erros classificada, layout pshpack4 testado e leitura do rasphone.pbk"
```

---

### Task 8: core/platform/ras: cliente RAS (interface, implementação Windows, stub e fake)

**Files:**
- Create: `internal/core/platform/platform.go`
- Create: `internal/core/platform/ras/client.go`
- Create: `internal/core/platform/ras/ras_windows.go`
- Create: `internal/core/platform/ras/ras_other.go`
- Create: `internal/core/platform/fake/ras.go`
- Test: `internal/core/platform/ras/client_test.go`
- Test: `internal/core/platform/fake/ras_test.go`

**Interfaces:**
- Consumes: `ras.DialParams`, `ras.DialParamsSizes`, `ras.ConnStatusSizes`, `ras.ConnSizes`, `ras.Decode*`, `ras.ParsePhonebook`, `ras.Error`, constantes (tarefa 7); `shared.Secret` (tarefa 2).
- Produces: `platform.ErrNotSupported`; `ras.Handle`; `ras.State` (`StateConnecting`, `StateConnected`, `StateDisconnected`); `ras.Status{State; Code uint32}`;
  `ras.StatusFromRaw(ConnStatus) Status`; `ras.ActiveConn{Handle; Entry}`; `ras.Saved{User string; HasPassword bool}` (marcador opaco) com
  `NewSaved(DialParams, hasPassword) *Saved` e `Fingerprint() string`; `ras.DialRequest{Entry, User string; Password shared.Secret; Saved *Saved}`;
  `ras.BuildDialParams(req, size) (DialParams, error)`; interface `ras.Client`:
  `Entries() ([]string, error)`, `Active() ([]ActiveConn, error)`, `StartDial(DialRequest) (Handle, error)`, `Status(Handle) (Status, error)`,
  `HangUp(Handle) error`, `Saved(entry) (*Saved, error)`, `WatchDisconnects(ctx) (<-chan struct{}, error)`, `ErrorText(code) string`;
  `ras.NewClient() (Client, error)`, `ras.AllUsersPhonebook()` (Windows).
  Fake: `fake.NewRAS(entries...) *fake.RAS` com `Script(entry, ...DialOutcome{Immediate bool; Code uint32; Polls int})` (`Polls: -1` = conectando para sempre),
  `SetActive(entry) ras.Handle`, `Drop(entry)`, `IsActive(entry) bool`, `SetSaved(entry, user, password)`, `Calls() []string` ("StartDial X", "HangUp X"), campo `ActiveErr`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/core/platform/ras/client_test.go`:

```go
package ras

import (
	"testing"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

func TestStatusFromRaw(t *testing.T) {
	cases := []struct {
		in   ConnStatus
		want Status
	}{
		{ConnStatus{State: RASCS_Connected}, Status{State: StateConnected}},
		{ConnStatus{State: RASCS_Disconnected, Error: 691}, Status{StateDisconnected, 691}},
		{ConnStatus{State: 3}, Status{State: StateConnecting}},
		{ConnStatus{State: 3, Error: 809}, Status{StateDisconnected, 809}},
	}
	for _, c := range cases {
		if got := StatusFromRaw(c.in); got != c.want {
			t.Errorf("%+v → %+v, quer %+v", c.in, got, c.want)
		}
	}
}

func TestBuildDialParamsFromScratch(t *testing.T) {
	p, err := BuildDialParams(DialRequest{Entry: "Matriz", User: "ana", Password: shared.NewSecret("pw")}, 2112)
	if err != nil {
		t.Fatal(err)
	}
	if p.Size() != 2112 || p.Entry() != "Matriz" || p.User() != "ana" {
		t.Fatalf("%d %q %q", p.Size(), p.Entry(), p.User())
	}
}

func TestBuildDialParamsKeepsSavedMarker(t *testing.T) {
	base := NewDialParams(2108)
	_ = base.SetUser("dominio\\ana")
	_ = base.SetPassword("<marcador-opaco>")
	saved := NewSaved(base, true)

	p, err := BuildDialParams(DialRequest{Entry: "Matriz", Saved: saved}, 2120)
	if err != nil {
		t.Fatal(err)
	}
	if p.Size() != 2108 {
		t.Fatalf("deve manter o tamanho aceito na leitura, veio %d", p.Size())
	}
	if p.User() != "dominio\\ana" || p.PasswordFingerprint() != saved.Fingerprint() {
		t.Fatal("marcador da senha salva deve passar intacto")
	}
	p.Wipe()
	if saved.Fingerprint() != base.PasswordFingerprint() {
		t.Fatal("Wipe da cópia não pode afetar o Saved")
	}
	if NewSaved(base, false).Fingerprint() != "" {
		t.Fatal("sem senha salva a impressão é vazia")
	}
}
```

`internal/core/platform/fake/ras_test.go`:

```go
package fake

import (
	"testing"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

func TestFakeRASDialScript(t *testing.T) {
	f := NewRAS("Matriz")
	f.Script("Matriz", DialOutcome{Code: 691, Polls: 2}, DialOutcome{Immediate: true, Code: 623})

	h, err := f.StartDial(ras.DialRequest{Entry: "Matriz"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if st, _ := f.Status(h); st.State != ras.StateConnecting {
			t.Fatalf("consulta %d: %+v", i, st)
		}
	}
	if st, _ := f.Status(h); st != (ras.Status{State: ras.StateDisconnected, Code: 691}) {
		t.Fatalf("final: %+v", st)
	}
	if _, err := f.StartDial(ras.DialRequest{Entry: "Matriz"}); err == nil {
		t.Fatal("esperava falha imediata")
	}
	h, _ = f.StartDial(ras.DialRequest{Entry: "Matriz"}) // sem roteiro: conecta
	f.Status(h)
	if st, _ := f.Status(h); st.State != ras.StateConnected || !f.IsActive("Matriz") {
		t.Fatalf("deveria conectar: %+v", st)
	}
	f.Drop("Matriz")
	if f.IsActive("Matriz") {
		t.Fatal("Drop não derrubou")
	}
}

func TestFakeRASSaved(t *testing.T) {
	f := NewRAS("Matriz")
	if _, err := f.Saved("Outra"); err == nil {
		t.Fatal("entrada inexistente deve dar 623")
	}
	s, _ := f.Saved("Matriz")
	if s.Fingerprint() != "" {
		t.Fatal("sem senha salva")
	}
	f.SetSaved("Matriz", "ana", "m1")
	a, _ := f.Saved("Matriz")
	f.SetSaved("Matriz", "ana", "m2")
	b, _ := f.Saved("Matriz")
	if a.Fingerprint() == "" || a.Fingerprint() == b.Fingerprint() {
		t.Fatal("impressão digital deve refletir o marcador")
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/core/platform/...`  
Expected: FAIL — `undefined: StatusFromRaw`, `undefined: NewRAS`…

- [ ] **Step 3: Implementar**

A credencial salva no Windows chega como `*ras.Saved`: o marcador da senha é repassado intacto ao `RasDialW` (o
`BuildDialParams` clona o buffer salvo) e só o hash dele (`Fingerprint`) é comparado — nunca é `Secret` nem vai para log (`String()` só mostra o usuário).

`internal/core/platform/platform.go`:

```go
// Package platform reúne as interfaces de acesso ao SO. Cada subpacote tem a
// interface, a implementação Windows (_windows.go) e um stub (!windows); os
// fakes para teste ficam em platform/fake.
package platform

import "errors"

// ErrNotSupported é devolvido pelos stubs fora do Windows.
var ErrNotSupported = errors.New("operação disponível só no Windows")
```

`internal/core/platform/ras/client.go`:

```go
package ras

import (
	"context"
	"fmt"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// Handle é um HRASCONN.
type Handle uintptr

// State resume o RASCONNSTATE no que interessa ao monitor.
type State int

const (
	StateConnecting State = iota
	StateConnected
	StateDisconnected
)

func (s State) String() string {
	return [...]string{"conectando", "conectada", "desconectada"}[s]
}

// Status é o estado de uma conexão. Code é o erro RAS quando a discagem
// terminou em falha (0 se não houve).
type Status struct {
	State State
	Code  uint32
}

// StatusFromRaw converte o RASCONNSTATUSW decodificado.
func StatusFromRaw(cs ConnStatus) Status {
	switch {
	case cs.State == RASCS_Connected:
		return Status{State: StateConnected}
	case cs.State == RASCS_Disconnected || cs.Error != 0:
		return Status{State: StateDisconnected, Code: cs.Error}
	}
	return Status{State: StateConnecting}
}

// ActiveConn é uma conexão RAS ativa.
type ActiveConn struct {
	Handle Handle
	Entry  string
}

// Saved são os parâmetros que o Windows guarda para a entrada
// (RasGetEntryDialParams). Desde o Vista, o campo de senha traz um marcador,
// não a senha: ele é repassado intacto ao RasDialW e só seu hash é comparado.
type Saved struct {
	params      DialParams
	User        string
	HasPassword bool
}

// NewSaved embrulha os parâmetros lidos (ou montados pelo fake).
func NewSaved(p DialParams, hasPassword bool) *Saved {
	return &Saved{params: p.Clone(), User: p.User(), HasPassword: hasPassword}
}

// Fingerprint é o hash do marcador; "" sem senha salva. Nunca logar.
func (s *Saved) Fingerprint() string {
	if s == nil || !s.HasPassword {
		return ""
	}
	return s.params.PasswordFingerprint()
}

// String não expõe o marcador.
func (s *Saved) String() string { return fmt.Sprintf("ras.Saved{user=%q}", s.User) }

// DialRequest descreve uma discagem. Com Saved, parte dos parâmetros salvos
// do Windows; User/Password, se informados, sobrescrevem.
type DialRequest struct {
	Entry    string
	User     string
	Password shared.Secret
	Saved    *Saved
}

// BuildDialParams monta o RASDIALPARAMSW da discagem. Com Saved, o tamanho é
// o do buffer salvo (o que a API aceitou); senão, size.
func BuildDialParams(req DialRequest, size uint32) (DialParams, error) {
	var p DialParams
	if req.Saved != nil {
		p = req.Saved.params.Clone()
	} else {
		p = NewDialParams(size)
	}
	if err := p.SetEntry(req.Entry); err != nil {
		return nil, fmt.Errorf("entrada: %w", err)
	}
	if req.User != "" {
		if err := p.SetUser(req.User); err != nil {
			return nil, fmt.Errorf("usuário: %w", err)
		}
	}
	if !req.Password.IsEmpty() {
		if err := p.SetPassword(req.Password.Reveal()); err != nil {
			p.Wipe()
			return nil, fmt.Errorf("senha: %w", err)
		}
	}
	return p, nil
}

// Client é o acesso à API RAS (rasapi32.dll).
type Client interface {
	// Entries lista as entradas do catálogo de todos os usuários.
	Entries() ([]string, error)
	// Active lista as conexões ativas (RasEnumConnections).
	Active() ([]ActiveConn, error)
	// StartDial inicia uma discagem assíncrona (RasDialW) e devolve o handle.
	// Falha imediata vem como *Error.
	StartDial(req DialRequest) (Handle, error)
	// Status consulta RasGetConnectStatus; handle inválido = desconectada.
	Status(h Handle) (Status, error)
	// HangUp desliga e espera o handle ser liberado.
	HangUp(h Handle) error
	// Saved lê os parâmetros salvos da entrada (RasGetEntryDialParams).
	Saved(entry string) (*Saved, error)
	// WatchDisconnects avisa (agregado) a cada desconexão de qualquer entrada.
	WatchDisconnects(ctx context.Context) (<-chan struct{}, error)
	// ErrorText devolve a mensagem do Windows para o código.
	ErrorText(code uint32) string
}
```

`internal/core/platform/ras/ras_windows.go`: Implementação real. **Não roda no Linux**: aqui só compila (`GOOS=windows go vet`). Pontos da §4.4 que o revisor deve conferir:
`dialCallback` é o único `windows.NewCallback`, criado na inicialização do pacote e sem fazer nada; `RasHangUp` nunca é chamado do callback;
o buffer do `RasDialW` fica no mapa `pending` (vivo) até `Status` ver o fim ou `HangUp`; `HangUp` espera o handle ser liberado
(`ERROR_INVALID_HANDLE`) por até 3 s; tudo via `NewLazySystemDLL("rasapi32.dll")`; o catálogo de todos os usuários é passado
**explicitamente** (não `NULL`) ao `RasDialW` e ao `RasGetEntryDialParamsW`.

```go
//go:build windows

package ras

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// As funções RAS não estão no x/sys: carregamos da DLL do sistema.
var (
	rasapi32                       = windows.NewLazySystemDLL("rasapi32.dll")
	procRasDialW                   = rasapi32.NewProc("RasDialW")
	procRasHangUpW                 = rasapi32.NewProc("RasHangUpW")
	procRasGetConnectStatusW       = rasapi32.NewProc("RasGetConnectStatusW")
	procRasEnumConnectionsW        = rasapi32.NewProc("RasEnumConnectionsW")
	procRasGetEntryDialParamsW     = rasapi32.NewProc("RasGetEntryDialParamsW")
	procRasConnectionNotificationW = rasapi32.NewProc("RasConnectionNotificationW")
	procRasGetErrorStringW         = rasapi32.NewProc("RasGetErrorStringW")
)

const (
	notifierRasDialFunc = 0 // dwNotifierType: RasDialFunc(UINT, RASCONNSTATE, DWORD)
	rascnDisconnection  = 2 // RASCN_Disconnection
)

// dialCallback é o ÚNICO callback passado ao RasDialW (§4.4): criado uma vez
// na inicialização do pacote (callbacks do Go nunca são liberados e têm
// limite) e não faz nada além de retornar. O andamento é acompanhado por
// RasGetConnectStatus; RasHangUp nunca é chamado daqui.
var dialCallback = windows.NewCallback(func(msg, state, code uintptr) uintptr { return 0 })

type winClient struct {
	// phonebook é passado explicitamente ao RasDialW e ao
	// RasGetEntryDialParamsW: com NULL o Windows escolheria o catálogo
	// "padrão" do contexto, que não é garantidamente o de todos os usuários.
	phonebook string
	pbPtr     *uint16
	mu        sync.Mutex
	// pending mantém vivos os buffers passados ao RasDialW até a discagem
	// terminar (conectada, falha ou hangup).
	pending map[Handle]DialParams
}

// AllUsersPhonebook é o catálogo que o LocalSystem enxerga.
func AllUsersPhonebook() (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, `Microsoft\Network\Connections\Pbk\rasphone.pbk`), nil
}

// NewClient cria o cliente RAS real.
func NewClient() (Client, error) {
	if err := rasapi32.Load(); err != nil {
		return nil, fmt.Errorf("carregando rasapi32.dll: %w", err)
	}
	pb, err := AllUsersPhonebook()
	if err != nil {
		return nil, err
	}
	ptr, err := windows.UTF16PtrFromString(pb)
	if err != nil {
		return nil, err
	}
	return &winClient{phonebook: pb, pbPtr: ptr, pending: map[Handle]DialParams{}}, nil
}

func (c *winClient) Entries() ([]string, error) {
	raw, err := os.ReadFile(c.phonebook)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParsePhonebook(raw), nil
}

func (c *winClient) Active() ([]ActiveConn, error) {
sizes:
	for _, size := range ConnSizes {
		n := 4
		for attempt := 0; attempt < 4; attempt++ {
			buf := NewConnArray(size, n)
			cb := uint32(len(buf))
			var count uint32
			r, _, _ := procRasEnumConnectionsW.Call(
				uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&cb)), uintptr(unsafe.Pointer(&count)))
			switch r {
			case 0:
				out := make([]ActiveConn, 0, count)
				for _, rc := range DecodeConns(buf, size, int(count)) {
					out = append(out, ActiveConn{Handle: Handle(rc.Handle), Entry: rc.Entry})
				}
				return out, nil
			case ERROR_BUFFER_TOO_SMALL:
				n = int(cb/size) + 1
			case ERROR_INVALID_SIZE:
				continue sizes
			default:
				return nil, &Error{Op: "RasEnumConnectionsW", Code: uint32(r)}
			}
		}
		return nil, &Error{Op: "RasEnumConnectionsW", Code: ERROR_BUFFER_TOO_SMALL}
	}
	return nil, &Error{Op: "RasEnumConnectionsW", Code: ERROR_INVALID_SIZE}
}

func (c *winClient) release(h Handle) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.pending[h]; ok {
		p.Wipe()
		delete(c.pending, h)
	}
}

func (c *winClient) rawStatus(h Handle) (Status, uint32) {
	for _, size := range ConnStatusSizes {
		buf := NewConnStatusBuffer(size)
		r, _, _ := procRasGetConnectStatusW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])))
		switch r {
		case 0:
			return StatusFromRaw(DecodeConnStatus(buf)), 0
		case ERROR_INVALID_SIZE:
			continue
		default:
			return Status{}, uint32(r)
		}
	}
	return Status{}, ERROR_INVALID_SIZE
}

func (c *winClient) Status(h Handle) (Status, error) {
	st, code := c.rawStatus(h)
	if code == ERROR_INVALID_HANDLE {
		c.release(h)
		return Status{State: StateDisconnected}, nil
	}
	if code != 0 {
		return Status{}, &Error{Op: "RasGetConnectStatusW", Code: code}
	}
	if st.State != StateConnecting {
		c.release(h)
	}
	return st, nil
}

func (c *winClient) StartDial(req DialRequest) (Handle, error) {
	sizes := DialParamsSizes
	if req.Saved != nil {
		sizes = []uint32{req.Saved.params.Size()}
	}
	var last uint32
	for _, size := range sizes {
		p, err := BuildDialParams(req, size)
		if err != nil {
			return 0, err
		}
		var h uintptr
		r, _, _ := procRasDialW.Call(0, uintptr(unsafe.Pointer(c.pbPtr)), uintptr(unsafe.Pointer(&p[0])),
			notifierRasDialFunc, dialCallback, uintptr(unsafe.Pointer(&h)))
		if r == 0 {
			c.mu.Lock()
			c.pending[Handle(h)] = p // mantém o buffer vivo até o fim
			c.mu.Unlock()
			runtime.KeepAlive(p)
			return Handle(h), nil
		}
		if h != 0 {
			_ = c.HangUp(Handle(h)) // falha imediata pode deixar handle aberto
		}
		p.Wipe()
		last = uint32(r)
		if r != ERROR_INVALID_SIZE {
			break
		}
	}
	return 0, &Error{Op: "RasDialW", Code: last}
}

func (c *winClient) HangUp(h Handle) error {
	defer c.release(h)
	r, _, _ := procRasHangUpW.Call(uintptr(h))
	if r != 0 && r != ERROR_NO_CONNECTION && r != ERROR_INVALID_HANDLE {
		return &Error{Op: "RasHangUpW", Code: uint32(r)}
	}
	// A documentação manda esperar o handle ser liberado antes de rediscar.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, code := c.rawStatus(h); code == ERROR_INVALID_HANDLE {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return &Error{Op: "RasHangUpW (espera)", Code: ERROR_INVALID_HANDLE}
}

func (c *winClient) Saved(entry string) (*Saved, error) {
	var last uint32
	for _, size := range DialParamsSizes {
		p := NewDialParams(size)
		if err := p.SetEntry(entry); err != nil {
			return nil, err
		}
		var hasPassword int32
		r, _, _ := procRasGetEntryDialParamsW.Call(uintptr(unsafe.Pointer(c.pbPtr)), uintptr(unsafe.Pointer(&p[0])), uintptr(unsafe.Pointer(&hasPassword)))
		if r == 0 {
			s := NewSaved(p, hasPassword != 0)
			p.Wipe()
			return s, nil
		}
		last = uint32(r)
		if r != ERROR_INVALID_SIZE {
			break
		}
	}
	return nil, &Error{Op: "RasGetEntryDialParamsW", Code: last}
}

func (c *winClient) WatchDisconnects(ctx context.Context) (<-chan struct{}, error) {
	ev, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return nil, err
	}
	r, _, _ := procRasConnectionNotificationW.Call(uintptr(windows.InvalidHandle), uintptr(ev), rascnDisconnection)
	if r != 0 {
		windows.CloseHandle(ev)
		return nil, &Error{Op: "RasConnectionNotificationW", Code: uint32(r)}
	}
	ch := make(chan struct{}, 1)
	go func() {
		defer windows.CloseHandle(ev)
		for ctx.Err() == nil {
			s, _ := windows.WaitForSingleObject(ev, 500)
			if s == windows.WAIT_OBJECT_0 {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
		}
	}()
	return ch, nil
}

func (c *winClient) ErrorText(code uint32) string {
	buf := make([]uint16, 512)
	r, _, _ := procRasGetErrorStringW.Call(uintptr(code), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r != 0 {
		return fmt.Sprintf("erro %d do Acesso Remoto", code)
	}
	return windows.UTF16ToString(buf)
}
```

`internal/core/platform/ras/ras_other.go`:

```go
//go:build !windows

package ras

import "github.com/guibsu/vpn-tray-monitor/internal/core/platform"

// NewClient fora do Windows: não há RAS.
func NewClient() (Client, error) { return nil, platform.ErrNotSupported }
```

`internal/core/platform/ras/ras_windows_test.go`: Roda **só no job `test-windows`**: confirma que `RasEnumConnectionsW` aceita o layout (sem 632), que entrada inexistente dá 623 e, com `VPNMON_REAL_ENTRY=<entrada>`, disca e desliga de verdade.

```go
//go:build windows

package ras

import (
	"context"
	"os"
	"testing"
	"time"
)

// Roda só no job Windows do CI: confirma que a API aceita os tamanhos de
// estrutura calculados (632 aqui significa layout errado).
func TestWindowsActiveAcceptsLayout(t *testing.T) {
	c, err := NewClient()
	if err != nil {
		t.Skipf("RAS indisponível: %v", err)
	}
	if _, err := c.Active(); err != nil {
		var re *Error
		if asError(err, &re) && re.Code == ERROR_RASMAN_CANNOT_INITIALIZE {
			t.Skip("serviço RasMan indisponível neste runner (o e2e do Marco C sonda isso explicitamente)")
		}
		t.Fatalf("RasEnumConnectionsW (632 = layout errado): %v", err)
	}
	if _, err := c.Entries(); err != nil {
		t.Fatalf("lendo o catálogo: %v", err)
	}
}

func TestWindowsSavedMissingEntryIs623(t *testing.T) {
	c, err := NewClient()
	if err != nil {
		t.Skipf("RAS indisponível: %v", err)
	}
	_, err = c.Saved("vpnmon-entrada-que-nao-existe")
	var re *Error
	if !asError(err, &re) || re.Code != ERROR_CANNOT_FIND_PHONEBOOK_ENTRY {
		t.Fatalf("esperava 623, veio %v", err)
	}
	if c.ErrorText(691) == "" {
		t.Fatal("ErrorText vazio")
	}
}

// Opcional, com VPN real: VPNMON_REAL_ENTRY=<entrada> disca e desliga.
func TestWindowsRealDialAndHangUp(t *testing.T) {
	entry := os.Getenv("VPNMON_REAL_ENTRY")
	if entry == "" {
		t.Skip("defina VPNMON_REAL_ENTRY para discar de verdade")
	}
	c, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := c.Saved(entry)
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.StartDial(DialRequest{Entry: entry, Saved: saved})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		st, err := c.Status(h)
		if err != nil {
			t.Fatal(err)
		}
		if st.State == StateConnected {
			break
		}
		if st.State == StateDisconnected {
			t.Fatalf("discagem falhou: %d %s", st.Code, c.ErrorText(st.Code))
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err := c.HangUp(h); err != nil {
		t.Fatal(err)
	}
}
```

`internal/core/platform/ras/errors_helper_test.go`:

```go
package ras

import "errors"

func asError(err error, target **Error) bool { return errors.As(err, target) }
```

`internal/core/platform/fake/ras.go`:

```go
// Package fake tem implementações em memória das interfaces de platform,
// para testar no Linux toda a lógica que depende do SO.
package fake

import (
	"context"
	"fmt"
	"sync"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

// DialOutcome roteiriza uma discagem do fake.
type DialOutcome struct {
	// Immediate faz StartDial falhar na hora com Code.
	Immediate bool
	// Code é o erro final (0 = conecta).
	Code uint32
	// Polls é quantas consultas de Status respondem "conectando" antes do
	// resultado. -1 = fica conectando para sempre (até HangUp).
	Polls int
}

type fakeDial struct {
	entry   string
	outcome DialOutcome
	polls   int
}

// RAS é um ras.Client em memória.
type RAS struct {
	mu       sync.Mutex
	entries  []string
	active   map[string]ras.Handle
	dials    map[ras.Handle]*fakeDial
	script   map[string][]DialOutcome
	saved    map[string]*ras.Saved
	calls    []string
	next     ras.Handle
	watchers []chan struct{}
	// ActiveErr, se não nil, é devolvido por Active.
	ActiveErr error
}

// NewRAS cria o fake com as entradas do catálogo.
func NewRAS(entries ...string) *RAS {
	return &RAS{
		entries: entries, active: map[string]ras.Handle{}, dials: map[ras.Handle]*fakeDial{},
		script: map[string][]DialOutcome{}, saved: map[string]*ras.Saved{}, next: 100,
	}
}

// Script enfileira resultados para as próximas discagens da entrada.
// Sem roteiro, a discagem conecta após 1 consulta.
func (f *RAS) Script(entry string, outcomes ...DialOutcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script[entry] = append(f.script[entry], outcomes...)
}

// SetActive marca a entrada como conectada (ex.: conexão já existente).
func (f *RAS) SetActive(entry string) ras.Handle {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	f.active[entry] = f.next
	return f.next
}

// Drop derruba a conexão da entrada e avisa os observadores.
func (f *RAS) Drop(entry string) {
	f.mu.Lock()
	delete(f.active, entry)
	ws := append([]chan struct{}(nil), f.watchers...)
	f.mu.Unlock()
	for _, w := range ws {
		select {
		case w <- struct{}{}:
		default:
		}
	}
}

// IsActive diz se a entrada está conectada.
func (f *RAS) IsActive(entry string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.active[entry]
	return ok
}

// SetSaved grava a "credencial salva no Windows"; password vira o marcador.
func (f *RAS) SetSaved(entry, user, password string) {
	p := ras.NewDialParams(ras.DialParamsSizes[1])
	_ = p.SetEntry(entry)
	_ = p.SetUser(user)
	_ = p.SetPassword(password)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved[entry] = ras.NewSaved(p, password != "")
}

// Calls devolve o registro de chamadas ("StartDial X", "HangUp X").
func (f *RAS) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *RAS) Entries() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.entries...), nil
}

func (f *RAS) Active() ([]ras.ActiveConn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ActiveErr != nil {
		return nil, f.ActiveErr
	}
	var out []ras.ActiveConn
	for e, h := range f.active {
		out = append(out, ras.ActiveConn{Handle: h, Entry: e})
	}
	return out, nil
}

func (f *RAS) StartDial(req ras.DialRequest) (ras.Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "StartDial "+req.Entry)
	out := DialOutcome{Polls: 1}
	if q := f.script[req.Entry]; len(q) > 0 {
		out, f.script[req.Entry] = q[0], q[1:]
	}
	if out.Immediate {
		return 0, &ras.Error{Op: "RasDialW", Code: out.Code}
	}
	f.next++
	f.dials[f.next] = &fakeDial{entry: req.Entry, outcome: out, polls: out.Polls}
	return f.next, nil
}

func (f *RAS) Status(h ras.Handle) (ras.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.dials[h]
	if !ok {
		for _, ah := range f.active {
			if ah == h {
				return ras.Status{State: ras.StateConnected}, nil
			}
		}
		return ras.Status{State: ras.StateDisconnected}, nil
	}
	if d.polls != 0 {
		if d.polls > 0 {
			d.polls--
		}
		return ras.Status{State: ras.StateConnecting}, nil
	}
	delete(f.dials, h)
	if d.outcome.Code != 0 {
		return ras.Status{State: ras.StateDisconnected, Code: d.outcome.Code}, nil
	}
	f.active[d.entry] = h
	return ras.Status{State: ras.StateConnected}, nil
}

func (f *RAS) HangUp(h ras.Handle) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := ""
	if d, ok := f.dials[h]; ok {
		entry = d.entry
		delete(f.dials, h)
	}
	for e, ah := range f.active {
		if ah == h {
			entry = e
			delete(f.active, e)
		}
	}
	f.calls = append(f.calls, "HangUp "+entry)
	return nil
}

func (f *RAS) Saved(entry string) (*ras.Saved, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	known := false
	for _, e := range f.entries {
		known = known || e == entry
	}
	if !known {
		return nil, &ras.Error{Op: "RasGetEntryDialParamsW", Code: ras.ERROR_CANNOT_FIND_PHONEBOOK_ENTRY}
	}
	if s, ok := f.saved[entry]; ok {
		return s, nil
	}
	p := ras.NewDialParams(ras.DialParamsSizes[1])
	_ = p.SetEntry(entry)
	return ras.NewSaved(p, false), nil
}

func (f *RAS) WatchDisconnects(ctx context.Context) (<-chan struct{}, error) {
	ch := make(chan struct{}, 1)
	f.mu.Lock()
	f.watchers = append(f.watchers, ch)
	f.mu.Unlock()
	return ch, nil
}

func (f *RAS) ErrorText(code uint32) string { return fmt.Sprintf("erro RAS %d (fake)", code) }

var _ ras.Client = (*RAS)(nil)
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race ./internal/core/platform/...`  
Expected: PASS

Run: `go vet ./... && GOOS=windows go vet ./...`  
Expected: sem saída

Run: `GOOS=windows go test -c -o /dev/null ./internal/core/platform/ras/`  
Expected: compila o teste Windows

- [ ] **Step 5: Commit**

```bash
git add internal/core/platform && git commit -m "feat(ras): cliente RAS assíncrono com callback único, stub e fake roteirizável"
```

---

### Task 9: core/platform: ICMP (IcmpSendEcho2) e DPAPI de máquina

**Files:**
- Create: `internal/core/platform/icmp/icmp.go`
- Create: `internal/core/platform/icmp/icmp_windows.go`
- Create: `internal/core/platform/icmp/icmp_other.go`
- Create: `internal/core/platform/dpapi/dpapi.go`
- Create: `internal/core/platform/dpapi/dpapi_windows.go`
- Create: `internal/core/platform/dpapi/dpapi_other.go`
- Create: `internal/core/platform/fake/icmp.go`
- Create: `internal/core/platform/fake/dpapi.go`
- Test: `internal/core/platform/icmp/icmp_test.go`
- Test: `internal/core/platform/fake/icmp_dpapi_test.go`

**Interfaces:**
- Consumes: `platform.ErrNotSupported` (tarefa 8).
- Produces: `icmp.Pinger` (`Ping(ctx, host string, timeout) (icmp.Result{OK bool; RTT time.Duration; Status uint32}, error)`), `icmp.New() Pinger`,
  `icmp.DecodeReply([]byte) Result`, `icmp.IPv4ToUint32(net.IP)`, `icmp.ResolveIPv4(ctx, host)`, `icmp.IP_SUCCESS`, `icmp.IP_REQ_TIMED_OUT`…;
  `dpapi.Protector` (`Protect(plain, entropy)`, `Unprotect(blob, entropy)`), `dpapi.New() Protector`;
  `fake.NewPinger() *fake.Pinger` (`SetReachable(host, ok)`, `SetError(host, err)`, `SetBlock(bool)` (espera o ctx), `SetHang(bool)` (trava ignorando o ctx, como o `IcmpSendEcho2` real), `Calls() int`, campo `RTT` = 12 ms); `fake.DPAPI{}`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/core/platform/icmp/icmp_test.go`:

```go
package icmp

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestDecodeReply(t *testing.T) {
	b := make([]byte, ReplySize)
	binary.LittleEndian.PutUint32(b[4:], IP_SUCCESS)
	binary.LittleEndian.PutUint32(b[8:], 12)
	if r := DecodeReply(b); !r.OK || r.RTT != 12*time.Millisecond {
		t.Fatalf("%+v", r)
	}
	binary.LittleEndian.PutUint32(b[4:], IP_DEST_HOST_UNREACHABLE)
	if r := DecodeReply(b); r.OK || r.Status != 11003 {
		t.Fatalf("destino inacessível não é sucesso: %+v", r)
	}
}

func TestIPv4ToUint32(t *testing.T) {
	got, err := IPv4ToUint32(net.ParseIP("10.254.1.172"))
	if err != nil || got != 0xAC01FE0A {
		t.Fatalf("%#x %v", got, err)
	}
	if _, err := IPv4ToUint32(net.ParseIP("::1")); err == nil {
		t.Fatal("IPv6 deve falhar")
	}
}

func TestResolveIPv4Literal(t *testing.T) {
	ip, err := ResolveIPv4(context.Background(), "192.0.2.1")
	if err != nil || ip.String() != "192.0.2.1" {
		t.Fatal(ip, err)
	}
	if _, err := ResolveIPv4(context.Background(), "fe80::1"); err == nil {
		t.Fatal("IPv6 literal deve falhar")
	}
}
```

`internal/core/platform/fake/icmp_dpapi_test.go`:

```go
package fake

import (
	"context"
	"testing"
	"time"
)

func TestFakePinger(t *testing.T) {
	p := NewPinger()
	if r, err := p.Ping(context.Background(), "10.0.0.1", time.Second); err != nil || r.OK {
		t.Fatal("host desconhecido não responde")
	}
	p.SetReachable("10.0.0.1", true)
	if r, _ := p.Ping(context.Background(), "10.0.0.1", time.Second); !r.OK || r.RTT == 0 {
		t.Fatal("deveria responder")
	}
	p.SetBlock(true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := p.Ping(ctx, "10.0.0.1", time.Second); err == nil {
		t.Fatal("bloqueado deve terminar com erro do ctx")
	}
	if p.Calls() != 3 {
		t.Fatal(p.Calls())
	}
	p.SetBlock(false)
	p.SetHang(true)
	done := make(chan struct{})
	go func() { p.Ping(context.Background(), "10.0.0.1", time.Second); close(done) }()
	select {
	case <-done:
		t.Fatal("SetHang deveria travar o Ping")
	case <-time.After(20 * time.Millisecond):
	}
	p.SetHang(false)
	<-done
}

func TestFakeDPAPI(t *testing.T) {
	var d DPAPI
	b, _ := d.Protect([]byte("x"), []byte("e"))
	if got, err := d.Unprotect(b, []byte("e")); err != nil || string(got) != "x" {
		t.Fatal(got, err)
	}
	if _, err := d.Unprotect(b, []byte("f")); err == nil {
		t.Fatal("entropia errada")
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/core/platform/...`  
Expected: FAIL — `undefined: DecodeReply`, `undefined: NewPinger`…

- [ ] **Step 3: Implementar**

`internal/core/platform/icmp/icmp.go`:

```go
// Package icmp faz eco ICMP pela API do Windows (IcmpSendEcho2). Só o
// status 0 conta como resposta do próprio destino: o ping.exe trata
// "destino inacessível" enviado por um roteador como sucesso.
package icmp

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// Result é o resultado de um eco. OK=false sem erro significa "sem resposta"
// (um resultado válido); erro significa que o teste nem pôde ser feito.
type Result struct {
	OK     bool
	RTT    time.Duration
	Status uint32 // IP_STATUS do Windows (0 = IP_SUCCESS)
}

// Pinger envia um eco ICMP.
type Pinger interface {
	Ping(ctx context.Context, host string, timeout time.Duration) (Result, error)
}

// Status de ICMP_ECHO_REPLY relevantes (ipexport.h).
const (
	IP_SUCCESS               = 0
	IP_DEST_NET_UNREACHABLE  = 11002
	IP_DEST_HOST_UNREACHABLE = 11003
	IP_REQ_TIMED_OUT         = 11010
)

// Layout de ICMP_ECHO_REPLY em x64 (alinhamento natural).
const (
	replyOffStatus = 4
	replyOffRTT    = 8
	ReplySize      = 40 // Address,Status,RTT(12) DataSize,Reserved(4) Data*(8) Options(16)
)

// DecodeReply lê Status e RoundTripTime do buffer de resposta.
func DecodeReply(b []byte) Result {
	st := binary.LittleEndian.Uint32(b[replyOffStatus:])
	rtt := binary.LittleEndian.Uint32(b[replyOffRTT:])
	return Result{OK: st == IP_SUCCESS, Status: st, RTT: time.Duration(rtt) * time.Millisecond}
}

// IPv4ToUint32 monta o IPAddr na ordem de bytes que a API espera.
func IPv4ToUint32(ip net.IP) (uint32, error) {
	v4 := ip.To4()
	if v4 == nil {
		return 0, fmt.Errorf("%s não é IPv4", ip)
	}
	return uint32(v4[0]) | uint32(v4[1])<<8 | uint32(v4[2])<<16 | uint32(v4[3])<<24, nil
}

// ResolveIPv4 aceita IPv4 literal ou nome (resolvido só para IPv4).
func ResolveIPv4(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() == nil {
			return nil, fmt.Errorf("%s não é IPv4", host)
		}
		return ip, nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return nil, fmt.Errorf("resolvendo %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%q não resolveu para IPv4", host)
	}
	return ips[0], nil
}
```

`internal/core/platform/icmp/icmp_windows.go`: Reaproveita a v1 trocando `IcmpSendEcho` por `IcmpSendEcho2` (síncrono, sem evento nem APC). Só `Status` e `RoundTripTime` são lidos — mesmos deslocamentos em `ICMP_ECHO_REPLY` e `ICMP_ECHO_REPLY32`.

```go
//go:build windows

package icmp

import (
	"context"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	procIcmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpSendEcho2   = iphlpapi.NewProc("IcmpSendEcho2")
	procIcmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
)

const payloadSize = 32

type winPinger struct{}

// New devolve o Pinger real.
func New() Pinger { return winPinger{} }

func (winPinger) Ping(ctx context.Context, host string, timeout time.Duration) (Result, error) {
	ip, err := ResolveIPv4(ctx, host)
	if err != nil {
		return Result{}, err
	}
	dst, _ := IPv4ToUint32(ip)
	h, _, errno := procIcmpCreateFile.Call()
	if h == 0 || h == uintptr(windows.InvalidHandle) {
		return Result{}, fmt.Errorf("IcmpCreateFile: %w", errno)
	}
	defer procIcmpCloseHandle.Call(h)

	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte('a' + i%23)
	}
	reply := make([]byte, ReplySize+payloadSize+8+64) // margem pedida pela documentação
	ms := timeout.Milliseconds()
	if ms <= 0 {
		ms = 1000
	}
	n, _, _ := procIcmpSendEcho2.Call(h, 0, 0, 0, uintptr(dst),
		uintptr(unsafe.Pointer(&payload[0])), uintptr(len(payload)), 0,
		uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), uintptr(ms))
	if n == 0 {
		return Result{Status: IP_REQ_TIMED_OUT}, nil // sem resposta: resultado válido
	}
	return DecodeReply(reply), nil
}
```

`internal/core/platform/icmp/icmp_other.go`:

```go
//go:build !windows

package icmp

import (
	"context"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform"
)

type stubPinger struct{}

// New fora do Windows devolve um Pinger que sempre falha.
func New() Pinger { return stubPinger{} }

func (stubPinger) Ping(context.Context, string, time.Duration) (Result, error) {
	return Result{}, platform.ErrNotSupported
}
```

`internal/core/platform/icmp/icmp_windows_test.go`: Só no job `test-windows`: eco real em 127.0.0.1 e TEST-NET sem resposta.

```go
//go:build windows

package icmp

import (
	"context"
	"testing"
	"time"
)

// Só no job Windows: eco real no loopback e num TEST-NET sem resposta.
func TestWindowsPingLoopback(t *testing.T) {
	r, err := New().Ping(context.Background(), "127.0.0.1", 2*time.Second)
	if err != nil || !r.OK {
		t.Fatalf("loopback: %+v %v", r, err)
	}
}

func TestWindowsPingUnreachable(t *testing.T) {
	r, err := New().Ping(context.Background(), "192.0.2.1", time.Second)
	if err != nil || r.OK {
		t.Fatalf("TEST-NET deveria ficar sem resposta: %+v %v", r, err)
	}
}
```

`internal/core/platform/dpapi/dpapi.go`:

```go
// Package dpapi protege dados com a DPAPI no escopo da máquina
// (CRYPTPROTECT_LOCAL_MACHINE). Qualquer processo local decifra o blob se
// puder lê-lo: a proteção real é a ACL da pasta (§5.4).
package dpapi

// Protector cifra e decifra com entropia adicional.
type Protector interface {
	Protect(plain, entropy []byte) ([]byte, error)
	Unprotect(blob, entropy []byte) ([]byte, error)
}
```

`internal/core/platform/dpapi/dpapi_windows.go`: `CryptProtectData` com `CRYPTPROTECT_LOCAL_MACHINE|CRYPTPROTECT_UI_FORBIDDEN` (substitui a dependência `billgraziano/dpapi` da v1, que era por usuário).

```go
//go:build windows

package dpapi

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type machine struct{}

// New devolve o Protector real (escopo de máquina).
func New() Protector { return machine{} }

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func takeOut(out *windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	res := make([]byte, out.Size)
	copy(res, unsafe.Slice(out.Data, out.Size))
	return res
}

const flags = windows.CRYPTPROTECT_LOCAL_MACHINE | windows.CRYPTPROTECT_UI_FORBIDDEN

func (machine) Protect(plain, entropy []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptProtectData(blob(plain), nil, blob(entropy), 0, nil, flags, &out); err != nil {
		return nil, err
	}
	return takeOut(&out), nil
}

func (machine) Unprotect(b, entropy []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(blob(b), nil, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeOut(&out), nil
}
```

`internal/core/platform/dpapi/dpapi_other.go`:

```go
//go:build !windows

package dpapi

import "github.com/guibsu/vpn-tray-monitor/internal/core/platform"

type stub struct{}

// New fora do Windows devolve um Protector que sempre falha.
func New() Protector { return stub{} }

func (stub) Protect([]byte, []byte) ([]byte, error)   { return nil, platform.ErrNotSupported }
func (stub) Unprotect([]byte, []byte) ([]byte, error) { return nil, platform.ErrNotSupported }
```

`internal/core/platform/dpapi/dpapi_windows_test.go`: Só no job `test-windows`: ida e volta real e entropia errada recusada.

```go
//go:build windows

package dpapi

import (
	"bytes"
	"testing"
)

// Só no job Windows: ida e volta real e entropia errada recusada.
func TestWindowsRoundTrip(t *testing.T) {
	p := New()
	blob, err := p.Protect([]byte("segredo"), []byte("entropia"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("segredo")) {
		t.Fatal("blob contém o texto em claro")
	}
	got, err := p.Unprotect(blob, []byte("entropia"))
	if err != nil || string(got) != "segredo" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := p.Unprotect(blob, []byte("outra")); err == nil {
		t.Fatal("entropia errada deveria falhar")
	}
}
```

`internal/core/platform/fake/icmp.go`:

```go
package fake

import (
	"context"
	"sync"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/icmp"
)

// Pinger é um icmp.Pinger roteirizável.
type Pinger struct {
	mu        sync.Mutex
	reachable map[string]bool
	errs      map[string]error
	block     bool
	hang      chan struct{}
	calls     int
	// RTT devolvido nos sucessos.
	RTT time.Duration
}

// NewPinger cria o fake; hosts não configurados não respondem.
func NewPinger() *Pinger {
	return &Pinger{reachable: map[string]bool{}, errs: map[string]error{}, RTT: 12 * time.Millisecond}
}

// SetReachable define se host responde.
func (p *Pinger) SetReachable(host string, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reachable[host] = ok
}

// SetError faz Ping(host) falhar com err (nil remove).
func (p *Pinger) SetError(host string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errs[host] = err
}

// SetBlock faz Ping esperar o ctx terminar (verificação travada).
func (p *Pinger) SetBlock(b bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.block = b
}

// SetHang(true) faz Ping travar ignorando o ctx, como o IcmpSendEcho2 real
// (síncrono); SetHang(false) solta as chamadas presas.
func (p *Pinger) SetHang(h bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if h && p.hang == nil {
		p.hang = make(chan struct{})
	}
	if !h && p.hang != nil {
		close(p.hang)
		p.hang = nil
	}
}

// Calls conta as chamadas.
func (p *Pinger) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *Pinger) Ping(ctx context.Context, host string, _ time.Duration) (icmp.Result, error) {
	p.mu.Lock()
	p.calls++
	block, hang, err, ok, rtt := p.block, p.hang, p.errs[host], p.reachable[host], p.RTT
	p.mu.Unlock()
	if hang != nil {
		<-hang
	}
	if block {
		<-ctx.Done()
		return icmp.Result{}, ctx.Err()
	}
	if err != nil {
		return icmp.Result{}, err
	}
	if !ok {
		return icmp.Result{Status: icmp.IP_REQ_TIMED_OUT}, nil
	}
	return icmp.Result{OK: true, RTT: rtt}, nil
}

var _ icmp.Pinger = (*Pinger)(nil)
```

`internal/core/platform/fake/dpapi.go`:

```go
package fake

import (
	"bytes"
	"errors"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/dpapi"
)

// DPAPI é um dpapi.Protector reversível e determinístico: "FAKEDPAPI" +
// entropia + dados com XOR. Recusa entropia errada, como a real.
type DPAPI struct{}

var fakeMagic = []byte("FAKEDPAPI:")

func xor(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = c ^ 0x5A
	}
	return out
}

func (DPAPI) Protect(plain, entropy []byte) ([]byte, error) {
	out := append([]byte{}, fakeMagic...)
	out = append(out, byte(len(entropy)))
	out = append(out, entropy...)
	return append(out, xor(plain)...), nil
}

func (DPAPI) Unprotect(blob, entropy []byte) ([]byte, error) {
	if !bytes.HasPrefix(blob, fakeMagic) || len(blob) < len(fakeMagic)+1 {
		return nil, errors.New("blob DPAPI inválido")
	}
	rest := blob[len(fakeMagic):]
	n := int(rest[0])
	if len(rest) < 1+n || !bytes.Equal(rest[1:1+n], entropy) {
		return nil, errors.New("entropia incorreta")
	}
	return xor(rest[1+n:]), nil
}

var _ dpapi.Protector = DPAPI{}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race ./internal/core/platform/...`  
Expected: PASS

Run: `go vet ./... && GOOS=windows go vet ./...`  
Expected: sem saída

Run: `GOOS=windows go test -c -o /dev/null ./internal/core/platform/icmp/ && GOOS=windows go test -c -o /dev/null ./internal/core/platform/dpapi/`  
Expected: compila

- [ ] **Step 5: Commit**

```bash
git add internal/core/platform && git commit -m "feat(platform): eco ICMP pelo IcmpSendEcho2 e DPAPI de máquina, com fakes"
```

---

### Task 10: core/platform: avisos de rede (netwatch) e ACL da pasta de dados

**Files:**
- Create: `internal/core/platform/netwatch/netwatch.go`
- Create: `internal/core/platform/netwatch/netwatch_windows.go`
- Create: `internal/core/platform/netwatch/netwatch_other.go`
- Create: `internal/core/platform/acl/acl.go`
- Create: `internal/core/platform/acl/acl_windows.go`
- Create: `internal/core/platform/acl/acl_other.go`
- Create: `internal/core/platform/fake/netwatch_acl.go`
- Test: `internal/core/platform/netwatch/netwatch_test.go`
- Test: `internal/core/platform/acl/acl_test.go`

**Interfaces:**
- Consumes: `shared.Clock`/`FakeClock.WaitForDeadline` (tarefa 1); `platform.ErrNotSupported`.
- Produces: `netwatch.Watcher` (`Changes() <-chan struct{}`, `HasPhysicalDefaultRoute() (bool, error)`, `Close()`), `netwatch.New()`,
  `netwatch.Route{PrefixLen uint8; IfType uint32; OperUp bool}`, `netwatch.HasPhysicalDefault([]Route) bool`, `netwatch.IsVirtualIfType(uint32) bool`,
  `netwatch.Debounce(ctx, clock, in <-chan struct{}, quiet) <-chan struct{}`, `netwatch.DefaultQuiet = 2s`;
  `acl.Securer` (`EnsureDir(path) (changed bool, err error)`), `acl.New()`, `acl.DirSDDL = "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"`, `acl.Matches(sddl) bool` (dono e DACL);
  `fake.NewNet() *fake.Net` (`SetPhysical(bool)` também emite aviso), `fake.ACL{Dirs []string}`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/core/platform/netwatch/netwatch_test.go`:

```go
package netwatch

import (
	"context"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

func TestHasPhysicalDefault(t *testing.T) {
	cases := []struct {
		name   string
		routes []Route
		want   bool
	}{
		{"sem rotas", nil, false},
		{"ethernet com padrão", []Route{{0, 6, true}}, true},
		{"wifi com padrão", []Route{{0, 71, true}}, true},
		{"só a VPN (PPP) tem padrão", []Route{{0, 23, true}, {24, 6, true}}, false},
		{"túnel IKEv2", []Route{{0, 131, true}}, false},
		{"ethernet caída", []Route{{0, 6, false}}, false},
		{"ethernet sem padrão", []Route{{24, 6, true}}, false},
	}
	for _, c := range cases {
		if got := HasPhysicalDefault(c.routes); got != c.want {
			t.Errorf("%s: %v, quer %v", c.name, got, c.want)
		}
	}
}

func TestDebounce(t *testing.T) {
	clk := shared.NewFakeClock(time.Unix(0, 0))
	in := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := Debounce(ctx, clk, in, 2*time.Second)

	in <- struct{}{}
	if !clk.WaitForDeadline(2*time.Second, time.Second) {
		t.Fatal("espera de 2s não foi armada")
	}
	clk.Advance(1500 * time.Millisecond)
	in <- struct{}{} // rajada: reinicia a espera
	if !clk.WaitForDeadline(2*time.Second, time.Second) {
		t.Fatal("espera não foi reiniciada")
	}
	clk.Advance(1500 * time.Millisecond)
	select {
	case <-out:
		t.Fatal("aviso antes de 2s de silêncio")
	case <-time.After(20 * time.Millisecond):
	}
	clk.Advance(600 * time.Millisecond)
	select {
	case <-out:
	case <-time.After(time.Second):
		t.Fatal("aviso não chegou")
	}
}
```

`internal/core/platform/acl/acl_test.go`:

```go
package acl

import "testing"

func TestMatches(t *testing.T) {
	cases := map[string]bool{
		DirSDDL: true,
		"O:BAG:SYD:PAI(A;OICI;FA;;;BA)(A;OICI;FA;;;SY)":                 true,
		"O:BAD:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)S:AI":                 true,
		"D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)":                           false, // sem dono
		"O:S-1-5-21-1-2-3-1001D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)":      false, // usuário que pré-criou é dono
		"O:BAD:AI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)":                      false, // herança ligada
		"O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)": false, // usuários lendo
		"O:BAD:P(A;OICI;FA;;;SY)":                                       false,
		"O:BA":                                                          false,
		"O:BAD:P":                                                       false,
	}
	for in, want := range cases {
		if got := Matches(in); got != want {
			t.Errorf("Matches(%q) = %v, quer %v", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/core/platform/...`  
Expected: FAIL — `undefined: HasPhysicalDefault`, `undefined: Matches`…

- [ ] **Step 3: Implementar**

`internal/core/platform/netwatch/netwatch.go`:

```go
// Package netwatch avisa mudanças de endereço/rota e diz se a máquina tem
// alguma interface física com rota padrão (§4.3 passo 5, §4.6).
package netwatch

import (
	"context"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// Watcher observa a rede.
type Watcher interface {
	// Changes recebe um aviso (agregado) por notificação bruta do SO.
	Changes() <-chan struct{}
	// HasPhysicalDefaultRoute diz se alguma interface física ativa tem rota padrão.
	HasPhysicalDefaultRoute() (bool, error)
	Close() error
}

// Tipos de interface (ipifcons.h) que não contam como rede física.
const (
	ifTypeSoftwareLoopback = 24
	ifTypePPP              = 23
	ifTypePropVirtual      = 53
	ifTypeTunnel           = 131
)

// Route é uma rota já combinada com os dados da interface.
type Route struct {
	PrefixLen uint8
	IfType    uint32
	OperUp    bool
}

// IsVirtualIfType diz se o tipo é PPP/RAS, túnel, loopback ou virtual.
func IsVirtualIfType(t uint32) bool {
	switch t {
	case ifTypeSoftwareLoopback, ifTypePPP, ifTypePropVirtual, ifTypeTunnel:
		return true
	}
	return false
}

// HasPhysicalDefault decide a partir das rotas: alguma rota padrão (/0) em
// interface física e ativa? A rota padrão da própria VPN não conta.
func HasPhysicalDefault(routes []Route) bool {
	for _, r := range routes {
		if r.PrefixLen == 0 && r.OperUp && !IsVirtualIfType(r.IfType) {
			return true
		}
	}
	return false
}

// DefaultQuiet é a espera após a última notificação (§4.6): a própria
// discagem gera uma rajada delas.
const DefaultQuiet = 2 * time.Second

// Debounce repassa um aviso só depois de quiet sem novas notificações.
func Debounce(ctx context.Context, clock shared.Clock, in <-chan struct{}, quiet time.Duration) <-chan struct{} {
	out := make(chan struct{}, 1)
	go func() {
		var timer shared.Timer
		var fire <-chan time.Time
		for {
			select {
			case <-ctx.Done():
				if timer != nil {
					timer.Stop()
				}
				return
			case _, ok := <-in:
				if !ok {
					return
				}
				if timer == nil {
					timer = clock.NewTimer(quiet)
				} else {
					timer.Reset(quiet)
				}
				fire = timer.C()
			case <-fire:
				fire = nil
				select {
				case out <- struct{}{}:
				default:
				}
			}
		}
	}()
	return out
}
```

`internal/core/platform/netwatch/netwatch_windows.go`: `NotifyUnicastIpAddressChange` + `NotifyRouteChange2` com **um** callback global; rota padrão física via `GetIpForwardTable2` + `GetIfEntry2Ex`.

```go
//go:build windows

package netwatch

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// rawChanges recebe os avisos dos callbacks. Callbacks do Go nunca são
// liberados: criamos um só, na inicialização, e um único Watcher por processo.
var (
	rawChanges     = make(chan struct{}, 1)
	changeCallback = windows.NewCallback(func(callerCtx, row, notificationType uintptr) uintptr {
		select {
		case rawChanges <- struct{}{}:
		default:
		}
		return 0
	})
)

type winWatcher struct {
	once         sync.Once
	addr, routes windows.Handle
}

// New registra NotifyUnicastIpAddressChange e NotifyRouteChange2.
func New() (Watcher, error) {
	w := &winWatcher{}
	if err := windows.NotifyUnicastIpAddressChange(windows.AF_UNSPEC, changeCallback, nil, false, &w.addr); err != nil {
		return nil, err
	}
	if err := windows.NotifyRouteChange2(windows.AF_UNSPEC, changeCallback, nil, false, &w.routes); err != nil {
		_ = windows.CancelMibChangeNotify2(w.addr)
		return nil, err
	}
	return w, nil
}

func (w *winWatcher) Changes() <-chan struct{} { return rawChanges }

func (w *winWatcher) HasPhysicalDefaultRoute() (bool, error) {
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_UNSPEC, &table); err != nil {
		return false, err
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	var routes []Route
	for _, r := range table.Rows() {
		if r.DestinationPrefix.PrefixLength != 0 {
			continue
		}
		row := windows.MibIfRow2{InterfaceLuid: r.InterfaceLuid, InterfaceIndex: r.InterfaceIndex}
		if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
			continue
		}
		routes = append(routes, Route{PrefixLen: 0, IfType: row.Type, OperUp: row.OperStatus == windows.IfOperStatusUp})
	}
	return HasPhysicalDefault(routes), nil
}

func (w *winWatcher) Close() error {
	w.once.Do(func() {
		_ = windows.CancelMibChangeNotify2(w.addr)
		_ = windows.CancelMibChangeNotify2(w.routes)
	})
	return nil
}
```

`internal/core/platform/netwatch/netwatch_other.go`:

```go
//go:build !windows

package netwatch

import "github.com/guibsu/vpn-tray-monitor/internal/core/platform"

// New fora do Windows não observa nada.
func New() (Watcher, error) { return nil, platform.ErrNotSupported }
```

`internal/core/platform/netwatch/netwatch_windows_test.go`: Só no job `test-windows`: o runner tem rota padrão física.

```go
//go:build windows

package netwatch

import "testing"

// Só no job Windows: o runner tem rede, então há rota padrão física.
func TestWindowsHasPhysicalDefaultRoute(t *testing.T) {
	w, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ok, err := w.HasPhysicalDefaultRoute()
	if err != nil || !ok {
		t.Fatalf("runner deveria ter rota padrão física: %v %v", ok, err)
	}
}
```

`internal/core/platform/acl/acl.go`:

```go
// Package acl aplica e confere a segurança da pasta de dados (§5.1): dono
// Administradores; SYSTEM e Administradores com controle total; herança
// desligada; mais ninguém.
package acl

import (
	"sort"
	"strings"
)

// DirSDDL é o descritor da pasta. O dono (O:BA) importa: quem pré-cria a
// pasta continua dono e, como dono, mantém WRITE_DAC mesmo fora da DACL.
// A DACL é a mesma que o MSI aplica via PermissionEx.
const DirSDDL = "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

var wantACEs = []string{"A;OICI;FA;;;BA", "A;OICI;FA;;;SY"}

// Securer garante a segurança de uma pasta.
type Securer interface {
	// EnsureDir cria a pasta se preciso e corrige dono e DACL se divergirem.
	// changed=true quando precisou corrigir.
	EnsureDir(path string) (changed bool, err error)
}

// section devolve o trecho de um SDDL que começa em tag ("O:", "D:") até a
// próxima seção.
func section(sddl, tag string) (string, bool) {
	i := strings.Index(sddl, tag)
	if i < 0 {
		return "", false
	}
	s := sddl[i+len(tag):]
	for _, next := range []string{"O:", "G:", "D:", "S:"} {
		if next == tag {
			continue
		}
		if j := strings.Index(s, next); j >= 0 {
			s = s[:j] // as ACEs que o Windows produz não contêm essas marcas
		}
	}
	return s, true
}

// Matches confere um SDDL com dono (O:) e DACL (D:): dono Administradores,
// DACL protegida (P) e exatamente as ACEs de SYSTEM e Administradores, em
// qualquer ordem.
func Matches(sddl string) bool {
	owner, ok := section(sddl, "O:")
	if !ok || owner != "BA" {
		return false
	}
	d, ok := section(sddl, "D:")
	if !ok {
		return false
	}
	open := strings.IndexByte(d, '(')
	if open < 0 || !strings.Contains(d[:open], "P") {
		return false
	}
	var aces []string
	for _, part := range strings.Split(d[open:], ")") {
		if part = strings.TrimPrefix(part, "("); part != "" {
			aces = append(aces, part)
		}
	}
	sort.Strings(aces)
	if len(aces) != len(wantACEs) {
		return false
	}
	for k := range aces {
		if aces[k] != wantACEs[k] {
			return false
		}
	}
	return true
}
```

`internal/core/platform/acl/acl_windows.go`: `SetNamedSecurityInfo` com `OWNER_SECURITY_INFORMATION` (dono = Administradores: quem pré-criar a pasta deixa de ser dono e perde o `WRITE_DAC` implícito) e `PROTECTED_DACL_SECURITY_INFORMATION` (desliga herança); só reaplica se `Matches` falhar.

```go
//go:build windows

package acl

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

type winSecurer struct{}

// New devolve o Securer real.
func New() Securer { return winSecurer{} }

func (winSecurer) EnsureDir(path string) (bool, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return false, err
	}
	cur, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, fmt.Errorf("lendo a segurança de %s: %w", path, err)
	}
	if Matches(cur.String()) {
		return false, nil
	}
	sd, err := windows.SecurityDescriptorFromString(DirSDDL)
	if err != nil {
		return false, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return false, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return false, err
	}
	// Dono = Administradores (tira o WRITE_DAC implícito de quem pré-criou a
	// pasta); PROTECTED desliga a herança; o Windows propaga as ACEs OICI.
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner, nil, dacl, nil)
	if err != nil {
		return false, fmt.Errorf("aplicando a segurança em %s: %w", path, err)
	}
	return true, nil
}
```

`internal/core/platform/acl/acl_other.go`:

```go
//go:build !windows

package acl

import "os"

type posixSecurer struct{}

// New fora do Windows: pasta 0700 (desenvolvimento com VPNMON_DATA_DIR).
func New() Securer { return posixSecurer{} }

func (posixSecurer) EnsureDir(path string) (bool, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return false, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if st.Mode().Perm() == 0o700 {
		return false, nil
	}
	return true, os.Chmod(path, 0o700)
}
```

`internal/core/platform/acl/acl_windows_test.go`: Só no job `test-windows` (runner é administrador): aplica, confere e a segunda chamada é no-op.

```go
//go:build windows

package acl

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// Só no job Windows (runner é administrador): aplica dono e DACL, confere,
// e a segunda chamada não muda nada.
func TestWindowsEnsureDir(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("exige processo elevado (o runner do CI é)")
	}
	dir := filepath.Join(t.TempDir(), "VPNMonitor")
	changed, err := New().EnsureDir(dir)
	if err != nil || !changed {
		t.Fatalf("primeira aplicação: %v %v", changed, err)
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || !Matches(sd.String()) {
		t.Fatalf("segurança resultante %v: %v", sd, err)
	}
	if changed, err := New().EnsureDir(dir); err != nil || changed {
		t.Fatalf("segunda chamada deveria ser no-op: %v %v", changed, err)
	}
}
```

`internal/core/platform/fake/netwatch_acl.go`:

```go
package fake

import (
	"sync"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/acl"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/netwatch"
)

// Net é um netwatch.Watcher controlável.
type Net struct {
	mu       sync.Mutex
	physical bool
	ch       chan struct{}
}

// NewNet cria o fake com rede física presente.
func NewNet() *Net { return &Net{physical: true, ch: make(chan struct{}, 1)} }

// SetPhysical liga/desliga a rede física e emite um aviso de mudança.
func (n *Net) SetPhysical(ok bool) {
	n.mu.Lock()
	n.physical = ok
	n.mu.Unlock()
	select {
	case n.ch <- struct{}{}:
	default:
	}
}

func (n *Net) Changes() <-chan struct{} { return n.ch }

func (n *Net) HasPhysicalDefaultRoute() (bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.physical, nil
}

func (n *Net) Close() error { return nil }

var _ netwatch.Watcher = (*Net)(nil)

// ACL registra as pastas garantidas.
type ACL struct {
	mu   sync.Mutex
	Dirs []string
}

func (a *ACL) EnsureDir(path string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Dirs = append(a.Dirs, path)
	return false, nil
}

var _ acl.Securer = (*ACL)(nil)
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race -count=10 ./internal/core/platform/netwatch/ ./internal/core/platform/acl/`  
Expected: PASS

Run: `go vet ./... && GOOS=windows go vet ./...`  
Expected: sem saída

Run: `GOOS=windows go test -c -o /dev/null ./internal/core/platform/netwatch/ && GOOS=windows go test -c -o /dev/null ./internal/core/platform/acl/`  
Expected: compila

- [ ] **Step 5: Commit**

```bash
git add internal/core/platform && git commit -m "feat(platform): avisos de rota/endereço com espera, rede física e ACL da pasta de dados"
```

---

### Task 11: core/platform/svc: laço do SCM, energia, install/uninstall e PID do serviço

**Files:**
- Create: `internal/core/platform/svc/svc.go`
- Create: `internal/core/platform/svc/svc_windows.go`
- Create: `internal/core/platform/svc/svc_other.go`
- Test: `internal/core/platform/svc/svc_test.go`

**Interfaces:**
- Consumes: `platform.ErrNotSupported`.
- Produces: `svc.ServiceName = "VPNMonitor"`, `svc.DefaultStopTimeout = 10s`, `svc.PBT_APMRESUMEAUTOMATIC`, `svc.PBT_APMRESUMESUSPEND`;
  `svc.Request{Cmd; EventType}`, `svc.Cmd` (`CmdStop`, `CmdPreShutdown`, `CmdPowerEvent`), `svc.State` (`StateStartPending`, `StateRunning`, `StateStopPending`);
  `svc.Hooks{Run func(ctx) error; OnResume func(); StopTimeout time.Duration}`; `svc.Loop(Hooks, <-chan Request, report func(State)) uint32`;
  `svc.IsService() (bool, error)`, `svc.Run(Hooks) error`, `svc.Install(exePath) error`, `svc.Uninstall() error`, `svc.ServicePID() (uint32, error)`, `svc.IsElevated() bool`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/core/platform/svc/svc_test.go`:

```go
package svc

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu     sync.Mutex
	states []State
}

func (r *recorder) report(s State) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, s)
}

func (r *recorder) get() []State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]State(nil), r.states...)
}

func TestLoopStopCancelsRun(t *testing.T) {
	var rec recorder
	reqs := make(chan Request)
	resumed := make(chan struct{}, 1)
	h := Hooks{
		Run:      func(ctx context.Context) error { <-ctx.Done(); return nil },
		OnResume: func() { resumed <- struct{}{} },
	}
	code := make(chan uint32)
	go func() { code <- Loop(h, reqs, rec.report) }()

	reqs <- Request{Cmd: CmdPowerEvent, EventType: 0x4} // suspensão: ignora
	reqs <- Request{Cmd: CmdPowerEvent, EventType: PBT_APMRESUMEAUTOMATIC}
	select {
	case <-resumed:
	case <-time.After(time.Second):
		t.Fatal("OnResume não chamado")
	}
	reqs <- Request{Cmd: CmdStop}
	if c := <-code; c != 0 {
		t.Fatalf("código %d", c)
	}
	want := []State{StateStartPending, StateRunning, StateStopPending}
	if got := rec.get(); !reflect.DeepEqual(got, want) {
		t.Fatalf("estados %v, quer %v", got, want)
	}
}

func TestLoopPreShutdownAndTimeout(t *testing.T) {
	var rec recorder
	reqs := make(chan Request, 1)
	h := Hooks{
		Run:         func(ctx context.Context) error { select {} },
		StopTimeout: 20 * time.Millisecond,
	}
	reqs <- Request{Cmd: CmdPreShutdown}
	if c := Loop(h, reqs, rec.report); c != 1 {
		t.Fatalf("Run travado deve dar código 1, veio %d", c)
	}
}

func TestLoopRunFailsAlone(t *testing.T) {
	var rec recorder
	h := Hooks{Run: func(context.Context) error { return errors.New("pipe ocupado") }}
	if c := Loop(h, make(chan Request), rec.report); c != 1 {
		t.Fatalf("código %d", c)
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/core/platform/svc/`  
Expected: FAIL — `undefined: Loop`…

- [ ] **Step 3: Implementar**

O corpo do handler (`Loop`) é neutro e testado no Linux: stop/preshutdown cancelam `Run` e esperam até `StopTimeout`; `Run` travado → código 1; retomada chama `OnResume`.

`internal/core/platform/svc/svc.go`:

```go
// Package svc integra com o Service Control Manager: ciclo de vida do
// serviço, eventos de energia, instalação manual e consulta do PID.
// A lógica do laço de controle (Loop) é neutra e testada no Linux; o
// adaptador Windows só traduz tipos.
package svc

import (
	"context"
	"time"
)

// Nome e textos do serviço (§8).
const (
	ServiceName        = "VPNMonitor"
	DisplayName        = "VPN Monitor"
	Description        = "Mantém as VPNs nativas do Windows conectadas e reconecta após quedas."
	DefaultStopTimeout = 10 * time.Second
)

// Eventos de energia (pbt.h) que indicam retomada.
const (
	PBT_APMRESUMESUSPEND   = 0x7
	PBT_APMRESUMEAUTOMATIC = 0x12
)

// Cmd é um pedido do SCM já traduzido.
type Cmd int

const (
	CmdStop Cmd = iota
	CmdPreShutdown
	CmdPowerEvent
)

// Request é um pedido do SCM.
type Request struct {
	Cmd       Cmd
	EventType uint32
}

// State é o estado informado ao SCM.
type State int

const (
	StateStartPending State = iota
	StateRunning
	StateStopPending
)

// Hooks é o que o serviço faz.
type Hooks struct {
	// Run sobe tudo e bloqueia até ctx ser cancelado; deve voltar em StopTimeout.
	Run func(ctx context.Context) error
	// OnResume é chamado na retomada de energia.
	OnResume func()
	// StopTimeout é o prazo para Run voltar após o pedido de parada.
	StopTimeout time.Duration
}

// Loop é o corpo do handler do SCM. Informa estados por report e devolve o
// código de saída: 0 em parada pedida; 1 se Run terminou sozinho com erro
// ou não voltou no prazo.
func Loop(h Hooks, reqs <-chan Request, report func(State)) uint32 {
	if h.StopTimeout <= 0 {
		h.StopTimeout = DefaultStopTimeout
	}
	report(StateStartPending)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	report(StateRunning)
	for {
		select {
		case err := <-done:
			report(StateStopPending)
			if err != nil {
				return 1
			}
			return 0
		case r, ok := <-reqs:
			if !ok {
				r = Request{Cmd: CmdStop}
			}
			switch r.Cmd {
			case CmdStop, CmdPreShutdown:
				report(StateStopPending)
				cancel()
				select {
				case <-done:
					return 0
				case <-time.After(h.StopTimeout):
					return 1
				}
			case CmdPowerEvent:
				if (r.EventType == PBT_APMRESUMEAUTOMATIC || r.EventType == PBT_APMRESUMESUSPEND) && h.OnResume != nil {
					h.OnResume()
				}
			}
		}
	}
}
```

`internal/core/platform/svc/svc_windows.go`: Adaptador fino sobre `x/sys/windows/svc`: traduz `ChangeRequest` para `svc.Request` e estados para `svc.Status`
(aceita `Stop|PreShutdown|PowerEvent`). `Install`: início automático, dependência `RasMan`, LocalSystem, recuperação 5 s/30 s/60 s zerando
em 1 dia, origem do Event Log. O `eventlog.InstallAsEventCreate` do x/sys devolve um erro **de texto** ("registry key already exists")
quando a origem já existe (MSI ou install anterior) — `errors.Is(err, ERROR_ALREADY_EXISTS)` nunca casaria, por isso a checagem é por
`strings.Contains`. `ServicePID` usa `SC_MANAGER_CONNECT` + `SERVICE_QUERY_STATUS` (funciona sem elevação; `mgr.Connect` exigiria
administrador). As variantes `installNamed`/`uninstallNamed`/`servicePIDNamed`/`runNamed` existem para o teste usar outro nome.

```go
//go:build windows

package svc

import (
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const accepts = svc.AcceptStop | svc.AcceptPreShutdown | svc.AcceptPowerEvent

type handler struct{ hooks Hooks }

func (h handler) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	reqs := make(chan Request)
	stop := make(chan struct{})
	defer close(stop)
	send := func(r Request) {
		select {
		case reqs <- r:
		case <-stop: // Loop já terminou
		}
	}
	go func() {
		for {
			select {
			case <-stop:
				return
			case c := <-r:
				switch c.Cmd {
				case svc.Interrogate:
					s <- c.CurrentStatus
				case svc.Stop:
					send(Request{Cmd: CmdStop})
				case svc.PreShutdown:
					send(Request{Cmd: CmdPreShutdown})
				case svc.PowerEvent:
					send(Request{Cmd: CmdPowerEvent, EventType: c.EventType})
				}
			}
		}
	}()
	report := func(st State) {
		switch st {
		case StateStartPending:
			s <- svc.Status{State: svc.StartPending, WaitHint: 10000}
		case StateRunning:
			s <- svc.Status{State: svc.Running, Accepts: accepts}
		case StateStopPending:
			s <- svc.Status{State: svc.StopPending, WaitHint: uint32(DefaultStopTimeout / time.Millisecond)}
		}
	}
	return false, Loop(h.hooks, reqs, report)
}

// IsService diz se o processo foi iniciado pelo SCM.
func IsService() (bool, error) { return svc.IsWindowsService() }

// Run entrega o processo ao SCM.
func Run(h Hooks) error { return runNamed(ServiceName, h) }

func runNamed(name string, h Hooks) error { return svc.Run(name, handler{h}) }

// Install registra o serviço (início automático, depende do RasMan,
// LocalSystem, recuperação 5 s/30 s/60 s zerando em 1 dia) e a origem do
// Event Log. É o caminho sem MSI.
func Install(exePath string) error {
	return installNamed(ServiceName, DisplayName, exePath, []string{"RasMan"})
}

func installNamed(name, display, exePath string, deps []string, args ...string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	if s, err := m.OpenService(name); err == nil {
		s.Close()
		return fmt.Errorf("o serviço %s já está instalado", name)
	}
	s, err := m.CreateService(name, exePath, mgr.Config{
		DisplayName:  display,
		Description:  Description,
		StartType:    mgr.StartAutomatic,
		Dependencies: deps,
	}, args...)
	if err != nil {
		return err
	}
	defer s.Close()
	actions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}
	if err := s.SetRecoveryActions(actions, uint32((24 * time.Hour).Seconds())); err != nil {
		return fmt.Errorf("configurando recuperação: %w", err)
	}
	// O MSI (util:EventSource) ou um install anterior pode já ter registrado a
	// origem; o x/sys devolve um erro de texto ("registry key already exists"),
	// não ERROR_ALREADY_EXISTS, então a checagem é pelo texto.
	if err := eventlog.InstallAsEventCreate(name, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("registrando a origem do Event Log: %w", err)
	}
	return nil
}

// Uninstall para o serviço (sem derrubar VPNs) e remove o registro.
func Uninstall() error { return uninstallNamed(ServiceName) }

func uninstallNamed(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("o serviço %s não está instalado", name)
	}
	defer s.Close()
	if st, err := s.Query(); err == nil && st.State != svc.Stopped {
		_, _ = s.Control(svc.Stop)
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
			if st, err := s.Query(); err != nil || st.State == svc.Stopped {
				break
			}
		}
	}
	if err := s.Delete(); err != nil {
		return err
	}
	_ = eventlog.Remove(name)
	return nil
}

// ServicePID devolve o PID do serviço em execução, com acesso mínimo
// (funciona sem elevação; usado pela bandeja e pela CLI para conferir o
// servidor do pipe).
func ServicePID() (uint32, error) { return servicePIDNamed(ServiceName) }

func servicePIDNamed(name string) (uint32, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return 0, err
	}
	defer windows.CloseServiceHandle(scm)
	n, _ := windows.UTF16PtrFromString(name)
	h, err := windows.OpenService(scm, n, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return 0, err
	}
	defer windows.CloseServiceHandle(h)
	var st windows.SERVICE_STATUS_PROCESS
	var needed uint32
	if err := windows.QueryServiceStatusEx(h, windows.SC_STATUS_PROCESS_INFO,
		(*byte)(unsafe.Pointer(&st)), uint32(unsafe.Sizeof(st)), &needed); err != nil {
		return 0, err
	}
	if st.CurrentState != windows.SERVICE_RUNNING || st.ProcessId == 0 {
		return 0, fmt.Errorf("o serviço %s não está em execução", name)
	}
	return st.ProcessId, nil
}

// IsElevated diz se o processo roda elevado (administrador).
func IsElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }
```

`internal/core/platform/svc/svc_other.go`:

```go
//go:build !windows

package svc

import (
	"os"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform"
)

func IsService() (bool, error)    { return false, nil }
func Run(Hooks) error             { return platform.ErrNotSupported }
func Install(string) error        { return platform.ErrNotSupported }
func Uninstall() error            { return platform.ErrNotSupported }
func ServicePID() (uint32, error) { return 0, platform.ErrNotSupported }
func IsElevated() bool            { return os.Geteuid() == 0 }
```

`internal/core/platform/svc/svc_windows_test.go`: Só no job `test-windows` (exige elevação; senão `t.Skip`): instala um serviço de teste `VPNMonitorTeste<pid>` sem dependência
de RasMan, cujo executável é o próprio binário de teste (`-test.run=^TestWindowsServiceHelper$ <nome>`), inicia pelo SCM, confere
`ServicePID` (diferente do PID do teste), desinstala e confere que o PID some. Também: `ServicePID` de serviço inexistente dá erro.

```go
//go:build windows

package svc

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Só no job Windows: consulta de PID com acesso mínimo para serviço inexistente.
func TestWindowsServicePIDMissing(t *testing.T) {
	if _, err := servicePIDNamed("VPNMonitorInexistente"); err == nil {
		t.Fatal("serviço não instalado deveria dar erro")
	}
}

// TestWindowsServiceHelper é o "serviço" do teste abaixo: o próprio binário de
// teste, registrado no SCM com -test.run apontando para cá e o nome do
// serviço como argumento posicional. Fora do SCM, não faz nada.
func TestWindowsServiceHelper(t *testing.T) {
	if ok, _ := svc.IsWindowsService(); !ok {
		t.Skip("só roda quando iniciado pelo SCM")
	}
	name := flag.Arg(0)
	_ = runNamed(name, Hooks{Run: func(ctx context.Context) error { <-ctx.Done(); return nil }})
}

// Só no job Windows (exige elevação): Install → start → ServicePID →
// Uninstall com um nome de serviço de teste, sem RasMan como dependência.
func TestWindowsInstallStartPIDUninstall(t *testing.T) {
	if !IsElevated() {
		t.Skip("exige processo elevado (o runner do CI é)")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("VPNMonitorTeste%d", os.Getpid())
	if err := installNamed(name, name, exe, nil, "-test.run=^TestWindowsServiceHelper$", name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = uninstallNamed(name) })
	if err := installNamed(name, name, exe, nil); err == nil {
		t.Fatal("instalar de novo deveria dizer que já está instalado")
	}

	m, err := mgr.Connect()
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.OpenService(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	m.Disconnect()

	var pid uint32
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if pid, err = servicePIDNamed(name); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if pid == 0 || pid == uint32(os.Getpid()) {
		t.Fatalf("PID do serviço de teste: %d (%v)", pid, err)
	}
	if err := uninstallNamed(name); err != nil {
		t.Fatal(err)
	}
	if _, err := servicePIDNamed(name); err == nil {
		t.Fatal("após Uninstall o serviço não pode responder")
	}
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race ./internal/core/platform/svc/`  
Expected: PASS

Run: `go vet ./... && GOOS=windows go vet ./...`  
Expected: sem saída

Run: `GOOS=windows go test -c -o /dev/null ./internal/core/platform/svc/`  
Expected: compila

- [ ] **Step 5: Commit**

```bash
git add internal/core/platform/svc && git commit -m "feat(svc): laço do SCM testável, eventos de energia, instalação manual e PID do serviço"
```

---

### Task 12: monitor/domain: estados, parâmetros, estado inicial e textos de aviso

**Files:**
- Create: `internal/features/monitor/domain/state.go`
- Test: `internal/features/monitor/domain/state_test.go`

**Interfaces:**
- Consumes: `config.VPN`, `config.RawVPN`, `config.Pause`, `config.CheckKind` (tarefas 4–5); `ras.Class`, `ras.ERROR_CANNOT_FIND_PHONEBOOK_ENTRY` (tarefa 7).
- Produces: `domain.State` (string): `Desconhecido`, `Conectada`, `Degradada`, `Reconectando`, `Desconectada`, `CredencialInvalida`, `ErroConfig`, `Pausada`, `SemRede`, `Desativada`;
  `domain.Params{Name, Entry string; Enabled bool; CheckKind; Interval; Failures int; Grace; ConnectTimeout; MaxBackoff}` e `ParamsFrom(config.VPN)`;
  `domain.Op` (`OpNone`, `OpProbeLink`, `OpProbeReach`, `OpDial`, `OpHangupDial`); `domain.DialError{Class ras.Class; Code uint32; Message string}`;
  `domain.Status{State; Since; Op; Failures; Attempt; NextTick; NextAttempt; GraceUntil; PausedUntil; PausedIndefinite; Blocked State; BlockedFP; RejectedAt; LastManualTry; LastErr *DialError; LastCheck; LastRTT; DownSince; Reconnects []time.Time}`
  com `PauseRecord() config.Pause` e `Reconnects24h(now) int`; `domain.Initial(Params, now, config.Pause) Status`;
  `domain.Notice{Kind NoticeKind; Text}` (`NoticeDown`, `NoticeUp`, `NoticeCredential`, `NoticeConfig`); `domain.FormatOutage(d) string`; `domain.ConfigErrorText(*DialError, entry) string`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/monitor/domain/state_test.go`:

```go
package domain

import (
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

var t0 = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func params() Params {
	return ParamsFrom(config.RawVPN{Name: "Matriz", RasEntry: "VPN Matriz",
		Check: &config.RawCheck{Kind: config.CheckPing, Host: "10.0.0.1"}}.Normalize())
}

func TestParamsFrom(t *testing.T) {
	p := params()
	if p.Interval != 30*time.Second || p.Failures != 3 || p.Grace != 15*time.Second ||
		p.ConnectTimeout != time.Minute || p.MaxBackoff != 5*time.Minute || !p.Enabled || p.CheckKind != config.CheckPing {
		t.Fatalf("%+v", p)
	}
}

func TestInitial(t *testing.T) {
	p := params()
	if s := Initial(p, t0, config.Pause{}); s.State != Desconhecido || !s.NextTick.Equal(t0) {
		t.Fatalf("normal: %+v", s)
	}
	p.Enabled = false
	if s := Initial(p, t0, config.Pause{}); s.State != Desativada || !s.NextTick.IsZero() {
		t.Fatalf("desativada: %+v", s)
	}
	p.Enabled = true
	if s := Initial(p, t0, config.Pause{Indefinite: true}); s.State != Pausada || !s.PausedIndefinite {
		t.Fatalf("pausa indefinida: %+v", s)
	}
	until := t0.Add(time.Hour)
	s := Initial(p, t0, config.Pause{UntilUnix: until.Unix()})
	if s.State != Pausada || !s.PausedUntil.Equal(until) || !s.NextTick.Equal(until) {
		t.Fatalf("pausa temporária: %+v", s)
	}
	if s.PauseRecord() != (config.Pause{UntilUnix: until.Unix()}) {
		t.Fatalf("PauseRecord: %+v", s.PauseRecord())
	}
	if s := Initial(p, t0, config.Pause{UntilUnix: t0.Add(-time.Minute).Unix()}); s.State != Desconhecido {
		t.Fatalf("pausa vencida não vale: %+v", s)
	}
}

func TestFormatOutage(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second: "45 s", 4 * time.Minute: "4 min", 2 * time.Hour: "2 h",
		2*time.Hour + 5*time.Minute: "2 h 5 min",
	}
	for d, want := range cases {
		if got := FormatOutage(d); got != want {
			t.Errorf("%v → %q, quer %q", d, got, want)
		}
	}
}

func TestConfigErrorText(t *testing.T) {
	got := ConfigErrorText(&DialError{Code: ras.ERROR_CANNOT_FIND_PHONEBOOK_ENTRY}, "VPN Matriz")
	if want := `a entrada RAS "VPN Matriz" não existe no catálogo de todos os usuários; recrie-a com Add-VpnConnection -AllUserConnection`; got != want {
		t.Fatal(got)
	}
	if got := ConfigErrorText(&DialError{Code: 720, Message: "sem protocolos"}, "x"); got != "erro 720: sem protocolos" {
		t.Fatal(got)
	}
}

func TestReconnects24h(t *testing.T) {
	s := Status{Reconnects: []time.Time{t0.Add(-25 * time.Hour), t0.Add(-time.Hour), t0}}
	if n := s.Reconnects24h(t0); n != 2 {
		t.Fatal(n)
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/features/monitor/domain/`  
Expected: FAIL — `undefined: ParamsFrom`…

- [ ] **Step 3: Implementar**

`internal/features/monitor/domain/state.go`:

```go
// Package domain tem o modelo do monitor de uma VPN: estados, entradas e a
// política de transição como função pura (Decide). Nada aqui faz E/S.
package domain

import (
	"fmt"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

// State é o estado de uma VPN (§4.2). O texto é o mesmo do protocolo.
type State string

const (
	Desconhecido       State = "Desconhecido"
	Conectada          State = "Conectada"
	Degradada          State = "Degradada"
	Reconectando       State = "Reconectando"
	Desconectada       State = "Desconectada"
	CredencialInvalida State = "CredencialInvalida"
	ErroConfig         State = "ErroConfig"
	Pausada            State = "Pausada"
	SemRede            State = "SemRede"
	Desativada         State = "Desativada"
)

// isDown diz se o estado conta como "fora do ar" para o aviso de queda.
// Degradada não conta: uma perda de ping isolada não é queda.
func isDown(s State) bool {
	switch s {
	case Reconectando, Desconectada, CredencialInvalida, ErroConfig, SemRede:
		return true
	}
	return false
}

// Params é o trecho da config que o domínio usa, já em durações.
type Params struct {
	Name           string
	Entry          string
	Enabled        bool
	CheckKind      config.CheckKind
	Interval       time.Duration
	Failures       int
	Grace          time.Duration
	ConnectTimeout time.Duration
	MaxBackoff     time.Duration
}

// ParamsFrom converte a config de uma VPN.
func ParamsFrom(v config.VPN) Params {
	sec := func(n int) time.Duration { return time.Duration(n) * time.Second }
	return Params{
		Name: v.Name, Entry: v.RasEntry, Enabled: v.Enabled, CheckKind: v.Check.Kind,
		Interval: sec(v.IntervalSeconds), Failures: v.FailuresBeforeReconnect,
		Grace: sec(v.GraceAfterConnectSeconds), ConnectTimeout: sec(v.ConnectTimeoutSeconds),
		MaxBackoff: sec(v.MaxBackoffSeconds),
	}
}

// Op é a operação externa em andamento (ou a pedir).
type Op int

const (
	OpNone       Op = iota
	OpProbeLink     // ras.Status + presença de rede física
	OpProbeReach    // ping/tcp
	OpDial          // discar
	OpHangupDial    // desligar e discar (túnel zumbi, reconexão manual)
)

// DialError descreve a falha de uma discagem.
type DialError struct {
	Class   ras.Class
	Code    uint32
	Message string
}

// Status é o estado completo de uma VPN, do qual o supervisor é o dono.
type Status struct {
	State State
	Since time.Time
	// Op em andamento (OpNone se ocioso). OpHangupDial vira OpDial.
	Op Op
	// Failures é o contador de falhas de alcance consecutivas.
	Failures int
	// Attempt conta discagens falhas seguidas (expoente do backoff).
	Attempt     int
	NextTick    time.Time // próximo despertar do temporizador (zero = nenhum)
	NextAttempt time.Time // próxima discagem permitida pelo backoff
	GraceUntil  time.Time
	// Pausa.
	PausedUntil      time.Time
	PausedIndefinite bool
	// Blocked guarda CredencialInvalida/ErroConfig durante uma pausa.
	Blocked State
	// BlockedFP é a impressão digital da credencial rejeitada.
	BlockedFP     string
	RejectedAt    time.Time
	LastManualTry time.Time
	LastErr       *DialError
	LastCheck     time.Time
	LastRTT       time.Duration
	DownSince     time.Time
	Reconnects    []time.Time
}

// Initial é o estado ao criar o supervisor. pause vem do state.json.
func Initial(p Params, now time.Time, pause config.Pause) Status {
	s := Status{State: Desconhecido, Since: now, NextTick: now}
	switch {
	case !p.Enabled:
		s = Status{State: Desativada, Since: now}
	case pause.Indefinite:
		s = Status{State: Pausada, Since: now, PausedIndefinite: true}
	case pause.UntilUnix > now.Unix():
		u := time.Unix(pause.UntilUnix, 0)
		s = Status{State: Pausada, Since: now, PausedUntil: u, NextTick: u}
	}
	return s
}

// PauseRecord devolve a pausa a persistir (zero se não pausada).
func (s Status) PauseRecord() config.Pause {
	if s.State != Pausada {
		return config.Pause{}
	}
	if s.PausedIndefinite {
		return config.Pause{Indefinite: true}
	}
	return config.Pause{UntilUnix: s.PausedUntil.Unix()}
}

// Reconnects24h conta reconexões bem-sucedidas nas últimas 24 h.
func (s Status) Reconnects24h(now time.Time) int {
	n := 0
	for _, t := range s.Reconnects {
		if now.Sub(t) < 24*time.Hour {
			n++
		}
	}
	return n
}

// NoticeKind classifica os avisos ao usuário (§4.8).
type NoticeKind string

const (
	NoticeDown       NoticeKind = "down"
	NoticeUp         NoticeKind = "up"
	NoticeCredential NoticeKind = "credential"
	NoticeConfig     NoticeKind = "config"
)

// Notice é um aviso para balão e log.
type Notice struct {
	Kind NoticeKind
	Text string
}

// FormatOutage escreve uma duração como "45 s", "4 min", "2 h", "2 h 5 min".
func FormatOutage(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	h, m := int(d.Hours()), int(d.Minutes())%60
	if m == 0 {
		return fmt.Sprintf("%d h", h)
	}
	return fmt.Sprintf("%d h %d min", h, m)
}

func noticeDown(name string) Notice {
	return Notice{NoticeDown, fmt.Sprintf("VPN %s caiu", name)}
}

func noticeUp(name string, outage time.Duration) Notice {
	return Notice{NoticeUp, fmt.Sprintf("VPN %s voltou (fora do ar por %s)", name, FormatOutage(outage))}
}

func noticeCredential(name string) Notice {
	return Notice{NoticeCredential, fmt.Sprintf("VPN %s: credencial rejeitada — rode vpnmon-svc credential set \"%s\"", name, name)}
}

// ConfigErrorText explica um erro de configuração, com a dica da §4.4 para
// entrada inexistente.
func ConfigErrorText(e *DialError, entry string) string {
	if e.Code == ras.ERROR_CANNOT_FIND_PHONEBOOK_ENTRY {
		return fmt.Sprintf("a entrada RAS %q não existe no catálogo de todos os usuários; recrie-a com Add-VpnConnection -AllUserConnection", entry)
	}
	return fmt.Sprintf("erro %d: %s", e.Code, e.Message)
}

func noticeConfig(name, reason string) Notice {
	return Notice{NoticeConfig, fmt.Sprintf("VPN %s: erro de configuração: %s", name, reason)}
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race ./internal/features/monitor/domain/`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/features/monitor/domain && git commit -m "feat(monitor): modelo de estados, parâmetros e avisos do domínio"
```

---

### Task 13: monitor/domain: Decide — ciclo automático (enlace, alcance, discagem, rede, energia)

**Files:**
- Create: `internal/features/monitor/domain/decide.go`
- Test: `internal/features/monitor/domain/decide_test.go`

**Interfaces:**
- Consumes: `domain.Status`, `Params`, `Op`, `DialError`, avisos (tarefa 12); `shared.Backoff` (tarefa 2); `ras.Class*`.
- Produces: `domain.InputKind` (`InTick`, `InWake`, `InPowerResume`, `InLinkResult`, `InReachResult`, `InDialResult`, `InCheckNow`, `InReconnect`, `InPause`, `InResume`, `InCredentialChanged`);
  `domain.Input{Kind; LinkUp, Network bool; ReachOK bool; RTT; DialErr *DialError; PauseUntil time.Time; Fingerprint string}`;
  `domain.Env{Now time.Time; Rand float64}`; `domain.ReplyCode` (`ReplyOK`, `ReplyPaused`, `ReplyAlreadyReconnecting`, `ReplyCredentialRejected`, `ReplyDisabled`), `domain.Reply{Code; Message}`;
  `domain.Decision{Next Status; Action Op; Cancel, Manual bool; Reply *Reply; Notices []Notice}`;
  `domain.Decide(s Status, in Input, p Params, env Env) Decision` (pura). Nesta tarefa os comandos ainda caem no `default` (sem efeito); a tarefa 14 os liga.

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/monitor/domain/decide_test.go`:

```go
package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

func env(d time.Duration) Env { return Env{Now: t0.Add(d), Rand: 0.5} }

func st(state State, mut ...func(*Status)) Status {
	s := Status{State: state, Since: t0}
	for _, m := range mut {
		m(&s)
	}
	return s
}

func withOp(op Op) func(*Status) { return func(s *Status) { s.Op = op } }

func noticeKinds(d Decision) []NoticeKind {
	var k []NoticeKind
	for _, n := range d.Notices {
		k = append(k, n.Kind)
	}
	return k
}

func TestTickStartsLinkProbe(t *testing.T) {
	for _, state := range []State{Desconhecido, Conectada, Degradada, Reconectando, Desconectada, SemRede} {
		d := Decide(st(state), Input{Kind: InTick}, params(), env(0))
		if d.Action != OpProbeLink || d.Next.Op != OpProbeLink {
			t.Errorf("%s: ação %v", state, d.Action)
		}
	}
	for _, state := range []State{CredencialInvalida, ErroConfig, Desativada} {
		if d := Decide(st(state), Input{Kind: InTick}, params(), env(0)); d.Action != OpNone {
			t.Errorf("%s não deve verificar sozinho", state)
		}
	}
	if d := Decide(st(Conectada, withOp(OpDial)), Input{Kind: InTick}, params(), env(0)); d.Action != OpNone {
		t.Error("com operação em andamento o tique é ignorado")
	}
}

func TestLinkResults(t *testing.T) {
	p := params()
	cases := []struct {
		name   string
		from   Status
		in     Input
		state  State
		action Op
		notes  int
	}{
		{"sem rede", st(Conectada, withOp(OpProbeLink)), Input{Kind: InLinkResult, Network: false}, SemRede, OpNone, 1},
		{"enlace caído disca", st(Conectada, withOp(OpProbeLink)), Input{Kind: InLinkResult, Network: true}, Reconectando, OpDial, 1},
		{"enlace caído na partida não avisa", st(Desconhecido, withOp(OpProbeLink)), Input{Kind: InLinkResult, Network: true}, Reconectando, OpDial, 0},
		{"enlace de pé verifica alcance", st(Desconhecido, withOp(OpProbeLink)), Input{Kind: InLinkResult, Network: true, LinkUp: true}, Desconhecido, OpProbeReach, 0},
	}
	for _, c := range cases {
		d := Decide(c.from, c.in, p, env(time.Second))
		if d.Next.State != c.state || d.Action != c.action || len(d.Notices) != c.notes {
			t.Errorf("%s: estado %s ação %v avisos %v", c.name, d.Next.State, d.Action, noticeKinds(d))
		}
	}
}

func TestLinkKindAndGraceSkipReach(t *testing.T) {
	p := params()
	p.CheckKind = config.CheckLink
	d := Decide(st(Desconhecido, withOp(OpProbeLink)), Input{Kind: InLinkResult, Network: true, LinkUp: true}, p, env(0))
	if d.Next.State != Conectada || d.Action != OpNone || !d.Next.NextTick.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("link: %+v", d)
	}
	p = params()
	grace := st(Conectada, withOp(OpProbeLink), func(s *Status) { s.GraceUntil = t0.Add(15 * time.Second) })
	if d := Decide(grace, Input{Kind: InLinkResult, Network: true, LinkUp: true}, p, env(10*time.Second)); d.Action != OpNone {
		t.Fatal("durante a carência não verifica alcance")
	}
	if d := Decide(grace, Input{Kind: InLinkResult, Network: true, LinkUp: true}, p, env(15*time.Second)); d.Action != OpProbeReach {
		t.Fatal("após a carência verifica alcance")
	}
}

func TestBackoffGateKeepsWaiting(t *testing.T) {
	waiting := st(Reconectando, withOp(OpProbeLink), func(s *Status) { s.NextAttempt = t0.Add(time.Minute); s.Attempt = 2 })
	d := Decide(waiting, Input{Kind: InLinkResult, Network: true}, params(), env(10*time.Second))
	if d.Action != OpNone || d.Next.State != Reconectando || !d.Next.NextTick.Equal(t0.Add(time.Minute)) {
		t.Fatalf("despertar antes do backoff não disca: %+v", d)
	}
	fresh := st(Conectada, withOp(OpProbeLink), func(s *Status) { s.NextAttempt = t0.Add(time.Minute) })
	if d := Decide(fresh, Input{Kind: InLinkResult, Network: true}, params(), env(0)); d.Next.State != Desconectada {
		t.Fatalf("queda dentro do backoff = Desconectada, veio %s", d.Next.State)
	}
}

func TestNoNetworkDoesNotSpendBackoff(t *testing.T) {
	s := st(Reconectando, withOp(OpProbeLink), func(s *Status) { s.Attempt = 3 })
	d := Decide(s, Input{Kind: InLinkResult, Network: false}, params(), env(0))
	if d.Next.State != SemRede || d.Next.Attempt != 3 || d.Action != OpNone {
		t.Fatalf("%+v", d.Next)
	}
}

func TestReachFailuresThenZombieHangup(t *testing.T) {
	p := params()
	s := st(Conectada, withOp(OpProbeReach))
	for i := 1; i < p.Failures; i++ {
		d := Decide(s, Input{Kind: InReachResult}, p, env(0))
		if d.Next.State != Degradada || d.Next.Failures != i || d.Action != OpNone || len(d.Notices) != 0 {
			t.Fatalf("falha %d: %+v", i, d)
		}
		s = d.Next
		s.Op = OpProbeReach
	}
	d := Decide(s, Input{Kind: InReachResult}, p, env(0))
	if d.Next.State != Reconectando || d.Action != OpHangupDial || d.Next.Op != OpDial || d.Next.Failures != 0 {
		t.Fatalf("zumbi: %+v", d)
	}
	if k := noticeKinds(d); len(k) != 1 || k[0] != NoticeDown {
		t.Fatalf("avisos %v", k)
	}
}

func TestReachOKResetsAndReportsLatency(t *testing.T) {
	s := st(Degradada, withOp(OpProbeReach), func(s *Status) { s.Failures = 2 })
	d := Decide(s, Input{Kind: InReachResult, ReachOK: true, RTT: 12 * time.Millisecond}, params(), env(0))
	if d.Next.State != Conectada || d.Next.Failures != 0 || d.Next.LastRTT != 12*time.Millisecond || len(d.Notices) != 0 {
		t.Fatalf("%+v", d)
	}
}

func dialErr(code uint32) *DialError {
	return &DialError{Class: ras.Classify(code), Code: code, Message: "msg"}
}

func TestDialResults(t *testing.T) {
	p := params()
	dialing := st(Reconectando, withOp(OpDial), func(s *Status) { s.DownSince = t0 })

	d := Decide(dialing, Input{Kind: InDialResult}, p, env(4*time.Minute))
	if d.Next.State != Conectada || !d.Next.GraceUntil.Equal(t0.Add(4*time.Minute+15*time.Second)) || d.Next.Reconnects24h(t0.Add(4*time.Minute)) != 1 {
		t.Fatalf("sucesso: %+v", d.Next)
	}
	if len(d.Notices) != 1 || d.Notices[0].Text != "VPN Matriz voltou (fora do ar por 4 min)" {
		t.Fatalf("aviso de volta: %+v", d.Notices)
	}

	d = Decide(dialing, Input{Kind: InDialResult, DialErr: dialErr(691), Fingerprint: "fp1"}, p, env(0))
	if d.Next.State != CredencialInvalida || d.Next.BlockedFP != "fp1" || d.Next.NextTick != (time.Time{}) {
		t.Fatalf("691: %+v", d.Next)
	}
	if k := noticeKinds(d); len(k) != 1 || k[0] != NoticeCredential || !strings.Contains(d.Notices[0].Text, `credential set "Matriz"`) {
		t.Fatalf("aviso 691: %+v", d.Notices)
	}

	d = Decide(dialing, Input{Kind: InDialResult, DialErr: dialErr(623)}, p, env(0))
	if d.Next.State != ErroConfig || !strings.Contains(d.Notices[0].Text, "Add-VpnConnection -AllUserConnection") {
		t.Fatalf("623: %+v", d)
	}

	d = Decide(dialing, Input{Kind: InDialResult, DialErr: dialErr(756)}, p, env(0))
	if d.Next.State != Reconectando || d.Next.Attempt != 0 || !d.Next.NextTick.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("756: %+v", d.Next)
	}
}

func TestTransientBackoffGrows(t *testing.T) {
	p := params()
	s := st(Reconectando, withOp(OpDial))
	var waits []time.Duration
	for i := 0; i < 6; i++ {
		d := Decide(s, Input{Kind: InDialResult, DialErr: dialErr(809)}, p, env(0))
		waits = append(waits, d.Next.NextAttempt.Sub(t0))
		if d.Next.State != Reconectando || !d.Next.NextTick.Equal(d.Next.NextAttempt) {
			t.Fatalf("tentativa %d: %+v", i, d.Next)
		}
		s = d.Next
		s.Op = OpDial
	}
	want := []time.Duration{30, 60, 120, 240, 300, 300}
	for i := range want {
		if waits[i] != want[i]*time.Second {
			t.Fatalf("esperas %v", waits)
		}
	}
}

func TestPowerResumeResetsBackoff(t *testing.T) {
	s := st(Reconectando, func(s *Status) { s.Attempt = 4; s.NextAttempt = t0.Add(5 * time.Minute) })
	d := Decide(s, Input{Kind: InPowerResume}, params(), env(0))
	if d.Next.Attempt != 0 || !d.Next.NextAttempt.IsZero() || !d.Next.NextTick.Equal(t0.Add(5*time.Second)) {
		t.Fatalf("%+v", d.Next)
	}
}

func TestWakeAggregatesWhileBusy(t *testing.T) {
	if d := Decide(st(Conectada), Input{Kind: InWake}, params(), env(0)); d.Action != OpProbeLink {
		t.Fatal("despertar ocioso verifica")
	}
	if d := Decide(st(Conectada, withOp(OpProbeReach)), Input{Kind: InWake}, params(), env(0)); d.Action != OpNone {
		t.Fatal("despertar durante operação se agrega a ela")
	}
}

func TestOneDownNoticePerOutage(t *testing.T) {
	p := params()
	s := st(Conectada, withOp(OpProbeLink))
	d := Decide(s, Input{Kind: InLinkResult, Network: true}, p, env(0))
	total := len(d.Notices)
	s = d.Next
	for i := 0; i < 3; i++ { // falhas seguidas: sem novo "caiu"
		d = Decide(s, Input{Kind: InDialResult, DialErr: dialErr(809)}, p, env(0))
		s = d.Next
		s.Op = OpProbeLink
		d = Decide(s, Input{Kind: InLinkResult, Network: true}, p, env(time.Hour))
		total += len(d.Notices)
		s = d.Next
	}
	if total != 1 {
		t.Fatalf("avisos de queda = %d, quer 1", total)
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/features/monitor/domain/`  
Expected: FAIL — `undefined: Decide`, `undefined: Input`…

- [ ] **Step 3: Implementar**

Desvio consciente da assinatura do spec (`Decide(estado, observação, config, agora) → (novoEstado, ação, espera, eventos)`):
a "espera" vira `Status.NextTick` (o supervisor arma o temporizador a partir dele) e `Env` leva, além de `agora`, o aleatório do jitter,
para a função continuar pura.

Regras desta tarefa: enlace caído disca na hora (respeitando `NextAttempt` do backoff); sem rede física → `SemRede` sem gastar backoff;
N falhas de alcance → `OpHangupDial` (túnel zumbi); carência após discar; erro transitório → backoff ±20 % do intervalo até o teto;
691 & cia → `CredencialInvalida`; 623 & cia → `ErroConfig` com a dica `Add-VpnConnection -AllUserConnection`; 756 → aguarda sem gastar
backoff; retomada de energia zera o backoff e verifica em 5 s; "caiu" uma vez por queda (só a partir de `Conectada`/`Degradada`) e
"voltou (fora do ar por X)".

`internal/features/monitor/domain/decide.go`:

```go
package domain

import (
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// InputKind é o tipo de entrada do ator.
type InputKind int

const (
	InTick              InputKind = iota // temporizador do próximo ciclo
	InWake                               // desconexão RAS ou mudança de rede (já agregadas)
	InPowerResume                        // retomada de energia / salto de relógio
	InLinkResult                         // resultado de OpProbeLink
	InReachResult                        // resultado de OpProbeReach
	InDialResult                         // resultado de OpDial/OpHangupDial
	InCheckNow                           // comando: verificar agora
	InReconnect                          // comando: reconectar agora
	InPause                              // comando: pausar
	InResume                             // comando: retomar
	InCredentialChanged                  // o cofre ou a credencial do Windows mudou
)

// Input é uma entrada do ator. Só os campos do tipo em questão importam.
type Input struct {
	Kind InputKind
	// InLinkResult
	LinkUp  bool
	Network bool
	// InReachResult
	ReachOK bool
	RTT     time.Duration
	// InDialResult (nil = conectou)
	DialErr *DialError
	// InPause: zero = indefinida
	PauseUntil time.Time
	// Impressão digital atual da credencial (InReconnect, InResume,
	// InCredentialChanged, InDialResult).
	Fingerprint string
}

// Env traz o que é externo à função: o instante e um aleatório em [0,1).
type Env struct {
	Now  time.Time
	Rand float64
}

// ReplyCode é a resposta a um comando.
type ReplyCode string

const (
	ReplyOK                  ReplyCode = ""
	ReplyPaused              ReplyCode = "paused"
	ReplyAlreadyReconnecting ReplyCode = "already_reconnecting"
	ReplyCredentialRejected  ReplyCode = "credential_rejected"
	ReplyDisabled            ReplyCode = "disabled"
)

// Reply é a resposta a um comando, com mensagem em português.
type Reply struct {
	Code    ReplyCode
	Message string
}

// Decision é a saída de Decide.
type Decision struct {
	Next Status
	// Action a executar agora (OpNone = nenhuma).
	Action Op
	// Cancel pede para cancelar a operação em andamento antes de Action.
	Cancel bool
	// Manual põe a discagem na frente da fila global.
	Manual bool
	// Reply só existe para comandos.
	Reply   *Reply
	Notices []Notice
}

// Jitter do backoff (§4.5).
const backoffJitter = 0.2

// resumeDelay é a espera após retomada de energia (§4.6).
const resumeDelay = 5 * time.Second

// Decide é a política inteira: dado o estado, a entrada, a config e o
// ambiente, devolve o novo estado e o que fazer.
func Decide(s Status, in Input, p Params, env Env) Decision {
	d := Decision{Next: s}
	switch in.Kind {
	case InTick:
		onTick(&d, p, env)
	case InWake:
		onWake(&d)
	case InPowerResume:
		onPowerResume(&d, env)
	case InLinkResult:
		onLink(&d, in, p, env)
	case InReachResult:
		onReach(&d, in, p, env)
	case InDialResult:
		onDial(&d, in, p, env)
	}
	return d
}

func (d *Decision) set(st State, now time.Time) {
	if d.Next.State != st {
		d.Next.State = st
		d.Next.Since = now
	}
}

func (d *Decision) start(op Op) {
	d.Action = op
	if op == OpHangupDial {
		op = OpDial
	}
	d.Next.Op = op
}

// goDown entra num estado "fora do ar" e avisa a queda uma vez.
func (d *Decision) goDown(st State, p Params, now time.Time) {
	prev := d.Next.State
	if (prev == Conectada || prev == Degradada) && d.Next.DownSince.IsZero() {
		d.Next.DownSince = now
		d.Notices = append(d.Notices, noticeDown(p.Name))
	}
	d.set(st, now)
}

// goUp entra em Conectada e avisa a volta se houve queda.
func (d *Decision) goUp(p Params, now time.Time) {
	if !d.Next.DownSince.IsZero() {
		d.Notices = append(d.Notices, noticeUp(p.Name, now.Sub(d.Next.DownSince)))
		d.Next.DownSince = time.Time{}
	}
	d.Next.Failures = 0
	d.set(Conectada, now)
}

func isBlocked(st State) bool { return st == CredencialInvalida || st == ErroConfig }

// idle diz se o estado aceita ciclos automáticos.
func idle(s Status) bool {
	return s.Op == OpNone && s.State != Pausada && s.State != Desativada && !isBlocked(s.State)
}

func onTick(d *Decision, p Params, env Env) {
	s := d.Next
	switch {
	case s.State == Pausada:
		if !s.PausedIndefinite && !env.Now.Before(s.PausedUntil) {
			resume(d, "", p, env)
		}
	case idle(s):
		d.Next.NextTick = time.Time{}
		d.start(OpProbeLink)
	}
}

func onWake(d *Decision) {
	if idle(d.Next) {
		d.Next.NextTick = time.Time{}
		d.start(OpProbeLink)
	}
}

func onPowerResume(d *Decision, env Env) {
	d.Next.Attempt = 0
	d.Next.NextAttempt = time.Time{}
	if idle(d.Next) {
		d.Next.NextTick = env.Now.Add(resumeDelay)
	}
}

func onLink(d *Decision, in Input, p Params, env Env) {
	now := env.Now
	d.Next.Op = OpNone
	d.Next.LastCheck = now
	s := d.Next
	if s.State == Pausada || s.State == Desativada {
		return // resultado atrasado de um ciclo que a pausa venceu
	}
	if isBlocked(s.State) {
		// Bloqueado só verifica (checkNow): se alguém conectou, saiu do bloqueio.
		if in.LinkUp {
			d.Next.Blocked, d.Next.LastErr = "", nil
			d.goUp(p, now)
			d.Next.NextTick = now.Add(p.Interval)
		}
		return
	}
	switch {
	case !in.Network:
		d.goDown(SemRede, p, now)
		d.Next.NextTick = now.Add(p.Interval)
	case !in.LinkUp:
		if now.Before(s.NextAttempt) {
			if s.State != Reconectando {
				d.goDown(Desconectada, p, now)
			}
			d.Next.NextTick = s.NextAttempt
			return
		}
		d.goDown(Reconectando, p, now)
		d.Next.NextTick = time.Time{}
		d.start(OpDial)
	case p.CheckKind == config.CheckLink || now.Before(s.GraceUntil):
		d.Next.LastRTT = 0
		d.goUp(p, now)
		d.Next.NextTick = now.Add(p.Interval)
	default:
		d.start(OpProbeReach)
	}
}

func onReach(d *Decision, in Input, p Params, env Env) {
	now := env.Now
	d.Next.Op = OpNone
	d.Next.LastCheck = now
	if !idle(d.Next) {
		return
	}
	if in.ReachOK {
		d.Next.LastRTT = in.RTT
		d.Next.Attempt = 0
		d.goUp(p, now)
		d.Next.NextTick = now.Add(p.Interval)
		return
	}
	d.Next.Failures++
	if d.Next.Failures >= p.Failures {
		// Túnel zumbi: enlace de pé, alvo mudo por N verificações.
		d.Next.Failures = 0
		d.goDown(Reconectando, p, now)
		d.Next.NextTick = time.Time{}
		d.start(OpHangupDial)
		return
	}
	if d.Next.State != Degradada {
		d.set(Degradada, now)
	}
	d.Next.NextTick = now.Add(p.Interval)
}

func onDial(d *Decision, in Input, p Params, env Env) {
	now := env.Now
	d.Next.Op = OpNone
	if d.Next.State == Pausada || d.Next.State == Desativada {
		return
	}
	e := in.DialErr
	if e == nil {
		d.Next.Attempt = 0
		d.Next.NextAttempt = time.Time{}
		d.Next.GraceUntil = now.Add(p.Grace)
		d.Next.LastErr = nil
		d.Next.Blocked = ""
		d.Next.LastRTT = 0
		d.Next.Reconnects = appendRecent(d.Next.Reconnects, now)
		d.goUp(p, now)
		d.Next.NextTick = now.Add(p.Interval)
		return
	}
	d.Next.LastErr = e
	switch e.Class {
	case ras.ClassCredencial:
		d.goDown(CredencialInvalida, p, now)
		d.Next.Blocked = CredencialInvalida
		d.Next.BlockedFP = in.Fingerprint
		d.Next.RejectedAt = now
		d.Next.NextTick = time.Time{}
		d.Notices = append(d.Notices, noticeCredential(p.Name))
	case ras.ClassConfiguracao:
		d.goDown(ErroConfig, p, now)
		d.Next.Blocked = ErroConfig
		d.Next.NextTick = time.Time{}
		d.Notices = append(d.Notices, noticeConfig(p.Name, ConfigErrorText(e, p.Entry)))
	case ras.ClassJaDiscando:
		// Outro processo está discando: reavalia o enlace no próximo ciclo.
		d.goDown(Reconectando, p, now)
		d.Next.NextTick = now.Add(p.Interval)
	default:
		d.Next.Attempt++
		wait := shared.Backoff{Base: p.Interval, Max: p.MaxBackoff, Jitter: backoffJitter}.Delay(d.Next.Attempt, env.Rand)
		d.goDown(Reconectando, p, now)
		d.Next.NextAttempt = now.Add(wait)
		d.Next.NextTick = d.Next.NextAttempt
	}
}

func appendRecent(ts []time.Time, now time.Time) []time.Time {
	out := make([]time.Time, 0, len(ts)+1)
	for _, t := range ts {
		if now.Sub(t) < 24*time.Hour {
			out = append(out, t)
		}
	}
	return append(out, now)
}

// resume sai da pausa: volta ao bloqueio anterior se a causa não mudou,
// senão recomeça em Desconhecido verificando já.
func resume(d *Decision, fp string, p Params, env Env) {
	now := env.Now
	d.Next.PausedUntil, d.Next.PausedIndefinite = time.Time{}, false
	b := d.Next.Blocked
	if b == CredencialInvalida && fp != "" && fp != d.Next.BlockedFP {
		b = ""
	}
	if b != "" {
		d.set(b, now)
		d.Next.NextTick = time.Time{}
		return
	}
	d.Next.Blocked = ""
	d.set(Desconhecido, now)
	d.Next.NextTick = time.Time{}
	d.start(OpProbeLink)
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race ./internal/features/monitor/domain/`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/features/monitor/domain && git commit -m "feat(monitor): política de transição pura para o ciclo automático"
```

---

### Task 14: monitor/domain: Decide — comandos (checkNow, reconnect, pause, resume, credencial)

**Files:**
- Create: `internal/features/monitor/domain/commands.go`
- Modify: `internal/features/monitor/domain/decide.go` (switch de `Decide`)
- Test: `internal/features/monitor/domain/commands_test.go`
- Test: `internal/features/monitor/domain/invariants_test.go`

**Interfaces:**
- Consumes: `domain.Decide`, `Decision`, `Input`, `resume` (função interna de decide.go), `isBlocked`, `(*Decision).set/start` (tarefa 13).
- Produces: Comportamento de `Decide` para `InCheckNow`, `InReconnect`, `InPause`, `InResume`, `InCredentialChanged`; `domain.ManualRetryWindow = 15 * time.Minute`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/monitor/domain/commands_test.go`:

```go
package domain

import (
	"strings"
	"testing"
	"time"
)

func TestCheckNow(t *testing.T) {
	cases := []struct {
		from   Status
		reply  ReplyCode
		action Op
	}{
		{st(Conectada), ReplyOK, OpProbeLink},
		{st(CredencialInvalida), ReplyOK, OpProbeLink},
		{st(Conectada, withOp(OpProbeReach)), ReplyOK, OpNone},
		{st(Pausada), ReplyPaused, OpNone},
		{st(Desativada), ReplyDisabled, OpNone},
	}
	for _, c := range cases {
		d := Decide(c.from, Input{Kind: InCheckNow}, params(), env(0))
		if d.Reply == nil || d.Reply.Code != c.reply || d.Action != c.action {
			t.Errorf("%s/%v: %+v", c.from.State, c.from.Op, d)
		}
	}
}

func TestCheckNowInBlockedStateNeverDials(t *testing.T) {
	s := st(CredencialInvalida, withOp(OpProbeLink), func(s *Status) { s.Blocked = CredencialInvalida })
	d := Decide(s, Input{Kind: InLinkResult, Network: true, LinkUp: false}, params(), env(0))
	if d.Action != OpNone || d.Next.State != CredencialInvalida {
		t.Fatalf("bloqueado não disca: %+v", d)
	}
	d = Decide(s, Input{Kind: InLinkResult, Network: true, LinkUp: true}, params(), env(0))
	if d.Next.State != Conectada || d.Next.Blocked != "" {
		t.Fatalf("conectada por fora sai do bloqueio: %+v", d.Next)
	}
}

func TestReconnect(t *testing.T) {
	d := Decide(st(Conectada), Input{Kind: InReconnect}, params(), env(0))
	if d.Action != OpHangupDial || !d.Manual || d.Next.State != Reconectando || d.Reply.Code != ReplyOK || len(d.Notices) != 0 {
		t.Fatalf("manual: %+v", d)
	}
	d = Decide(st(Conectada, withOp(OpProbeReach)), Input{Kind: InReconnect}, params(), env(0))
	if !d.Cancel || d.Action != OpHangupDial {
		t.Fatalf("cancela a verificação em curso: %+v", d)
	}
	if d := Decide(st(Reconectando, withOp(OpDial)), Input{Kind: InReconnect}, params(), env(0)); d.Reply.Code != ReplyAlreadyReconnecting || d.Action != OpNone {
		t.Fatalf("já discando: %+v", d)
	}
	paused := st(Pausada, func(s *Status) { s.PausedIndefinite = true })
	if d := Decide(paused, Input{Kind: InReconnect}, params(), env(0)); d.Reply.Code != ReplyPaused || d.Next.State != Pausada {
		t.Fatalf("pausada recusa sem mudar estado: %+v", d)
	}
	if d := Decide(st(ErroConfig, func(s *Status) { s.Blocked = ErroConfig }), Input{Kind: InReconnect}, params(), env(0)); d.Action != OpHangupDial {
		t.Fatalf("ErroConfig faz uma tentativa: %+v", d)
	}
}

func TestReconnectRejectedCredentialRateLimited(t *testing.T) {
	rejected := st(CredencialInvalida, func(s *Status) {
		s.Blocked, s.BlockedFP, s.RejectedAt = CredencialInvalida, "fp1", t0
	})
	d := Decide(rejected, Input{Kind: InReconnect, Fingerprint: "fp1"}, params(), env(time.Minute))
	if d.Reply.Code != ReplyCredentialRejected || d.Action != OpNone || !strings.Contains(d.Reply.Message, "14 min") {
		t.Fatalf("mesma credencial em 1 min: %+v", d.Reply)
	}
	d = Decide(rejected, Input{Kind: InReconnect, Fingerprint: "fp2"}, params(), env(time.Minute))
	if d.Action != OpHangupDial {
		t.Fatal("credencial nova permite tentar")
	}
	d = Decide(rejected, Input{Kind: InReconnect, Fingerprint: "fp1"}, params(), env(15*time.Minute))
	if d.Action != OpHangupDial || !d.Next.LastManualTry.Equal(t0.Add(15*time.Minute)) {
		t.Fatal("após 15 min permite uma tentativa")
	}
	// A tentativa manual falha de novo: o próximo clique espera mais 15 min.
	s := d.Next
	d = Decide(s, Input{Kind: InDialResult, DialErr: dialErr(691), Fingerprint: "fp1"}, params(), env(16*time.Minute))
	d = Decide(d.Next, Input{Kind: InReconnect, Fingerprint: "fp1"}, params(), env(20*time.Minute))
	if d.Reply.Code != ReplyCredentialRejected {
		t.Fatalf("cliques repetidos não podem discar: %+v", d.Reply)
	}
}

func TestPauseWinsOverRunningCycle(t *testing.T) {
	until := t0.Add(15 * time.Minute)
	d := Decide(st(Reconectando, withOp(OpDial)), Input{Kind: InPause, PauseUntil: until}, params(), env(0))
	if !d.Cancel || d.Next.State != Pausada || d.Next.Op != OpNone || !d.Next.NextTick.Equal(until) {
		t.Fatalf("%+v", d)
	}
	late := Decide(d.Next, Input{Kind: InDialResult}, params(), env(time.Second))
	if late.Next.State != Pausada {
		t.Fatal("resultado atrasado não tira da pausa")
	}
	d = Decide(st(Conectada), Input{Kind: InPause}, params(), env(0))
	if !d.Next.PausedIndefinite || !d.Next.NextTick.IsZero() {
		t.Fatalf("pausa indefinida: %+v", d.Next)
	}
}

func TestPauseExpiresOnTick(t *testing.T) {
	until := t0.Add(time.Hour)
	s := Decide(st(Conectada), Input{Kind: InPause, PauseUntil: until}, params(), env(0)).Next
	if d := Decide(s, Input{Kind: InTick}, params(), env(59*time.Minute)); d.Next.State != Pausada {
		t.Fatal("antes do prazo continua pausada")
	}
	d := Decide(s, Input{Kind: InTick}, params(), env(time.Hour))
	if d.Next.State != Desconhecido || d.Action != OpProbeLink {
		t.Fatalf("pausa expirada: %+v", d)
	}
}

func TestResumeReturnsToBlockedUnlessCauseChanged(t *testing.T) {
	cred := st(CredencialInvalida, func(s *Status) { s.Blocked, s.BlockedFP = CredencialInvalida, "fp1" })
	paused := Decide(cred, Input{Kind: InPause}, params(), env(0)).Next
	d := Decide(paused, Input{Kind: InResume, Fingerprint: "fp1"}, params(), env(time.Minute))
	if d.Next.State != CredencialInvalida || d.Action != OpNone {
		t.Fatalf("mesma credencial volta ao bloqueio: %+v", d.Next)
	}
	d = Decide(paused, Input{Kind: InResume, Fingerprint: "fp2"}, params(), env(time.Minute))
	if d.Next.State != Desconhecido || d.Action != OpProbeLink {
		t.Fatalf("credencial nova verifica: %+v", d.Next)
	}
	cfg := Decide(st(ErroConfig), Input{Kind: InPause}, params(), env(0)).Next
	if d := Decide(cfg, Input{Kind: InResume}, params(), env(0)); d.Next.State != ErroConfig {
		t.Fatalf("ErroConfig volta ao bloqueio: %+v", d.Next)
	}
	if d := Decide(st(Conectada), Input{Kind: InResume}, params(), env(0)); d.Reply.Code != ReplyOK || d.Next.State != Conectada {
		t.Fatal("retomar sem pausa é no-op")
	}
}

func TestCredentialChanged(t *testing.T) {
	cred := st(CredencialInvalida, func(s *Status) { s.Blocked, s.BlockedFP = CredencialInvalida, "fp1" })
	if d := Decide(cred, Input{Kind: InCredentialChanged, Fingerprint: "fp1"}, params(), env(0)); d.Action != OpNone {
		t.Fatal("mesma impressão: nada muda")
	}
	d := Decide(cred, Input{Kind: InCredentialChanged, Fingerprint: "fp2"}, params(), env(0))
	if d.Next.State != Desconhecido || d.Action != OpProbeLink {
		t.Fatalf("credencial nova sai do bloqueio e verifica: %+v", d)
	}
	paused := Decide(cred, Input{Kind: InPause}, params(), env(0)).Next
	d = Decide(paused, Input{Kind: InCredentialChanged, Fingerprint: "fp2"}, params(), env(0))
	if d.Next.State != Pausada || d.Next.Blocked != "" {
		t.Fatalf("em pausa só esquece o bloqueio: %+v", d.Next)
	}
	if d := Decide(st(Conectada), Input{Kind: InCredentialChanged, Fingerprint: "x"}, params(), env(0)); d.Action != OpNone {
		t.Fatal("fora do bloqueio é ignorado")
	}
}

func TestDisabledIgnoresEverything(t *testing.T) {
	s := st(Desativada)
	for _, k := range []InputKind{InTick, InWake, InPowerResume, InCheckNow, InReconnect, InPause, InResume} {
		d := Decide(s, Input{Kind: k}, params(), env(0))
		if d.Action != OpNone || d.Next.State != Desativada {
			t.Errorf("entrada %v mudou a VPN desativada: %+v", k, d)
		}
	}
}
```

`internal/features/monitor/domain/invariants_test.go`:

```go
package domain

import (
	"fmt"
	"testing"
	"time"
)

// Tabela estados × entradas: para cada estado de partida (com e sem
// operação em andamento) e cada entrada possível (com variações), confere
// invariantes que nenhum caminho pode quebrar.
func TestInvariantsStatesByInputs(t *testing.T) {
	states := []State{Desconhecido, Conectada, Degradada, Reconectando, Desconectada, CredencialInvalida, ErroConfig, Pausada, SemRede, Desativada}
	ops := []Op{OpNone, OpProbeLink, OpProbeReach, OpDial}
	inputs := []Input{
		{Kind: InTick}, {Kind: InWake}, {Kind: InPowerResume},
		{Kind: InLinkResult, Network: true, LinkUp: true}, {Kind: InLinkResult, Network: true}, {Kind: InLinkResult},
		{Kind: InReachResult, ReachOK: true}, {Kind: InReachResult},
		{Kind: InDialResult}, {Kind: InDialResult, DialErr: dialErr(809)}, {Kind: InDialResult, DialErr: dialErr(691)},
		{Kind: InDialResult, DialErr: dialErr(623)}, {Kind: InDialResult, DialErr: dialErr(756)},
		{Kind: InCheckNow}, {Kind: InReconnect, Fingerprint: "fp1"}, {Kind: InReconnect, Fingerprint: "fp2"},
		{Kind: InPause}, {Kind: InPause, PauseUntil: t0.Add(time.Hour)},
		{Kind: InResume, Fingerprint: "fp1"}, {Kind: InResume, Fingerprint: "fp2"},
		{Kind: InCredentialChanged, Fingerprint: "fp1"}, {Kind: InCredentialChanged, Fingerprint: "fp2"},
	}
	p := params()
	for _, from := range states {
		for _, op := range ops {
			for _, in := range inputs {
				s := Status{State: from, Since: t0, Op: op, Attempt: 2, NextAttempt: t0.Add(time.Minute),
					BlockedFP: "fp1", RejectedAt: t0, PausedUntil: t0.Add(2 * time.Hour)}
				if isBlocked(from) {
					s.Blocked = from
				}
				name := fmt.Sprintf("%s/op%d/in%d", from, op, in.Kind)
				d := Decide(s, in, p, env(time.Second))
				dials := d.Action == OpDial || d.Action == OpHangupDial

				// Desativada nunca age.
				if from == Desativada && (d.Action != OpNone || d.Next.State != Desativada) {
					t.Errorf("%s: desativada agiu: %+v", name, d)
				}
				// Pausada só sai da pausa por resume ou pelo prazo; parada, não age.
				if d.Next.State == Pausada && d.Action != OpNone {
					t.Errorf("%s: pausada com ação %v", name, d.Action)
				}
				if from == Pausada && in.Kind != InResume && in.Kind != InTick && d.Next.State != Pausada {
					t.Errorf("%s: saiu da pausa sem resume", name)
				}
				// Bloqueado nunca disca sem pedido manual.
				if isBlocked(from) && dials && in.Kind != InReconnect {
					t.Errorf("%s: bloqueado discou sozinho", name)
				}
				// Credencial rejeitada: reconexão manual com a mesma credencial
				// dentro de 15 min nunca disca.
				if from == CredencialInvalida && in.Kind == InReconnect && in.Fingerprint == "fp1" && dials {
					t.Errorf("%s: repetiu credencial rejeitada", name)
				}
				// SemRede nunca gasta nem mexe no backoff.
				if in.Kind == InLinkResult && !in.Network &&
					(d.Next.Attempt != s.Attempt || !d.Next.NextAttempt.Equal(s.NextAttempt)) {
					t.Errorf("%s: sem rede alterou o backoff: %+v", name, d.Next)
				}
				// Nunca discar com outra discagem em andamento.
				if op == OpDial && dials {
					t.Errorf("%s: discagem dupla", name)
				}
				// Op em andamento coerente com a ação pedida.
				if d.Action == OpHangupDial && d.Next.Op != OpDial || d.Action != OpNone && d.Action != OpHangupDial && d.Next.Op != d.Action {
					t.Errorf("%s: Op %v não reflete a ação %v", name, d.Next.Op, d.Action)
				}
				// Comandos sempre respondem; entradas automáticas nunca.
				isCmd := in.Kind >= InCheckNow && in.Kind <= InResume
				if isCmd != (d.Reply != nil) {
					t.Errorf("%s: resposta %v para comando=%v", name, d.Reply, isCmd)
				}
			}
		}
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/features/monitor/domain/`  
Expected: FAIL — `undefined: ManualRetryWindow` (e, sem a edição, os comandos não respondem).

- [ ] **Step 3: Implementar**

§4.7: a pausa cancela a operação em curso (`Decision.Cancel`) e guarda o bloqueio (`Blocked`); `reconnect` em pausa
responde `paused` sem mudar o estado; com discagem em curso, `already_reconnecting`; em `ErroConfig`, uma tentativa; em
`CredencialInvalida`, só se a impressão digital mudou ou se passaram 15 min desde a última tentativa (manual ou a rejeição automática,
o que for mais recente) — senão `credential_rejected` com "tente novamente em N min". `resume` volta ao bloqueio anterior se a causa não
mudou. Comandos em `Desativada` respondem `disabled`.

`invariants_test.go` cruza todos os estados × operações em andamento × entradas (com variações) e confere: `Desativada` nunca age;
`Pausada` nunca age e só sai por `resume`/prazo; bloqueados nunca discam sem `reconnect`; credencial rejeitada não é repetida em 15 min;
`SemRede` não mexe em `Attempt`/`NextAttempt`; nunca há discagem dupla; `Op` reflete a ação; comandos sempre respondem e entradas
automáticas nunca.

`internal/features/monitor/domain/commands.go`:

```go
package domain

import (
	"fmt"
	"math"
	"time"
)

// ManualRetryWindow é o intervalo mínimo entre tentativas manuais com a
// mesma credencial já rejeitada (§4.7): impede bloquear a conta no AD.
const ManualRetryWindow = 15 * time.Minute

var ok = &Reply{Code: ReplyOK}

func onCheckNow(d *Decision) {
	s := d.Next
	switch {
	case s.State == Desativada:
		d.Reply = &Reply{ReplyDisabled, "VPN desativada"}
	case s.State == Pausada:
		d.Reply = &Reply{ReplyPaused, "VPN pausada"}
	case s.Op != OpNone:
		d.Reply = ok // a verificação/discagem em curso já responde
	default:
		// Inclui os estados bloqueados: verifica o enlace, sem discar.
		d.Next.NextTick = time.Time{}
		d.start(OpProbeLink)
		d.Reply = ok
	}
}

func onReconnect(d *Decision, in Input, p Params, env Env) {
	s, now := d.Next, env.Now
	switch {
	case s.State == Desativada:
		d.Reply = &Reply{ReplyDisabled, "VPN desativada"}
		return
	case s.State == Pausada:
		d.Reply = &Reply{ReplyPaused, "VPN pausada; retome antes de reconectar"}
		return
	case s.Op == OpDial:
		d.Reply = &Reply{ReplyAlreadyReconnecting, "já reconectando"}
		return
	case s.State == CredencialInvalida && in.Fingerprint == s.BlockedFP:
		ref := s.RejectedAt
		if s.LastManualTry.After(ref) {
			ref = s.LastManualTry
		}
		if wait := ref.Add(ManualRetryWindow).Sub(now); wait > 0 {
			mins := int(math.Ceil(wait.Minutes()))
			d.Reply = &Reply{ReplyCredentialRejected,
				fmt.Sprintf("credencial já rejeitada; tente novamente em %d min ou atualize a credencial", mins)}
			return
		}
	}
	if s.State == CredencialInvalida {
		d.Next.LastManualTry = now
	}
	d.Cancel = s.Op != OpNone
	d.Next.Blocked = ""
	d.Next.Failures = 0
	d.Next.NextTick = time.Time{}
	d.set(Reconectando, now)
	d.start(OpHangupDial)
	d.Manual = true
	d.Reply = ok
}

func onPause(d *Decision, in Input, env Env) {
	if d.Next.State == Desativada {
		d.Reply = &Reply{ReplyDisabled, "VPN desativada"}
		return
	}
	d.Cancel = d.Next.Op != OpNone
	d.Next.Op = OpNone
	if isBlocked(d.Next.State) {
		d.Next.Blocked = d.Next.State
	}
	d.Next.DownSince = time.Time{}
	d.Next.PausedUntil = in.PauseUntil
	d.Next.PausedIndefinite = in.PauseUntil.IsZero()
	d.Next.NextTick = in.PauseUntil
	d.set(Pausada, env.Now)
	d.Reply = ok
}

func onResume(d *Decision, in Input, p Params, env Env) {
	d.Reply = ok
	if d.Next.State != Pausada {
		return
	}
	resume(d, in.Fingerprint, p, env)
}

func onCredentialChanged(d *Decision, in Input, p Params, env Env) {
	s := d.Next
	if s.Blocked != CredencialInvalida && s.State != CredencialInvalida {
		return
	}
	if in.Fingerprint == s.BlockedFP {
		return
	}
	if s.State == Pausada {
		d.Next.Blocked = ""
		return
	}
	d.Next.Blocked = ""
	d.Next.LastErr = nil
	d.set(Desconhecido, env.Now)
	d.Next.NextTick = time.Time{}
	d.start(OpProbeLink)
}
```

Em `internal/features/monitor/domain/decide.go`, ligar os comandos no `switch` de `Decide`: trocar

```go
	case InDialResult:
		onDial(&d, in, p, env)
	}
```

por

```go
	case InDialResult:
		onDial(&d, in, p, env)
	case InCheckNow:
		onCheckNow(&d)
	case InReconnect:
		onReconnect(&d, in, p, env)
	case InPause:
		onPause(&d, in, env)
	case InResume:
		onResume(&d, in, p, env)
	case InCredentialChanged:
		onCredentialChanged(&d, in, p, env)
	}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race -cover ./internal/features/monitor/domain/`  
Expected: PASS, cobertura ≥ 90 %

- [ ] **Step 5: Commit**

```bash
git add internal/features/monitor/domain && git commit -m "feat(monitor): comandos manuais com limite de tentativas para credencial rejeitada"
```

---

### Task 15: monitor/adapters: verificações (ping, tcp, link), sonda de enlace e discador RAS

**Files:**
- Create: `internal/features/monitor/adapters/checkers.go`
- Create: `internal/features/monitor/adapters/link.go`
- Create: `internal/features/monitor/adapters/dialer.go`
- Test: `internal/features/monitor/adapters/adapters_test.go`

**Interfaces:**
- Consumes: `ras.Client`, `ras.Classify`, `ras.Error`, `ras.DialRequest`, `ras.Saved` (tarefas 7–8); `icmp.Pinger` (9); `netwatch.Watcher` (10); `domain.DialError` (12); `config.Check` (4); `shared.Clock`, `shared.Secret`; fakes.
- Produces: `adapters.ReachResult{OK; RTT; Err}`, `adapters.Checker` (`Check(ctx) ReachResult`), `adapters.DialFunc`, `adapters.NewChecker(config.Check, icmp.Pinger, DialFunc) Checker`;
  `adapters.LinkResult{Up, Network bool; Handle ras.Handle}`, `adapters.LinkProber{RAS; Net}` com `Probe(ctx, entry) (LinkResult, error)`, `adapters.FindActive(ras.Client, entry)`;
  `adapters.Credentials{User; Password shared.Secret; Saved *ras.Saved; Fingerprint; Source string}`;
  interface `adapters.CredentialResolver` (`Resolve(ctx, name, entry) (Credentials, error)`, `Fingerprint(ctx, name, entry) string`);
  `adapters.DialJob{Name, Entry string; Timeout time.Duration; HangupFirst bool}`, `adapters.DialOutcome{Err *domain.DialError; Fingerprint string; Cancelled bool}`;
  `adapters.Dialer{RAS; Creds CredentialResolver; Clock; PollInterval}` com `Dial(ctx, DialJob) DialOutcome`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/monitor/adapters/adapters_test.go`:

```go
package adapters

import (
	"context"
	"errors"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/fake"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

func TestPingChecker(t *testing.T) {
	p := fake.NewPinger()
	c := NewChecker(config.Check{Kind: config.CheckPing, Host: "10.0.0.1", TimeoutSeconds: 1}, p, nil)
	if r := c.Check(context.Background()); r.OK {
		t.Fatal("sem resposta")
	}
	p.SetReachable("10.0.0.1", true)
	if r := c.Check(context.Background()); !r.OK || r.RTT != 12*time.Millisecond {
		t.Fatalf("%+v", r)
	}
	p.SetError("10.0.0.1", errors.New("resolver"))
	if r := c.Check(context.Background()); r.OK || r.Err == nil {
		t.Fatalf("erro conta como falha: %+v", r)
	}
}

func TestPingCheckerAbandonsHungEcho(t *testing.T) {
	p := fake.NewPinger()
	p.SetHang(true) // IcmpSendEcho2 real ignora ctx
	defer p.SetHang(false)
	c := NewChecker(config.Check{Kind: config.CheckPing, Host: "10.0.0.1", TimeoutSeconds: 30}, p, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	start := time.Now()
	r := c.Check(ctx)
	if r.OK || r.Err == nil || time.Since(start) > time.Second {
		t.Fatalf("cancelamento deve abandonar o eco preso: %+v em %s", r, time.Since(start))
	}
}

func TestTCPChecker(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	c := NewChecker(config.Check{Kind: config.CheckTCP, Host: "127.0.0.1", Port: addr.Port, TimeoutSeconds: 1}, nil, nil)
	if r := c.Check(context.Background()); !r.OK {
		t.Fatalf("porta aberta: %+v", r)
	}
	ln.Close()
	if r := c.Check(context.Background()); r.OK {
		t.Fatal("porta fechada não é sucesso")
	}
}

func TestLinkProber(t *testing.T) {
	r := fake.NewRAS("VPN Matriz")
	n := fake.NewNet()
	p := LinkProber{RAS: r, Net: n}
	if res, err := p.Probe(context.Background(), "VPN Matriz"); err != nil || res.Up || !res.Network {
		t.Fatalf("caída com rede: %+v %v", res, err)
	}
	n.SetPhysical(false)
	if res, _ := p.Probe(context.Background(), "VPN Matriz"); res.Up || res.Network {
		t.Fatalf("sem rede: %+v", res)
	}
	r.SetActive("VPN Matriz")
	if res, _ := p.Probe(context.Background(), "vpn matriz"); !res.Up || !res.Network {
		t.Fatalf("ativa (nome sem diferenciar maiúsculas): %+v", res)
	}
	r.ActiveErr = errors.New("rasman parado")
	if _, err := p.Probe(context.Background(), "VPN Matriz"); err == nil {
		t.Fatal("erro do RAS deve propagar")
	}
}

type stubCreds struct {
	creds Credentials
	err   error
}

func (s stubCreds) Resolve(context.Context, string, string) (Credentials, error) {
	return s.creds, s.err
}
func (s stubCreds) Fingerprint(context.Context, string, string) string { return s.creds.Fingerprint }

func newDialer(r *fake.RAS, c stubCreds) *Dialer {
	return &Dialer{RAS: r, Creds: c, Clock: shared.RealClock{}, PollInterval: time.Millisecond}
}

func TestDialSuccessAndErrors(t *testing.T) {
	r := fake.NewRAS("VPN Matriz")
	d := newDialer(r, stubCreds{creds: Credentials{Fingerprint: "fp"}})
	job := DialJob{Name: "Matriz", Entry: "VPN Matriz", Timeout: time.Second}
	if out := d.Dial(context.Background(), job); out.Err != nil || out.Fingerprint != "fp" || !r.IsActive("VPN Matriz") {
		t.Fatalf("sucesso: %+v", out)
	}
	r.Drop("VPN Matriz")
	r.Script("VPN Matriz", fake.DialOutcome{Code: 691, Polls: 2})
	out := d.Dial(context.Background(), job)
	if out.Err == nil || out.Err.Class != ras.ClassCredencial || out.Err.Code != 691 || out.Err.Message == "" {
		t.Fatalf("691: %+v", out.Err)
	}
	r.Script("VPN Matriz", fake.DialOutcome{Immediate: true, Code: 623})
	if out := d.Dial(context.Background(), job); out.Err.Class != ras.ClassConfiguracao {
		t.Fatalf("623 imediato: %+v", out.Err)
	}
}

func TestDialTimeoutAndCancelHangUp(t *testing.T) {
	r := fake.NewRAS("VPN Matriz")
	d := newDialer(r, stubCreds{})
	r.Script("VPN Matriz", fake.DialOutcome{Polls: -1})
	out := d.Dial(context.Background(), DialJob{Name: "Matriz", Entry: "VPN Matriz", Timeout: 20 * time.Millisecond})
	if out.Err == nil || out.Err.Class != ras.ClassTransitorio || !slices.Contains(r.Calls(), "HangUp VPN Matriz") {
		t.Fatalf("timeout deve desligar: %+v %v", out.Err, r.Calls())
	}

	r2 := fake.NewRAS("VPN Matriz")
	r2.Script("VPN Matriz", fake.DialOutcome{Polls: -1})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	out = newDialer(r2, stubCreds{}).Dial(ctx, DialJob{Name: "Matriz", Entry: "VPN Matriz", Timeout: time.Minute})
	if !out.Cancelled || !slices.Contains(r2.Calls(), "HangUp VPN Matriz") {
		t.Fatalf("cancelamento deve desligar: %+v %v", out, r2.Calls())
	}
}

func TestDialHangupFirst(t *testing.T) {
	r := fake.NewRAS("VPN Matriz")
	r.SetActive("VPN Matriz")
	d := newDialer(r, stubCreds{})
	d.Dial(context.Background(), DialJob{Name: "Matriz", Entry: "VPN Matriz", Timeout: time.Second, HangupFirst: true})
	if calls := r.Calls(); len(calls) < 2 || calls[0] != "HangUp VPN Matriz" || calls[1] != "StartDial VPN Matriz" {
		t.Fatalf("zumbi: desliga e depois disca: %v", calls)
	}
}

func TestDialCredentialResolveError(t *testing.T) {
	r := fake.NewRAS()
	d := newDialer(r, stubCreds{err: &ras.Error{Op: "RasGetEntryDialParamsW", Code: 623}})
	out := d.Dial(context.Background(), DialJob{Name: "Matriz", Entry: "Nenhuma", Timeout: time.Second})
	if out.Err == nil || out.Err.Code != 623 || out.Err.Class != ras.ClassConfiguracao {
		t.Fatalf("%+v", out.Err)
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/features/monitor/adapters/`  
Expected: FAIL — `undefined: NewChecker`, `undefined: Dialer`…

- [ ] **Step 3: Implementar**

O `pingChecker` roda o eco numa goroutine e abandona o resultado quando o `ctx` termina: o `IcmpSendEcho2` real é síncrono e ignora
`ctx` (até 30 s), e sem isso uma pausa ou a parada do serviço ficariam presas nele. O discador acompanha a discagem assíncrona com `Status` a cada 250 ms; timeout ou cancelamento (pausa, parada) chamam
`HangUp` antes de voltar. Falha da sonda de rotas não impede discar (presume rede). Nomes de entrada são comparados sem diferenciar
maiúsculas, como o Windows faz.

`internal/features/monitor/adapters/checkers.go`:

```go
// Package adapters liga o domínio do monitor à plataforma: verificações de
// alcance (ping, tcp, link), sonda de enlace e discador RAS.
package adapters

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/icmp"
)

// ReachResult é o resultado de uma verificação de alcance. Err explica por
// que o teste nem pôde ser feito (conta como falha de alcance).
type ReachResult struct {
	OK  bool
	RTT time.Duration
	Err error
}

// Checker verifica o alcance do alvo da VPN.
type Checker interface {
	Check(ctx context.Context) ReachResult
}

// DialFunc abre uma conexão TCP (net.Dialer.DialContext; injetável em teste).
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// NewChecker escolhe o verificador pelo check.kind.
func NewChecker(c config.Check, pinger icmp.Pinger, dial DialFunc) Checker {
	timeout := time.Duration(c.TimeoutSeconds) * time.Second
	switch c.Kind {
	case config.CheckPing:
		return pingChecker{pinger: pinger, host: c.Host, timeout: timeout}
	case config.CheckTCP:
		if dial == nil {
			dial = (&net.Dialer{}).DialContext
		}
		return tcpChecker{dial: dial, addr: net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), timeout: timeout}
	}
	return linkChecker{}
}

type pingChecker struct {
	pinger  icmp.Pinger
	host    string
	timeout time.Duration
}

// Check não fica refém do IcmpSendEcho2, que é síncrono e ignora ctx
// (até 30 s): o eco roda numa goroutine e, se ctx terminar antes (pausa,
// parada do serviço), o resultado é abandonado. A goroutine termina sozinha
// quando o eco esgota o próprio prazo.
func (p pingChecker) Check(ctx context.Context) ReachResult {
	ctx, cancel := context.WithTimeout(ctx, p.timeout+time.Second)
	defer cancel()
	type result struct {
		r   icmp.Result
		err error
	}
	ch := make(chan result, 1)
	go func() {
		r, err := p.pinger.Ping(ctx, p.host, p.timeout)
		ch <- result{r, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			return ReachResult{Err: res.err}
		}
		return ReachResult{OK: res.r.OK, RTT: res.r.RTT}
	case <-ctx.Done():
		return ReachResult{Err: ctx.Err()}
	}
}

type tcpChecker struct {
	dial    DialFunc
	addr    string
	timeout time.Duration
}

func (t tcpChecker) Check(ctx context.Context) ReachResult {
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	start := time.Now()
	c, err := t.dial(ctx, "tcp", t.addr)
	if err != nil {
		return ReachResult{Err: err}
	}
	_ = c.Close()
	return ReachResult{OK: true, RTT: time.Since(start)}
}

// linkChecker: no tipo link o enlace basta; nunca é chamado pelo domínio,
// mas existe para o check único da CLI ter um resultado uniforme.
type linkChecker struct{}

func (linkChecker) Check(context.Context) ReachResult { return ReachResult{OK: true} }
```

`internal/features/monitor/adapters/link.go`:

```go
package adapters

import (
	"context"
	"strings"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/netwatch"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

// LinkResult é o resultado da sonda de enlace.
type LinkResult struct {
	Up      bool // a entrada RAS está conectada
	Network bool // há interface física com rota padrão
	Handle  ras.Handle
}

// LinkProber consulta o RAS e a rede.
type LinkProber struct {
	RAS ras.Client
	Net netwatch.Watcher
}

// FindActive procura a conexão ativa da entrada (o Windows não diferencia
// maiúsculas em nomes de entrada).
func FindActive(c ras.Client, entry string) (ras.ActiveConn, bool, error) {
	conns, err := c.Active()
	if err != nil {
		return ras.ActiveConn{}, false, err
	}
	for _, a := range conns {
		if strings.EqualFold(a.Entry, entry) {
			return a, true, nil
		}
	}
	return ras.ActiveConn{}, false, nil
}

// Probe diz se o enlace está de pé e se há rede física. Com o enlace de pé
// a rede é presumida. Falha ao consultar rotas não impede discar.
func (p LinkProber) Probe(_ context.Context, entry string) (LinkResult, error) {
	a, found, err := FindActive(p.RAS, entry)
	if err != nil {
		return LinkResult{}, err
	}
	if found {
		st, err := p.RAS.Status(a.Handle)
		if err != nil {
			return LinkResult{}, err
		}
		if st.State == ras.StateConnected {
			return LinkResult{Up: true, Network: true, Handle: a.Handle}, nil
		}
	}
	network := true
	if p.Net != nil {
		if ok, err := p.Net.HasPhysicalDefaultRoute(); err == nil {
			network = ok
		}
	}
	return LinkResult{Network: network}, nil
}
```

`internal/features/monitor/adapters/dialer.go`:

```go
package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// Credentials é a credencial resolvida para uma discagem.
type Credentials struct {
	User     string
	Password shared.Secret
	Saved    *ras.Saved // parâmetros salvos do Windows (marcador intacto)
	// Fingerprint identifica a credencial (arquivo do cofre ou marcador do
	// Windows) sem revelá-la; "" sem credencial.
	Fingerprint string
	Source      string // "cofre", "windows" ou "nenhuma"
}

// CredentialResolver resolve a credencial (implementado por features/credentials).
type CredentialResolver interface {
	Resolve(ctx context.Context, name, entry string) (Credentials, error)
	Fingerprint(ctx context.Context, name, entry string) string
}

// DialOutcome é o resultado de Dialer.Dial.
type DialOutcome struct {
	Err         *domain.DialError // nil = conectou
	Fingerprint string
	Cancelled   bool
}

// Dialer disca de forma assíncrona e acompanha com RasGetConnectStatus.
type Dialer struct {
	RAS          ras.Client
	Creds        CredentialResolver
	Clock        shared.Clock
	PollInterval time.Duration // padrão 250 ms (§4.4)
}

// DialJob é uma discagem pedida pelo supervisor.
type DialJob struct {
	Name, Entry string
	Timeout     time.Duration
	HangupFirst bool
}

func (d *Dialer) dialErr(code uint32) *domain.DialError {
	return &domain.DialError{Class: ras.Classify(code), Code: code, Message: d.RAS.ErrorText(code)}
}

// Dial executa a discagem inteira. Cancelar ctx (pausa, parada) ou estourar
// Timeout desliga com RasHangUp antes de voltar.
func (d *Dialer) Dial(ctx context.Context, job DialJob) DialOutcome {
	if job.HangupFirst {
		if a, found, err := FindActive(d.RAS, job.Entry); err == nil && found {
			_ = d.RAS.HangUp(a.Handle)
		}
	}
	creds, err := d.Creds.Resolve(ctx, job.Name, job.Entry)
	if err != nil {
		var re *ras.Error
		if errors.As(err, &re) {
			return DialOutcome{Err: d.dialErr(re.Code)}
		}
		return DialOutcome{Err: &domain.DialError{Class: ras.ClassTransitorio, Message: "resolvendo credencial: " + err.Error()}}
	}
	out := DialOutcome{Fingerprint: creds.Fingerprint}
	h, err := d.RAS.StartDial(ras.DialRequest{Entry: job.Entry, User: creds.User, Password: creds.Password, Saved: creds.Saved})
	creds.Password.Wipe()
	if err != nil {
		var re *ras.Error
		if errors.As(err, &re) {
			out.Err = d.dialErr(re.Code)
		} else {
			out.Err = &domain.DialError{Class: ras.ClassConfiguracao, Message: err.Error()}
		}
		return out
	}
	poll := d.PollInterval
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	deadline := d.Clock.NewTimer(job.Timeout)
	defer deadline.Stop()
	tick := d.Clock.NewTimer(poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = d.RAS.HangUp(h)
			out.Cancelled = true
			out.Err = &domain.DialError{Class: ras.ClassTransitorio, Message: "discagem cancelada"}
			return out
		case <-deadline.C():
			_ = d.RAS.HangUp(h)
			out.Err = &domain.DialError{Class: ras.ClassTransitorio,
				Message: fmt.Sprintf("tempo esgotado após %s", job.Timeout)}
			return out
		case <-tick.C():
			st, err := d.RAS.Status(h)
			if err != nil {
				_ = d.RAS.HangUp(h)
				out.Err = &domain.DialError{Class: ras.ClassTransitorio, Message: err.Error()}
				return out
			}
			switch st.State {
			case ras.StateConnected:
				return out
			case ras.StateDisconnected:
				_ = d.RAS.HangUp(h)
				out.Err = d.dialErr(st.Code)
				return out
			}
			tick.Reset(poll)
		}
	}
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race ./internal/features/monitor/adapters/`  
Expected: PASS

Run: `go vet ./... && GOOS=windows go vet ./...`  
Expected: sem saída

- [ ] **Step 5: Commit**

```bash
git add internal/features/monitor/adapters && git commit -m "feat(monitor): verificações ping/tcp/link, sonda de enlace e discador com timeout e hangup"
```

---

### Task 16: features/credentials: cofre DPAPI por VPN e resolução cofre → Windows → nenhuma

**Files:**
- Create: `internal/features/credentials/vault.go`
- Create: `internal/features/credentials/resolver.go`
- Test: `internal/features/credentials/credentials_test.go`

**Interfaces:**
- Consumes: `dpapi.Protector`, `fake.DPAPI` (9); `ras.Client`, `ras.Saved`, `fake.RAS` (8); `config.NameKey` (4); `shared.Secret`, `shared.WriteFile`.
- Produces: `credentials.FileID(name) string` (até 32 `[a-z0-9_-]` + `-` + 8 hex); `credentials.Vault{Dir string; DPAPI dpapi.Protector}` com
  `Set(name, user string, pw shared.Secret) error`, `Get(name) (user string, pw shared.Secret, ok bool, err error)`, `Clear(name) (removed bool, err error)`,
  `Has(name) bool`, `Fingerprint(name) string`, `DirFingerprint() string`;
  `credentials.Resolution{User; Password; Saved *ras.Saved; Fingerprint; Source}`, `credentials.Resolver{Vault; RAS}` com
  `Resolve(ctx, name, entry) (Resolution, error)` e `Fingerprint(ctx, name, entry) string`; `SourceVault`/`SourceWindows`/`SourceNone`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/credentials/credentials_test.go`:

```go
package credentials

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/fake"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

func TestFileIDIsSafe(t *testing.T) {
	cases := map[string]string{
		"Matriz":           "matriz-",
		"MATRIZ":           "matriz-",
		`..\..\Windows`:    "______windows-",
		"Filial São Paulo": "filial_s_o_paulo-",
	}
	for in, prefix := range cases {
		id := FileID(in)
		if !strings.HasPrefix(id, prefix) || strings.ContainsAny(id, `/\.:`) || len(id) != len(prefix)+8 {
			t.Errorf("FileID(%q) = %q", in, id)
		}
	}
	if FileID("Matriz") != FileID("matriz") {
		t.Error("nomes iguais sem maiúsculas devem dar o mesmo arquivo")
	}
	if FileID("a/b") == FileID("a_b") {
		t.Error("o hash distingue nomes com a mesma versão saneada")
	}
	if long := FileID(strings.Repeat("x", 64)); len(long) != 32+1+8 {
		t.Errorf("tamanho %d", len(long))
	}
}

func newVault(t *testing.T) Vault {
	return Vault{Dir: filepath.Join(t.TempDir(), "credentials"), DPAPI: fake.DPAPI{}}
}

func TestVaultSetGetClear(t *testing.T) {
	v := newVault(t)
	if _, _, ok, err := v.Get("Matriz"); ok || err != nil {
		t.Fatal("cofre vazio")
	}
	if err := v.Set("Matriz", "ana", shared.NewSecret("s3nha")); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(v.Dir, FileID("Matriz")+".bin"))
	if strings.Contains(string(raw), "s3nha") {
		t.Fatal("senha em claro no arquivo")
	}
	u, pw, ok, err := v.Get("MATRIZ")
	if err != nil || !ok || u != "ana" || pw.Reveal() != "s3nha" {
		t.Fatalf("%q %v %v", u, ok, err)
	}
	fp1 := v.Fingerprint("Matriz")
	_ = v.Set("Matriz", "ana", shared.NewSecret("nova"))
	if fp1 == "" || v.Fingerprint("Matriz") == fp1 {
		t.Fatal("impressão digital deve mudar ao regravar")
	}
	if removed, err := v.Clear("Matriz"); !removed || err != nil || v.Has("Matriz") {
		t.Fatal("Clear")
	}
	if removed, err := v.Clear("Matriz"); removed || err != nil {
		t.Fatal("Clear de ausente não é erro")
	}
	if err := v.Set("X", "", shared.NewSecret("x")); err == nil {
		t.Fatal("usuário vazio")
	}
	if err := v.Set("X", "ana", shared.NewSecret(strings.Repeat("é", 257))); err == nil || !strings.Contains(err.Error(), "até 256") {
		t.Fatalf("senha acima de PWLEN deve ser recusada na gravação: %v", err)
	}
	if err := v.Set("X", "ana", shared.NewSecret(strings.Repeat("é", 256))); err != nil {
		t.Fatalf("256 caracteres cabem: %v", err)
	}
}

func TestVaultCorruptBlob(t *testing.T) {
	v := newVault(t)
	_ = os.MkdirAll(v.Dir, 0o700)
	_ = os.WriteFile(filepath.Join(v.Dir, FileID("Matriz")+".bin"), []byte("lixo"), 0o600)
	if _, _, _, err := v.Get("Matriz"); err == nil {
		t.Fatal("blob inválido deve dar erro")
	}
}

func TestResolverOrder(t *testing.T) {
	r := fake.NewRAS("VPN Matriz")
	v := newVault(t)
	res := Resolver{Vault: v, RAS: r}
	ctx := context.Background()

	got, err := res.Resolve(ctx, "Matriz", "VPN Matriz")
	if err != nil || got.Source != SourceNone || got.Fingerprint != "" || got.Saved == nil {
		t.Fatalf("nenhuma: %+v %v", got, err)
	}
	r.SetSaved("VPN Matriz", "ana", "marcador")
	got, _ = res.Resolve(ctx, "Matriz", "VPN Matriz")
	if got.Source != SourceWindows || !strings.HasPrefix(got.Fingerprint, "windows:") || res.Fingerprint(ctx, "Matriz", "VPN Matriz") != got.Fingerprint {
		t.Fatalf("windows: %+v", got)
	}
	_ = v.Set("Matriz", "bia", shared.NewSecret("pw"))
	got, _ = res.Resolve(ctx, "Matriz", "VPN Matriz")
	if got.Source != SourceVault || got.User != "bia" || got.Password.Reveal() != "pw" || !strings.HasPrefix(got.Fingerprint, "cofre:") {
		t.Fatalf("cofre: %+v", got)
	}
	if res.Fingerprint(ctx, "Matriz", "VPN Matriz") != got.Fingerprint {
		t.Fatal("Fingerprint deve bater com Resolve")
	}
	if _, err := res.Resolve(ctx, "Outra", "Inexistente"); err == nil {
		t.Fatal("entrada inexistente deve dar erro 623")
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/features/credentials/`  
Expected: FAIL — `undefined: FileID`…

- [ ] **Step 3: Implementar**

A feature não importa `monitor` (§3.2): ela devolve `credentials.Resolution`, e a montagem (`cmd/vpnmon-svc`, tarefa 24)
adapta para `adapters.Credentials`. `Set` recusa usuário/senha acima de 256 unidades UTF-16 (UNLEN/PWLEN) com mensagem clara, em vez de a
discagem falhar depois (item 4 do Review Focus).

`internal/features/credentials/vault.go`:

```go
// Package credentials guarda usuário e senha por VPN no cofre
// (credentials\<id>.bin, DPAPI de máquina) e resolve a credencial de uma
// discagem: cofre → credencial salva no Windows → nenhuma (§5.4).
package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/dpapi"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// maxCredentialUnits é UNLEN/PWLEN do lmcons.h.
const maxCredentialUnits = 256

// entropy é a entropia fixa do app passada à DPAPI.
var entropy = []byte("VPNMonitor/v2/credenciais")

// FileID deriva o nome do arquivo do nome da VPN, sem permitir caminho:
// versão saneada (até 32 caracteres [a-z0-9_-]) + 8 hex do hash do nome
// sem diferenciar maiúsculas.
func FileID(name string) string {
	key := config.NameKey(name)
	var b strings.Builder
	for _, r := range key {
		if b.Len() >= 32 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	sum := sha256.Sum256([]byte(key))
	return b.String() + "-" + hex.EncodeToString(sum[:4])
}

// Vault é o cofre em disco.
type Vault struct {
	Dir   string
	DPAPI dpapi.Protector
}

type record struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

func (v Vault) path(name string) string { return filepath.Join(v.Dir, FileID(name)+".bin") }

// Set grava (ou substitui) a credencial da VPN de forma atômica.
func (v Vault) Set(name, user string, password shared.Secret) error {
	if user == "" {
		return errors.New("usuário vazio")
	}
	// Limites do RASDIALPARAMSW (UNLEN e PWLEN, em unidades UTF-16): acima
	// disso a discagem nem poderia ser montada.
	if n := len(utf16.Encode([]rune(user))); n > maxCredentialUnits {
		return fmt.Errorf("usuário com %d caracteres; o Windows aceita até %d", n, maxCredentialUnits)
	}
	if n := len(utf16.Encode([]rune(password.Reveal()))); n > maxCredentialUnits {
		return fmt.Errorf("senha com %d caracteres; o Windows aceita até %d", n, maxCredentialUnits)
	}
	plain, err := json.Marshal(record{User: user, Password: password.Reveal()})
	if err != nil {
		return err
	}
	defer clear(plain)
	blob, err := v.DPAPI.Protect(plain, entropy)
	if err != nil {
		return fmt.Errorf("protegendo com DPAPI: %w", err)
	}
	if err := os.MkdirAll(v.Dir, 0o700); err != nil {
		return err
	}
	return shared.WriteFile(v.path(name), blob, 0o600)
}

// Get lê a credencial. ok=false se não houver arquivo.
func (v Vault) Get(name string) (user string, password shared.Secret, ok bool, err error) {
	blob, err := os.ReadFile(v.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", shared.Secret{}, false, nil
	}
	if err != nil {
		return "", shared.Secret{}, false, err
	}
	plain, err := v.DPAPI.Unprotect(blob, entropy)
	if err != nil {
		return "", shared.Secret{}, false, fmt.Errorf("decifrando a credencial de %q: %w", name, err)
	}
	defer clear(plain)
	var r record
	if err := json.Unmarshal(plain, &r); err != nil {
		return "", shared.Secret{}, false, fmt.Errorf("credencial de %q corrompida", name)
	}
	return r.User, shared.NewSecret(r.Password), true, nil
}

// Clear apaga a credencial; removed=false se não havia.
func (v Vault) Clear(name string) (removed bool, err error) {
	err = os.Remove(v.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Has diz se a VPN tem credencial no cofre.
func (v Vault) Has(name string) bool {
	_, err := os.Stat(v.path(name))
	return err == nil
}

// Fingerprint é o hash do arquivo da VPN ("" se não houver). Muda quando a
// credencial é regravada, sem revelar nada.
func (v Vault) Fingerprint(name string) string {
	blob, err := os.ReadFile(v.path(name))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:8])
}

// DirFingerprint resume a pasta (nomes, tamanhos, datas) para o observador.
func (v Vault) DirFingerprint() string {
	ents, err := os.ReadDir(v.Dir)
	if err != nil {
		return ""
	}
	var parts []string
	for _, e := range ents {
		if info, err := e.Info(); err == nil {
			parts = append(parts, fmt.Sprintf("%s:%d:%d", e.Name(), info.Size(), info.ModTime().UnixNano()))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}
```

`internal/features/credentials/resolver.go`:

```go
package credentials

import (
	"context"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// Origem da credencial.
const (
	SourceVault   = "cofre"
	SourceWindows = "windows"
	SourceNone    = "nenhuma"
)

// Resolution é a credencial resolvida para uma discagem.
type Resolution struct {
	User        string
	Password    shared.Secret
	Saved       *ras.Saved
	Fingerprint string
	Source      string
}

// Resolver aplica a ordem cofre → Windows → nenhuma.
type Resolver struct {
	Vault Vault
	RAS   ras.Client
}

// Resolve devolve a credencial da VPN. Entrada inexistente no catálogo
// volta como *ras.Error 623 (o monitor classifica como ErroConfig).
func (r Resolver) Resolve(_ context.Context, name, entry string) (Resolution, error) {
	saved, err := r.RAS.Saved(entry)
	if err != nil {
		return Resolution{}, err
	}
	user, pw, ok, err := r.Vault.Get(name)
	if err != nil {
		return Resolution{}, err
	}
	if ok {
		return Resolution{User: user, Password: pw, Saved: saved,
			Fingerprint: SourceVault + ":" + r.Vault.Fingerprint(name), Source: SourceVault}, nil
	}
	if saved.HasPassword {
		return Resolution{Saved: saved, Fingerprint: SourceWindows + ":" + saved.Fingerprint(), Source: SourceWindows}, nil
	}
	return Resolution{Saved: saved, Source: SourceNone}, nil
}

// Fingerprint identifica a credencial atual sem decifrá-la.
func (r Resolver) Fingerprint(_ context.Context, name, entry string) string {
	if fp := r.Vault.Fingerprint(name); fp != "" {
		return SourceVault + ":" + fp
	}
	if saved, err := r.RAS.Saved(entry); err == nil && saved.HasPassword {
		return SourceWindows + ":" + saved.Fingerprint()
	}
	return ""
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race ./internal/features/credentials/`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/features/credentials && git commit -m "feat(credentials): cofre DPAPI de máquina por VPN e resolução da credencial na discagem"
```

---

### Task 17: monitor/service: fila global de discagem com prioridade manual

**Files:**
- Create: `internal/features/monitor/service/queue.go`
- Test: `internal/features/monitor/service/queue_test.go`

**Interfaces:**
- Consumes: nada.
- Produces: `service.DialQueue` (valor zero pronto) com `Acquire(ctx, manual bool) (release func(), err error)`; `release` é idempotente.

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/monitor/service/queue_test.go`:

```go
package service

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestDialQueueOrderManualFirst(t *testing.T) {
	var q DialQueue
	release, err := q.Acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup
	start := func(name string, manual bool) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := q.Acquire(context.Background(), manual)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			r()
		}()
		time.Sleep(10 * time.Millisecond) // garante a ordem de chegada
	}
	start("auto1", false)
	start("auto2", false)
	start("manual", true)
	release()
	release() // idempotente
	wg.Wait()
	want := []string{"manual", "auto1", "auto2"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("ordem %v, quer %v", order, want)
		}
	}
}

func TestDialQueueCancelLeavesQueue(t *testing.T) {
	var q DialQueue
	release, _ := q.Acquire(context.Background(), false)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error)
	go func() { _, err := q.Acquire(ctx, false); errc <- err }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-errc; err == nil {
		t.Fatal("esperava erro de cancelamento")
	}
	release()
	r, err := q.Acquire(context.Background(), false) // fila livre de novo
	if err != nil {
		t.Fatal(err)
	}
	r()
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/features/monitor/service/`  
Expected: FAIL — `undefined: DialQueue`.

- [ ] **Step 3: Implementar**

`internal/features/monitor/service/queue.go`:

```go
// Package service tem o supervisor (ator por VPN), a fila global de
// discagem e o orquestrador.
package service

import (
	"context"
	"sync"
)

// DialQueue garante uma discagem por vez entre todas as VPNs (§4.4).
// Pedidos manuais passam na frente dos automáticos; dentro de cada classe,
// ordem de chegada.
type DialQueue struct {
	mu     sync.Mutex
	busy   bool
	manual []*queueWaiter
	auto   []*queueWaiter
}

type queueWaiter struct {
	ch      chan struct{}
	granted bool
}

// Acquire espera a vez. Devolve a função que libera a fila (idempotente).
func (q *DialQueue) Acquire(ctx context.Context, manual bool) (func(), error) {
	q.mu.Lock()
	if !q.busy {
		q.busy = true
		q.mu.Unlock()
		return q.releaser(), nil
	}
	w := &queueWaiter{ch: make(chan struct{})}
	if manual {
		q.manual = append(q.manual, w)
	} else {
		q.auto = append(q.auto, w)
	}
	q.mu.Unlock()

	select {
	case <-w.ch:
		return q.releaser(), nil
	case <-ctx.Done():
		q.mu.Lock()
		if w.granted { // ganhou a vez no mesmo instante: repassa
			q.mu.Unlock()
			q.releaser()()
			return nil, ctx.Err()
		}
		q.manual = remove(q.manual, w)
		q.auto = remove(q.auto, w)
		q.mu.Unlock()
		return nil, ctx.Err()
	}
}

func remove(list []*queueWaiter, w *queueWaiter) []*queueWaiter {
	for i, x := range list {
		if x == w {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}

func (q *DialQueue) releaser() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			q.mu.Lock()
			defer q.mu.Unlock()
			var next *queueWaiter
			switch {
			case len(q.manual) > 0:
				next, q.manual = q.manual[0], q.manual[1:]
			case len(q.auto) > 0:
				next, q.auto = q.auto[0], q.auto[1:]
			}
			if next == nil {
				q.busy = false
				return
			}
			next.granted = true
			close(next.ch)
		})
	}
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race -count=10 ./internal/features/monitor/service/`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/features/monitor/service && git commit -m "feat(monitor): fila global de discagem, uma por vez, manuais na frente"
```

---

### Task 18: monitor/service: supervisor como ator

**Files:**
- Create: `internal/features/monitor/service/supervisor.go`
- Test: `internal/features/monitor/service/supervisor_test.go`

**Interfaces:**
- Consumes: `domain.Decide`, `Initial`, `Status`, `Input`, `Reply` (12–14); `adapters.LinkResult`, `Checker`, `DialJob`, `DialOutcome` (15); `DialQueue` (17); `shared.Clock`, `shared.FakeClock`.
- Produces: Interfaces `service.LinkProber` (`Probe(ctx, entry)`), `service.Dialer` (`Dial(ctx, DialJob) DialOutcome`), `service.Fingerprinter` (`Fingerprint(ctx, name, entry) string`);
  `service.Update{Name; Status domain.Status; Notices []domain.Notice; PauseChanged bool}`;
  `service.Deps{Clock; Link; Checker; Dialer; Queue *DialQueue; Creds Fingerprinter; Rand func() float64; Log *slog.Logger; OnUpdate func(Update)}`;
  `service.NewSupervisor(config.VPN, domain.Status, Deps) *Supervisor` com `Run(ctx)`, `Wake()`, `Send(ctx, domain.Input) (domain.Reply, error)`,
  `CheckNow(ctx)`, `Reconnect(ctx)`, `Pause(ctx, until time.Time)` (zero = indefinida), `Resume(ctx)`, `CredentialChanged(ctx)`, `PowerResume(ctx)`; `service.ErrStopped`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/monitor/service/supervisor_test.go`:

```go
package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/adapters"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

var t0 = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// stubWorld é o "mundo" visto pelo supervisor: enlace, alvo e discador.
type stubWorld struct {
	mu        sync.Mutex
	up        bool
	network   bool
	reachable bool
	probes    int
	gate      chan struct{} // se não nil, Probe espera um valor
	dials     []adapters.DialJob
	outcomes  []adapters.DialOutcome
	block     bool          // Dial espera o ctx (cancelamento)
	hang      chan struct{} // Dial trava ignorando o ctx até fechar
	cancelled int
	panicOn   string
}

func (w *stubWorld) Probe(ctx context.Context, _ string) (adapters.LinkResult, error) {
	w.mu.Lock()
	w.probes++
	gate, p := w.gate, w.panicOn
	w.panicOn = "" // só uma vez
	w.mu.Unlock()
	if p == "probe" {
		panic("bug na sonda")
	}
	if gate != nil {
		<-gate
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return adapters.LinkResult{Up: w.up, Network: w.network}, nil
}

func (w *stubWorld) Check(context.Context) adapters.ReachResult {
	w.mu.Lock()
	defer w.mu.Unlock()
	return adapters.ReachResult{OK: w.reachable, RTT: 10 * time.Millisecond}
}

func (w *stubWorld) Dial(ctx context.Context, job adapters.DialJob) adapters.DialOutcome {
	w.mu.Lock()
	w.dials = append(w.dials, job)
	block, hang := w.block, w.hang
	var out adapters.DialOutcome
	if len(w.outcomes) > 0 {
		out, w.outcomes = w.outcomes[0], w.outcomes[1:]
	}
	w.mu.Unlock()
	if hang != nil {
		<-hang
		return adapters.DialOutcome{Cancelled: true}
	}
	if block {
		<-ctx.Done()
		w.mu.Lock()
		w.cancelled++
		w.mu.Unlock()
		return adapters.DialOutcome{Cancelled: true}
	}
	if out.Err == nil { // discagem do stub bem-sucedida: enlace e alvo de pé
		w.mu.Lock()
		w.up, w.reachable = true, true
		w.mu.Unlock()
	}
	return out
}

func (w *stubWorld) Fingerprint(context.Context, string, string) string { return "fp1" }

func (w *stubWorld) set(f func(w *stubWorld)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	f(w)
}

func (w *stubWorld) get() stubWorld {
	w.mu.Lock()
	defer w.mu.Unlock()
	return stubWorld{probes: w.probes, dials: append([]adapters.DialJob(nil), w.dials...), cancelled: w.cancelled}
}

type harness struct {
	t       *testing.T
	clk     *shared.FakeClock
	w       *stubWorld
	sup     *Supervisor
	updates chan Update
	cancel  context.CancelFunc
	done    chan any // valor do panic, ou nil
}

func vpnConfig() config.VPN {
	return config.RawVPN{Name: "Matriz", RasEntry: "VPN Matriz",
		Check: &config.RawCheck{Kind: config.CheckPing, Host: "10.0.0.1"}}.Normalize()
}

func start(t *testing.T, w *stubWorld, initial *domain.Status) *harness {
	t.Helper()
	clk := shared.NewFakeClock(t0)
	h := &harness{t: t, clk: clk, w: w, updates: make(chan Update, 1000), done: make(chan any, 1)}
	v := vpnConfig()
	st := domain.Initial(domain.ParamsFrom(v), t0, config.Pause{})
	if initial != nil {
		st = *initial
	}
	h.sup = NewSupervisor(v, st, Deps{
		Clock: clk, Link: w, Checker: w, Dialer: w, Queue: &DialQueue{}, Creds: w,
		OnUpdate: func(u Update) { h.updates <- u },
	})
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() {
		defer func() { h.done <- recover() }()
		h.sup.Run(ctx)
	}()
	t.Cleanup(func() { cancel(); <-h.done })
	return h
}

// waitState lê atualizações até o estado pedido (e devolve os avisos vistos).
func (h *harness) waitState(want domain.State) (domain.Status, []domain.Notice) {
	h.t.Helper()
	var notices []domain.Notice
	timeout := time.After(2 * time.Second)
	for {
		select {
		case u := <-h.updates:
			notices = append(notices, u.Notices...)
			if u.Status.State == want && u.Status.Op == domain.OpNone {
				return u.Status, notices
			}
		case <-timeout:
			h.t.Fatalf("estado %s não chegou", want)
		}
	}
}

func TestAdoptsExistingConnectionWithoutDialing(t *testing.T) {
	w := &stubWorld{up: true, network: true, reachable: true}
	h := start(t, w, nil)
	s, _ := h.waitState(domain.Conectada)
	if s.LastRTT != 10*time.Millisecond || len(w.get().dials) != 0 {
		t.Fatalf("adoção não deve discar: %+v %v", s, w.get().dials)
	}
}

func TestDialsWhenDownAndReportsConnected(t *testing.T) {
	w := &stubWorld{network: true, reachable: true}
	h := start(t, w, nil)
	h.waitState(domain.Conectada)
	if d := w.get().dials; len(d) != 1 || d[0].HangupFirst || d[0].Timeout != time.Minute {
		t.Fatalf("discagens %+v", d)
	}
}

func TestZombieTunnelHangsUpAndRedials(t *testing.T) {
	w := &stubWorld{up: true, network: true, reachable: true}
	h := start(t, w, nil)
	h.waitState(domain.Conectada)
	w.set(func(w *stubWorld) { w.reachable = false })
	for i := 1; i <= 2; i++ {
		h.clk.Advance(30 * time.Second)
		if s, _ := h.waitState(domain.Degradada); s.Failures != i {
			t.Fatalf("falhas = %d, quer %d", s.Failures, i)
		}
	}
	// 3ª falha: túnel zumbi. O discador do stub "conecta" e o alcance volta.
	w.set(func(w *stubWorld) { w.outcomes = []adapters.DialOutcome{{}} })
	h.clk.Advance(30 * time.Second)
	_, notices := h.waitState(domain.Conectada)
	d := w.get().dials
	if len(d) != 1 || !d[0].HangupFirst {
		t.Fatalf("zumbi deve desligar e discar: %+v", d)
	}
	if len(notices) != 2 || notices[0].Kind != domain.NoticeDown || notices[1].Kind != domain.NoticeUp {
		t.Fatalf("avisos %+v", notices)
	}
}

func TestRejectedCredentialStopsDialing(t *testing.T) {
	w := &stubWorld{network: true, outcomes: []adapters.DialOutcome{{
		Err: &domain.DialError{Class: ras.ClassCredencial, Code: 691}, Fingerprint: "fp1"}}}
	h := start(t, w, nil)
	_, notices := h.waitState(domain.CredencialInvalida)
	r, _ := h.sup.Reconnect(context.Background())
	if r.Code != domain.ReplyCredentialRejected {
		t.Fatalf("clique imediato com a mesma credencial deve ser recusado: %+v", r)
	}
	for i := 0; i < 10; i++ {
		h.clk.Advance(5 * time.Minute)
	}
	time.Sleep(20 * time.Millisecond)
	if n := len(w.get().dials); n != 1 {
		t.Fatalf("691 deve parar as tentativas; discagens = %d", n)
	}
	if len(notices) != 1 || !strings.Contains(notices[0].Text, "credencial rejeitada") {
		t.Fatalf("avisos %+v", notices)
	}
}

func TestPauseWinsOverDialInProgress(t *testing.T) {
	w := &stubWorld{network: true, block: true}
	h := start(t, w, nil)
	deadline := time.Now().Add(2 * time.Second)
	for len(w.get().dials) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	r, err := h.sup.Pause(context.Background(), t0.Add(15*time.Minute))
	if err != nil || r.Code != domain.ReplyOK {
		t.Fatal(r, err)
	}
	h.waitState(domain.Pausada)
	for w.get().cancelled == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if w.get().cancelled != 1 {
		t.Fatal("a discagem em curso deve ser cancelada (hangup)")
	}
	r, _ = h.sup.Reconnect(context.Background())
	if r.Code != domain.ReplyPaused {
		t.Fatalf("reconnect em pausa: %+v", r)
	}
}

func TestStopDuringDialCancelsIt(t *testing.T) {
	w := &stubWorld{network: true, block: true}
	h := start(t, w, nil)
	deadline := time.Now().Add(2 * time.Second)
	for len(w.get().dials) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	h.cancel()
	select {
	case <-h.done:
		h.done <- nil // para o Cleanup
	case <-time.After(2 * time.Second):
		t.Fatal("Run não voltou")
	}
	if w.get().cancelled != 1 {
		t.Fatal("parar o serviço durante a discagem deve desligá-la")
	}
	if _, err := h.sup.CheckNow(context.Background()); err != ErrStopped {
		t.Fatalf("comando após parada: %v", err)
	}
}

func TestWakesAggregateWhileProbing(t *testing.T) {
	gate := make(chan struct{})
	w := &stubWorld{up: true, network: true, reachable: true, gate: gate}
	h := start(t, w, nil)
	deadline := time.Now().Add(2 * time.Second)
	for w.get().probes == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 5; i++ {
		h.sup.Wake()
	}
	close(gate)
	h.waitState(domain.Conectada)
	time.Sleep(20 * time.Millisecond)
	if p := w.get().probes; p > 2 {
		t.Fatalf("despertares deveriam se agregar: %d sondas", p)
	}
}

func TestNoNetworkDoesNotDial(t *testing.T) {
	w := &stubWorld{network: false}
	h := start(t, w, nil)
	h.waitState(domain.SemRede)
	h.clk.Advance(30 * time.Second)
	h.waitState(domain.SemRede)
	if n := len(w.get().dials); n != 0 {
		t.Fatalf("sem rede não disca: %d", n)
	}
	w.set(func(w *stubWorld) { w.network = true; w.reachable = true })
	h.sup.Wake()
	h.waitState(domain.Conectada)
}

func TestPanicInOperationPropagatesToRun(t *testing.T) {
	w := &stubWorld{network: true, panicOn: "probe"}
	h := start(t, w, nil)
	select {
	case v := <-h.done:
		if v == nil || !strings.Contains(v.(string), "bug na sonda") {
			t.Fatalf("panic = %v", v)
		}
		h.done <- nil
	case <-time.After(2 * time.Second):
		t.Fatal("panic não chegou ao Run")
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/features/monitor/service/`  
Expected: FAIL — `undefined: NewSupervisor`…

- [ ] **Step 3: Implementar**

Uma goroutine dona do `domain.Status`; operações externas rodam fora do ator e voltam como entradas marcadas com uma
**geração** — resultado de operação cancelada (pausa, reconexão manual) é descartado. O temporizador é armado **antes** de publicar a
atualização (os testes com relógio falso dependem disso). Panic numa operação é repassado ao ator e propagado por `Run`, para o
orquestrador recuperar e recriar o supervisor (§4.9). Parar (`ctx`) cancela a discagem em curso, que desliga com `RasHangUp`.

`internal/features/monitor/service/supervisor.go`:

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"runtime/debug"
	"sync"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/adapters"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// ErrStopped é devolvido a comandos enviados a um supervisor encerrado.
var ErrStopped = errors.New("supervisor encerrado")

// LinkProber, Dialer e Fingerprinter são o que o supervisor usa de fora.
type LinkProber interface {
	Probe(ctx context.Context, entry string) (adapters.LinkResult, error)
}

type Dialer interface {
	Dial(ctx context.Context, job adapters.DialJob) adapters.DialOutcome
}

type Fingerprinter interface {
	Fingerprint(ctx context.Context, name, entry string) string
}

// Update é publicado a cada mudança do estado de uma VPN.
type Update struct {
	Name    string
	Status  domain.Status
	Notices []domain.Notice
	// PauseChanged indica que a pausa mudou e precisa ir para state.json.
	PauseChanged bool
}

// Deps são as dependências de um supervisor.
type Deps struct {
	Clock    shared.Clock
	Link     LinkProber
	Checker  adapters.Checker
	Dialer   Dialer
	Queue    *DialQueue
	Creds    Fingerprinter
	Rand     func() float64
	Log      *slog.Logger
	OnUpdate func(Update)
}

type message struct {
	in    domain.Input
	reply chan domain.Reply
	// Resultados de operação levam a geração; os de gerações antigas
	// (canceladas) são descartados.
	result bool
	gen    uint64
	panic  string
}

// Supervisor é o ator de uma VPN: uma goroutine dona exclusiva do estado.
type Supervisor struct {
	vpn     config.VPN
	params  domain.Params
	deps    Deps
	initial domain.Status

	in      chan message
	wake    chan struct{}
	stopped chan struct{}
}

// NewSupervisor cria o ator; chame Run numa goroutine.
func NewSupervisor(v config.VPN, initial domain.Status, deps Deps) *Supervisor {
	if deps.Rand == nil {
		deps.Rand = func() float64 { return 0.5 }
	}
	if deps.Log == nil {
		deps.Log = slog.New(slog.DiscardHandler)
	}
	if deps.OnUpdate == nil {
		deps.OnUpdate = func(Update) {}
	}
	return &Supervisor{
		vpn: v, params: domain.ParamsFrom(v), deps: deps, initial: initial,
		in: make(chan message), wake: make(chan struct{}, 1), stopped: make(chan struct{}),
	}
}

// Wake pede uma reavaliação (desconexão RAS, mudança de rede). Vários
// pedidos pendentes viram um só ciclo.
func (s *Supervisor) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Send entrega uma entrada (comando ou evento) e espera a resposta.
func (s *Supervisor) Send(ctx context.Context, in domain.Input) (domain.Reply, error) {
	m := message{in: in, reply: make(chan domain.Reply, 1)}
	select {
	case s.in <- m:
	case <-s.stopped:
		return domain.Reply{}, ErrStopped
	case <-ctx.Done():
		return domain.Reply{}, ctx.Err()
	}
	select {
	case r := <-m.reply:
		return r, nil
	case <-s.stopped:
		return domain.Reply{}, ErrStopped
	case <-ctx.Done():
		return domain.Reply{}, ctx.Err()
	}
}

// CheckNow pede uma verificação imediata.
func (s *Supervisor) CheckNow(ctx context.Context) (domain.Reply, error) {
	return s.Send(ctx, domain.Input{Kind: domain.InCheckNow})
}

// Reconnect pede reconexão manual, com a impressão digital atual da
// credencial (o domínio recusa repetir uma credencial já rejeitada).
func (s *Supervisor) Reconnect(ctx context.Context) (domain.Reply, error) {
	return s.Send(ctx, domain.Input{Kind: domain.InReconnect, Fingerprint: s.fingerprint(ctx)})
}

// Pause pausa até until (zero = indefinida).
func (s *Supervisor) Pause(ctx context.Context, until time.Time) (domain.Reply, error) {
	return s.Send(ctx, domain.Input{Kind: domain.InPause, PauseUntil: until})
}

// Resume retoma.
func (s *Supervisor) Resume(ctx context.Context) (domain.Reply, error) {
	return s.Send(ctx, domain.Input{Kind: domain.InResume, Fingerprint: s.fingerprint(ctx)})
}

// CredentialChanged avisa que o cofre mudou (o domínio ignora se a
// impressão digital desta VPN não mudou).
func (s *Supervisor) CredentialChanged(ctx context.Context) {
	_, _ = s.Send(ctx, domain.Input{Kind: domain.InCredentialChanged, Fingerprint: s.fingerprint(ctx)})
}

// PowerResume avisa retomada de energia ou salto de relógio.
func (s *Supervisor) PowerResume(ctx context.Context) {
	_, _ = s.Send(ctx, domain.Input{Kind: domain.InPowerResume})
}

func (s *Supervisor) fingerprint(ctx context.Context) string {
	if s.deps.Creds == nil {
		return ""
	}
	return s.deps.Creds.Fingerprint(ctx, s.vpn.Name, s.vpn.RasEntry)
}

// Run executa o ator até ctx terminar. Ao sair, cancela a operação em
// andamento (a discagem desliga com RasHangUp) e espera ela voltar.
func (s *Supervisor) Run(ctx context.Context) {
	defer close(s.stopped)
	var (
		status   = s.initial
		gen      uint64
		cancelOp context.CancelFunc
		ops      sync.WaitGroup
	)
	defer func() {
		if cancelOp != nil {
			cancelOp()
		}
		ops.Wait()
	}()

	timer := s.deps.Clock.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	arm := func() {
		timer.Stop()
		if status.NextTick.IsZero() {
			return
		}
		d := status.NextTick.Sub(s.deps.Clock.Now())
		if d < 0 {
			d = 0
		}
		timer.Reset(d)
	}

	apply := func(in domain.Input) *domain.Reply {
		dec := domain.Decide(status, in, s.params, domain.Env{Now: s.deps.Clock.Now(), Rand: s.deps.Rand()})
		if dec.Cancel && cancelOp != nil {
			cancelOp()
			cancelOp = nil
			gen++
		}
		prev := status
		status = dec.Next
		if dec.Action != domain.OpNone {
			gen++
			opCtx, cancel := context.WithCancel(ctx)
			cancelOp = cancel
			ops.Add(1)
			go s.execute(ctx, opCtx, gen, dec.Action, dec.Manual, &ops)
		}
		for _, n := range dec.Notices {
			s.deps.Log.Info(n.Text, "aviso", string(n.Kind))
		}
		if prev.State != status.State {
			s.deps.Log.Info("estado", "de", string(prev.State), "para", string(status.State))
		}
		arm() // antes de publicar: quem observa já encontra o temporizador armado
		if len(dec.Notices) > 0 || !reflect.DeepEqual(prev, status) {
			s.deps.OnUpdate(Update{
				Name: s.vpn.Name, Status: status, Notices: dec.Notices,
				PauseChanged: prev.PauseRecord() != status.PauseRecord(),
			})
		}
		return dec.Reply
	}

	arm()
	s.deps.OnUpdate(Update{Name: s.vpn.Name, Status: status})
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C():
			apply(domain.Input{Kind: domain.InTick})
		case <-s.wake:
			apply(domain.Input{Kind: domain.InWake})
		case m := <-s.in:
			switch {
			case m.panic != "":
				panic(m.panic) // o orquestrador recupera e recria o supervisor
			case m.result:
				if m.gen != gen {
					continue // operação cancelada: resultado velho
				}
				cancelOp = nil
				apply(m.in)
			default:
				r := apply(m.in)
				if r == nil {
					r = &domain.Reply{}
				}
				m.reply <- *r
			}
		}
	}
}

// execute roda uma operação fora do ator e devolve o resultado como entrada.
func (s *Supervisor) execute(runCtx, ctx context.Context, gen uint64, op domain.Op, manual bool, wg *sync.WaitGroup) {
	defer wg.Done()
	deliver := func(m message) {
		m.gen = gen
		select {
		case s.in <- m:
		case <-runCtx.Done():
		}
	}
	defer func() {
		if r := recover(); r != nil {
			deliver(message{panic: fmt.Sprintf("panic na operação %v: %v\n%s", op, r, debug.Stack())})
		}
	}()
	entry := s.vpn.RasEntry
	switch op {
	case domain.OpProbeLink:
		res, err := s.deps.Link.Probe(ctx, entry)
		if err != nil {
			s.deps.Log.Warn("consultando o enlace", "erro", err)
			res = adapters.LinkResult{Network: true}
		}
		deliver(message{result: true, in: domain.Input{Kind: domain.InLinkResult, LinkUp: res.Up, Network: res.Network}})
	case domain.OpProbeReach:
		r := s.deps.Checker.Check(ctx)
		if r.Err != nil {
			s.deps.Log.Debug("verificação de alcance", "erro", r.Err)
		}
		deliver(message{result: true, in: domain.Input{Kind: domain.InReachResult, ReachOK: r.OK, RTT: r.RTT}})
	case domain.OpDial, domain.OpHangupDial:
		release, err := s.deps.Queue.Acquire(ctx, manual)
		if err != nil {
			return // cancelada enquanto esperava a fila
		}
		defer release()
		s.deps.Log.Info("discando", "manual", manual, "desligaAntes", op == domain.OpHangupDial)
		out := s.deps.Dialer.Dial(ctx, adapters.DialJob{
			Name: s.vpn.Name, Entry: entry, HangupFirst: op == domain.OpHangupDial,
			Timeout: time.Duration(s.vpn.ConnectTimeoutSeconds) * time.Second,
		})
		if out.Cancelled {
			return
		}
		if out.Err != nil {
			s.deps.Log.Warn("discagem falhou", "classe", out.Err.Class.String(), "codigo", out.Err.Code, "mensagem", out.Err.Message)
		}
		deliver(message{result: true, in: domain.Input{Kind: domain.InDialResult, DialErr: out.Err, Fingerprint: out.Fingerprint}})
	}
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race -count=20 ./internal/features/monitor/service/`  
Expected: PASS (repetir para pegar corrida)

- [ ] **Step 5: Commit**

```bash
git add internal/features/monitor/service && git commit -m "feat(monitor): supervisor ator por VPN com cancelamento por geração e recuperação de panic"
```

---

### Task 19: core/ipc: protocolo e codec (JSON por linha, 64 KB, estrito)

**Files:**
- Create: `internal/core/ipc/protocol.go`
- Create: `internal/core/ipc/codec.go`
- Test: `internal/core/ipc/codec_test.go`

**Interfaces:**
- Consumes: `config.DecodeStrict`, `config.RawVPN`, `config.FieldError`, `config.Config` (4).
- Produces: `ipc.ProtocolVersion = 1`, `ipc.MaxMessage = 65536`, `ipc.MaxConns = 32`, `ipc.PipeName`, `ipc.PipeSDDL`, `ipc.PipeIUMask = 0x0012019b`, `ipc.FileCreatePipeInstance = 0x4`; constantes `ipc.Type*` (pedidos, `ok`, `error`, eventos) e `ipc.Code*`;
  `ipc.Message{V int; ID, Type string; Payload json.RawMessage}`; `ipc.Error{Code, Message string; Fields []config.FieldError}` (implementa `error`);
  payloads `Hello`, `VPNRef`, `PauseRequest{VPN; UntilUnix *int64}`, `SetEnabledRequest`, `AddVPNRequest{Config config.RawVPN}`, `UpdateVPNRequest{Name; Config}`,
  `RemoveVPNRequest`, `SetGlobalRequest{Notifications *bool; LogLevel *string}`, `LogTailRequest{MaxBytes}`;
  `VPNView{Name, Entry, Enabled, CheckKind, State, SinceUnix, LastCheckUnix, LatencyMs, Failures, Attempt, NextAttemptUnix, Reconnects24h, LastError *ErrorInfo, PausedUntilUnix, PausedIndefinite}`,
  `ErrorInfo{Class, Code, Message}`, `Snapshot{VPNs; Notifications}`, `NoticeEvent{VPN, Kind, Text}`, `ConfigStatus{OK; Message; Fields}`, `RasEntry{Name; Monitored}`, `RasEntries`, `LogTail{Text}`;
  `ipc.NewMessage(id, typ, payload)`, `ipc.MustMessage`, `ipc.ErrorMessage(id, *Error)`, `ipc.DecodePayload(raw, out) error` (erro = `*Error{bad_request}`);
  `ipc.Decode([]byte) (Message, error)` (erros `*ipc.DecodeError`), `ipc.NewCodec(io.ReadWriter) *Codec` com `Read()`/`Write(Message)`, `ipc.ErrTooLarge`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/core/ipc/codec_test.go`:

```go
package ipc

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestCodecRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	c := NewCodec(&buf)
	until := int64(1700000000)
	if err := c.Write(MustMessage("1", TypePause, PauseRequest{VPN: "Matriz", UntilUnix: &until})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `{"v":1,"id":"1","type":"pause","payload":{"vpn":"Matriz","untilUnix":1700000000}}`) {
		t.Fatalf("formato: %s", buf.String())
	}
	m, err := c.Read()
	if err != nil {
		t.Fatal(err)
	}
	var p PauseRequest
	if err := DecodePayload(m.Payload, &p); err != nil || p.VPN != "Matriz" || *p.UntilUnix != until {
		t.Fatalf("%+v %v", p, err)
	}
	var null PauseRequest
	if err := DecodePayload([]byte(`{"vpn":"x","untilUnix":null}`), &null); err != nil || null.UntilUnix != nil {
		t.Fatal("null = indefinida")
	}
}

func TestPipeSDDLDoesNotLetUsersCreateInstances(t *testing.T) {
	if !strings.Contains(PipeSDDL, "(A;;0x0012019b;;;IU)") || strings.Contains(PipeSDDL, "GW;;;IU") {
		t.Fatalf("ACE de IU inesperada: %s", PipeSDDL)
	}
	if PipeIUMask&FileCreatePipeInstance != 0 {
		t.Fatal("IU não pode ter FILE_CREATE_PIPE_INSTANCE")
	}
	const fileReadData, fileWriteData = 0x1, 0x2
	if PipeIUMask&fileReadData == 0 || PipeIUMask&fileWriteData == 0 {
		t.Fatal("IU precisa ler e escrever")
	}
}

func TestDecodeRejects(t *testing.T) {
	bad := []string{
		`{"v":1,"type":"status","extra":1}`,
		`{"v":2,"type":"status"}`,
		`{"v":1}`,
		`{"v":1,"type":"status"} {}`,
		`não é json`,
		``,
	}
	for _, in := range bad {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("Decode(%q) deveria falhar", in)
		}
	}
	var p VPNRef
	if err := DecodePayload([]byte(`{"vpn":"a","x":1}`), &p); err == nil {
		t.Fatal("payload com campo desconhecido")
	}
	var e *Error
	if err := DecodePayload([]byte(`{"vpn":1}`), &p); !errors.As(err, &e) || e.Code != CodeBadRequest {
		t.Fatalf("erro de payload deve ser bad_request: %v", err)
	}
}

func TestCodecLimits(t *testing.T) {
	big := `{"v":1,"type":"x","payload":"` + strings.Repeat("a", MaxMessage) + "\"}\n"
	c := NewCodec(bytes.NewBufferString(big))
	if _, err := c.Read(); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("leitura grande: %v", err)
	}
	var out bytes.Buffer
	w := NewCodec(&out)
	if err := w.Write(MustMessage("1", TypeLogTail, LogTail{Text: strings.Repeat("a", MaxMessage)})); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("escrita grande: %v", err)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte(`{"v":1,"id":"1","type":"status"}`))
	f.Add([]byte(`{"v":1,"type":"pause","payload":{"vpn":"a","untilUnix":null}}`))
	f.Add([]byte(`{"v":1,"type":"x","payload":[1,2,{"a":"\u0000"}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := Decode(data)
		if err != nil {
			return
		}
		if m.V != ProtocolVersion || m.Type == "" {
			t.Fatalf("mensagem inválida aceita: %+v", m)
		}
		var p PauseRequest
		_ = DecodePayload(m.Payload, &p) // não pode entrar em pânico
	})
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/core/ipc/`  
Expected: FAIL — `undefined: NewCodec`…

- [ ] **Step 3: Implementar**

Lacuna do spec preenchida: a §7 diz que "Abrir log" pede o fim do log **pelo pipe**, mas a §6.2 não lista o pedido; foi
acrescentado `logTail{maxBytes}` → `{text}`. As respostas usam `type:"ok"`/`type:"error"` com o mesmo `id`.

A ACE de Usuários Interativos usa a máscara explícita `0x0012019b` em vez de `GRGW`: em pipes, `FILE_APPEND_DATA` (parte de `GW`) é
`FILE_CREATE_PIPE_INSTANCE`, e um usuário comum poderia abrir uma instância falsa do pipe.

`internal/core/ipc/protocol.go`:

```go
// Package ipc é o canal bandeja/CLI ↔ serviço: named pipe \\.\pipe\vpnmon,
// uma mensagem JSON por linha (§6).
package ipc

import (
	"encoding/json"
	"fmt"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
)

// Constantes do protocolo (§6.1, §6.2).
const (
	ProtocolVersion = 1
	MaxMessage      = 64 * 1024
	MaxConns        = 32
	PipeName        = `\\.\pipe\vpnmon`
	// PipeSDDL: Rede negada; SYSTEM e Administradores total; Usuários
	// Interativos leitura e escrita. P = sem herança. Para IU a máscara é
	// explícita (0x0012019b = FILE_GENERIC_READ | FILE_WRITE_DATA |
	// FILE_WRITE_EA | FILE_WRITE_ATTRIBUTES): "GW" incluiria
	// FILE_APPEND_DATA, que em pipes é FILE_CREATE_PIPE_INSTANCE e deixaria
	// um usuário abrir uma instância falsa do pipe.
	PipeSDDL = "D:P(D;;GA;;;NU)(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x0012019b;;;IU)"
	// PipeIUMask é a máscara de IU acima; FileCreatePipeInstance não pode estar nela.
	PipeIUMask             = 0x0012019b
	FileCreatePipeInstance = 0x0004
)

// Tipos de mensagem.
const (
	TypeHello          = "hello"
	TypeOK             = "ok"
	TypeError          = "error"
	TypeStatus         = "status"
	TypeCheckNow       = "checkNow"
	TypeReconnect      = "reconnect"
	TypePause          = "pause"
	TypeResume         = "resume"
	TypeSetEnabled     = "setEnabled"
	TypeAddVPN         = "addVpn"
	TypeUpdateVPN      = "updateVpn"
	TypeRemoveVPN      = "removeVpn"
	TypeListRasEntries = "listRasEntries"
	TypeGetConfig      = "getConfig"
	TypeSetGlobal      = "setGlobal"
	TypeLogTail        = "logTail"
	TypeSubscribe      = "subscribe"
	// Eventos (após subscribe).
	TypeSnapshot        = "snapshot"
	TypeVPNState        = "vpnState"
	TypeNotice          = "notice"
	TypeConfigStatus    = "configStatus"
	TypeServiceStopping = "serviceStopping"
)

// Message é o envelope de toda linha.
type Message struct {
	V       int             `json:"v"`
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Códigos de erro.
const (
	CodeBadRequest          = "bad_request"
	CodeUnknownType         = "unknown_type"
	CodeIncompatible        = "incompatible"
	CodeNotFound            = "not_found"
	CodeInvalidConfig       = "invalid_config"
	CodePaused              = "paused"
	CodeAlreadyReconnecting = "already_reconnecting"
	CodeCredentialRejected  = "credential_rejected"
	CodeDisabled            = "disabled"
	CodeBusy                = "busy"
	CodeInternal            = "internal"
)

// Error é o payload de uma resposta "error". Fields aponta erros de
// validação por campo (para a janela de configurações).
type Error struct {
	Code    string              `json:"code"`
	Message string              `json:"message"`
	Fields  []config.FieldError `json:"fields,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Payloads de pedidos.
type (
	Hello struct {
		Protocol   int    `json:"protocol"`
		AppVersion string `json:"appVersion"`
	}
	VPNRef struct {
		VPN string `json:"vpn"`
	}
	PauseRequest struct {
		VPN       string `json:"vpn"`
		UntilUnix *int64 `json:"untilUnix"` // null = até retomar
	}
	SetEnabledRequest struct {
		VPN     string `json:"vpn"`
		Enabled bool   `json:"enabled"`
	}
	AddVPNRequest struct {
		Config config.RawVPN `json:"config"`
	}
	UpdateVPNRequest struct {
		Name   string        `json:"name"`
		Config config.RawVPN `json:"config"`
	}
	RemoveVPNRequest struct {
		Name string `json:"name"`
	}
	SetGlobalRequest struct {
		Notifications *bool   `json:"notifications,omitempty"`
		LogLevel      *string `json:"logLevel,omitempty"`
	}
	LogTailRequest struct {
		MaxBytes int `json:"maxBytes"`
	}
)

// Payloads de respostas e eventos.
type (
	// VPNView é o estado público de uma VPN (snapshot e vpnState).
	VPNView struct {
		Name             string     `json:"name"`
		Entry            string     `json:"entry"`
		Enabled          bool       `json:"enabled"`
		CheckKind        string     `json:"checkKind"`
		State            string     `json:"state"`
		SinceUnix        int64      `json:"sinceUnix"`
		LastCheckUnix    int64      `json:"lastCheckUnix,omitempty"`
		LatencyMs        int64      `json:"latencyMs,omitempty"`
		Failures         int        `json:"failures"`
		Attempt          int        `json:"attempt"`
		NextAttemptUnix  int64      `json:"nextAttemptUnix,omitempty"`
		Reconnects24h    int        `json:"reconnects24h"`
		LastError        *ErrorInfo `json:"lastError,omitempty"`
		PausedUntilUnix  int64      `json:"pausedUntilUnix,omitempty"`
		PausedIndefinite bool       `json:"pausedIndefinite,omitempty"`
	}
	ErrorInfo struct {
		Class   string `json:"class"`
		Code    uint32 `json:"code"`
		Message string `json:"message"`
	}
	Snapshot struct {
		VPNs          []VPNView `json:"vpns"`
		Notifications bool      `json:"notifications"`
	}
	NoticeEvent struct {
		VPN  string `json:"vpn"`
		Kind string `json:"kind"`
		Text string `json:"text"`
	}
	ConfigStatus struct {
		OK      bool                `json:"ok"`
		Message string              `json:"message,omitempty"`
		Fields  []config.FieldError `json:"fields,omitempty"`
	}
	RasEntry struct {
		Name      string `json:"name"`
		Monitored bool   `json:"monitored"`
	}
	RasEntries struct {
		Entries []RasEntry `json:"entries"`
	}
	LogTail struct {
		Text string `json:"text"`
	}
)

// NewMessage monta uma mensagem com payload serializado.
func NewMessage(id, typ string, payload any) (Message, error) {
	m := Message{V: ProtocolVersion, ID: id, Type: typ}
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return Message{}, err
		}
		m.Payload = b
	}
	return m, nil
}

// MustMessage é NewMessage para payloads que sempre serializam (tipos deste pacote).
func MustMessage(id, typ string, payload any) Message {
	m, err := NewMessage(id, typ, payload)
	if err != nil {
		panic(err)
	}
	return m
}

// ErrorMessage monta a resposta de erro.
func ErrorMessage(id string, e *Error) Message { return MustMessage(id, TypeError, e) }

// DecodePayload decodifica estritamente (campos desconhecidos são erro).
func DecodePayload(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := config.DecodeStrict(raw, out); err != nil {
		return &Error{Code: CodeBadRequest, Message: err.Error()}
	}
	return nil
}
```

`internal/core/ipc/codec.go`:

```go
package ipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ErrTooLarge: linha acima de MaxMessage. A conexão deve ser fechada.
var ErrTooLarge = errors.New("mensagem acima de 64 KB")

// DecodeError é uma linha que não é uma mensagem válida do protocolo.
type DecodeError struct{ Msg string }

func (e *DecodeError) Error() string { return e.Msg }

// Codec lê e escreve mensagens, uma por linha.
type Codec struct {
	r  *bufio.Reader
	w  io.Writer
	mu sync.Mutex
}

// NewCodec embrulha a conexão.
func NewCodec(rw io.ReadWriter) *Codec {
	return &Codec{r: bufio.NewReaderSize(rw, MaxMessage+1), w: rw}
}

// Decode valida uma linha: JSON estrito, versão e tipo presentes.
func Decode(line []byte) (Message, error) {
	line = bytes.TrimRight(line, "\r\n")
	var m Message
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Message{}, &DecodeError{"JSON malformado: " + err.Error()}
	}
	if dec.More() {
		return Message{}, &DecodeError{"conteúdo extra na linha"}
	}
	if m.V != ProtocolVersion {
		return Message{}, &DecodeError{fmt.Sprintf("versão de mensagem %d não suportada", m.V)}
	}
	if m.Type == "" {
		return Message{}, &DecodeError{"mensagem sem tipo"}
	}
	return m, nil
}

// Read lê a próxima mensagem.
func (c *Codec) Read() (Message, error) {
	line, err := c.r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return Message{}, ErrTooLarge
	}
	if err != nil {
		if err == io.EOF && len(line) > 0 {
			return Message{}, io.ErrUnexpectedEOF
		}
		return Message{}, err
	}
	return Decode(line)
}

// Write escreve uma mensagem (seguro para uso concorrente).
func (c *Codec) Write(m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b)+1 > MaxMessage {
		return ErrTooLarge
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.w.Write(append(b, '\n'))
	return err
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race ./internal/core/ipc/`  
Expected: PASS

Run: `go test ./internal/core/ipc/ -run=^$ -fuzz=FuzzDecode -fuzztime=10s`  
Expected: PASS sem falhas do fuzz

- [ ] **Step 5: Commit**

```bash
git add internal/core/ipc && git commit -m "feat(ipc): protocolo v1 com codec por linha, limite de 64 KB e fuzz do decodificador"
```

---

### Task 20: core/ipc: servidor, cliente da CLI e transporte por named pipe

**Files:**
- Create: `internal/core/ipc/server.go`
- Create: `internal/core/ipc/client.go`
- Create: `internal/core/ipc/pipe_windows.go`
- Create: `internal/core/ipc/pipe_other.go`
- Test: `internal/core/ipc/server_test.go`

**Interfaces:**
- Consumes: Tudo da tarefa 19; `svc.ServicePID` (11); `config.*`.
- Produces: Interface `ipc.Backend`: `Status() Snapshot`, `CheckNow(vpn) error`, `Reconnect(vpn) error`, `Pause(vpn, until *time.Time) error`, `Resume(vpn) error`,
  `SetEnabled(vpn, bool) error`, `AddVPN(config.RawVPN) error`, `UpdateVPN(name, config.RawVPN) error`, `RemoveVPN(name) error`, `ListRasEntries() ([]RasEntry, error)`,
  `GetConfig() config.Config`, `SetGlobal(SetGlobalRequest) error`, `LogTail(maxBytes int) (string, error)`, `Subscribe() (<-chan Message, func())`;
  `ipc.Server{Backend; AppVersion; Log; MaxConns; HandshakeTimeout; WriteTimeout; IdleTimeout; OutQueue}` com `Serve(ctx, net.Listener) error`;
  `ipc.Handshake(net.Conn, appVersion) (*Client, error)`, `(*Client).Call(typ, payload, out) error` (erro do serviço vem como `*ipc.Error`), `Close()`, campo `ServerApp`;
  `ipc.Listen() (net.Listener, error)`, `ipc.Dial(ctx) (net.Conn, error)` (stubs fora do Windows).

- [ ] **Step 1: Escrever o teste que falha**

`internal/core/ipc/server_test.go`:

```go
package ipc

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
)

type fakeBackend struct {
	mu      sync.Mutex
	calls   []string
	events  chan Message
	cancels int
}

func (f *fakeBackend) rec(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeBackend) Status() Snapshot {
	return Snapshot{VPNs: []VPNView{{Name: "Matriz", State: "Conectada"}}, Notifications: true}
}
func (f *fakeBackend) CheckNow(v string) error { f.rec("checkNow " + v); return nil }
func (f *fakeBackend) Reconnect(v string) error {
	return &Error{Code: CodePaused, Message: "VPN pausada"}
}
func (f *fakeBackend) Pause(v string, until *time.Time) error {
	if until == nil {
		f.rec("pause " + v + " indefinida")
	} else {
		f.rec("pause " + v + " " + until.UTC().Format(time.RFC3339))
	}
	return nil
}
func (f *fakeBackend) Resume(string) error                   { return errors.New("disco cheio") }
func (f *fakeBackend) SetEnabled(string, bool) error         { panic("bug") }
func (f *fakeBackend) AddVPN(config.RawVPN) error            { return nil }
func (f *fakeBackend) UpdateVPN(string, config.RawVPN) error { return nil }
func (f *fakeBackend) RemoveVPN(string) error                { return nil }
func (f *fakeBackend) ListRasEntries() ([]RasEntry, error) {
	return []RasEntry{{Name: "VPN Matriz", Monitored: true}}, nil
}
func (f *fakeBackend) GetConfig() config.Config         { return config.Empty() }
func (f *fakeBackend) SetGlobal(SetGlobalRequest) error { return nil }
func (f *fakeBackend) LogTail(n int) (string, error)    { return "linha\n", nil }
func (f *fakeBackend) Subscribe() (<-chan Message, func()) {
	return f.events, func() { f.mu.Lock(); f.cancels++; f.mu.Unlock() }
}

func startServer(t *testing.T, b Backend, tweak func(*Server)) (string, context.CancelFunc, chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Backend: b, AppVersion: "2.0.0-teste", HandshakeTimeout: time.Second, WriteTimeout: time.Second}
	if tweak != nil {
		tweak(s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	t.Cleanup(func() { cancel(); <-done })
	return ln.Addr().String(), cancel, done
}

func dial(t *testing.T, addr string) *Client {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	cl, err := Handshake(c, "teste")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	return cl
}

func TestServerRequests(t *testing.T) {
	b := &fakeBackend{}
	addr, _, _ := startServer(t, b, nil)
	cl := dial(t, addr)
	if cl.ServerApp != "2.0.0-teste" {
		t.Fatalf("hello: %q", cl.ServerApp)
	}
	var snap Snapshot
	if err := cl.Call(TypeStatus, nil, &snap); err != nil || snap.VPNs[0].State != "Conectada" {
		t.Fatalf("status: %+v %v", snap, err)
	}
	if err := cl.Call(TypeCheckNow, VPNRef{VPN: "Matriz"}, nil); err != nil {
		t.Fatal(err)
	}
	until := time.Date(2026, 10, 7, 15, 30, 0, 0, time.UTC).Unix()
	_ = cl.Call(TypePause, PauseRequest{VPN: "Matriz", UntilUnix: &until}, nil)
	_ = cl.Call(TypePause, PauseRequest{VPN: "Filial"}, nil)

	var e *Error
	if err := cl.Call(TypeReconnect, VPNRef{VPN: "Matriz"}, nil); !errors.As(err, &e) || e.Code != CodePaused {
		t.Fatalf("erro do backend passa como está: %v", err)
	}
	if err := cl.Call(TypeResume, VPNRef{VPN: "Matriz"}, nil); !errors.As(err, &e) || e.Code != CodeInternal {
		t.Fatalf("erro comum vira internal: %v", err)
	}
	if err := cl.Call(TypeSetEnabled, SetEnabledRequest{VPN: "x"}, nil); !errors.As(err, &e) || e.Code != CodeInternal {
		t.Fatalf("panic vira internal: %v", err)
	}
	if err := cl.Call("formatarDisco", nil, nil); !errors.As(err, &e) || e.Code != CodeUnknownType {
		t.Fatalf("tipo desconhecido: %v", err)
	}
	if err := cl.Call(TypeCheckNow, map[string]any{"vpn": "x", "extra": 1}, nil); !errors.As(err, &e) || e.Code != CodeBadRequest {
		t.Fatalf("payload estrito: %v", err)
	}
	var entries RasEntries
	if err := cl.Call(TypeListRasEntries, nil, &entries); err != nil || len(entries.Entries) != 1 {
		t.Fatal(entries, err)
	}
	// A conexão sobreviveu ao panic e aos erros.
	if err := cl.Call(TypeStatus, nil, nil); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	want := []string{"checkNow Matriz", "pause Matriz 2026-10-07T15:30:00Z", "pause Filial indefinida"}
	if strings.Join(b.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("chamadas %v", b.calls)
	}
}

func rawConn(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	return c, bufio.NewReader(c)
}

func expectErrorAndClose(t *testing.T, r *bufio.Reader, code string) {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil || !strings.Contains(line, `"code":"`+code+`"`) {
		t.Fatalf("esperava erro %s, veio %q %v", code, line, err)
	}
	if _, err := r.ReadString('\n'); err == nil {
		t.Fatal("conexão deveria ter sido fechada")
	}
}

func TestServerHandshakeRules(t *testing.T) {
	addr, _, _ := startServer(t, &fakeBackend{}, nil)

	c, r := rawConn(t, addr)
	c.Write([]byte(`{"v":1,"id":"1","type":"hello","payload":{"protocol":99,"appVersion":"x"}}` + "\n"))
	expectErrorAndClose(t, r, CodeIncompatible)

	c, r = rawConn(t, addr)
	c.Write([]byte(`{"v":1,"id":"1","type":"status"}` + "\n"))
	expectErrorAndClose(t, r, CodeBadRequest)

	c, r = rawConn(t, addr) // não manda nada: prazo do handshake
	if _, err := r.ReadString('\n'); err == nil {
		t.Fatal("sem hello a conexão deve cair")
	}
}

func TestServerMalformedAndOversized(t *testing.T) {
	addr, _, _ := startServer(t, &fakeBackend{}, nil)
	hello := `{"v":1,"id":"1","type":"hello","payload":{"protocol":1,"appVersion":"x"}}` + "\n"

	c, r := rawConn(t, addr)
	c.Write([]byte(hello + "{isso não é json\n"))
	r.ReadString('\n') // hello
	expectErrorAndClose(t, r, CodeBadRequest)

	c, r = rawConn(t, addr)
	c.Write([]byte(hello))
	r.ReadString('\n')
	go c.Write([]byte(strings.Repeat("a", MaxMessage+10) + "\n"))
	expectErrorAndClose(t, r, CodeBadRequest)
}

func TestServerMaxConnections(t *testing.T) {
	addr, _, _ := startServer(t, &fakeBackend{}, func(s *Server) { s.MaxConns = 2 })
	dial(t, addr)
	dial(t, addr)
	_, r := rawConn(t, addr)
	expectErrorAndClose(t, r, CodeBusy)
}

func TestServerSlowSubscriberIsDisconnected(t *testing.T) {
	b := &fakeBackend{events: make(chan Message, 1000)}
	addr, _, _ := startServer(t, b, func(s *Server) { s.OutQueue = 4 })
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cl, err := Handshake(c, "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.Call(TypeSubscribe, nil, nil); err != nil {
		t.Fatal(err)
	}
	big := MustMessage("", TypeLogTail, LogTail{Text: strings.Repeat("x", 60000)})
	for i := 0; i < 1000; i++ { // cliente não lê: a fila de saída enche
		b.events <- big
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		n := b.cancels
		b.mu.Unlock()
		if n == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("assinante lento deveria ter sido desconectado (e a assinatura cancelada)")
}

func TestServerStopsOnContextCancel(t *testing.T) {
	addr, cancel, done := startServer(t, &fakeBackend{}, nil)
	cl := dial(t, addr)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		done <- nil
	case <-time.After(2 * time.Second):
		t.Fatal("Serve não voltou")
	}
	if err := cl.Call(TypeStatus, nil, nil); err == nil {
		t.Fatal("conexões devem ser fechadas na parada")
	}
}

func TestServerIdleTimeoutSparesSubscribers(t *testing.T) {
	b := &fakeBackend{events: make(chan Message, 10)}
	addr, _, _ := startServer(t, b, func(s *Server) { s.IdleTimeout = 100 * time.Millisecond })
	idle := dial(t, addr)
	sub := dial(t, addr)
	if err := sub.Call(TypeSubscribe, nil, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := idle.Call(TypeStatus, nil, nil); err == nil {
		t.Fatal("conexão ociosa sem inscrição deveria ter sido fechada")
	}
	if err := sub.Call(TypeStatus, nil, nil); err != nil {
		t.Fatalf("conexão inscrita deve sobreviver à ociosidade: %v", err)
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/core/ipc/`  
Expected: FAIL — `undefined: Server`, `undefined: Handshake`…

- [ ] **Step 3: Implementar**

Servidor: handshake `hello` em até 5 s (protocolo diferente → `incompatible` e fecha); cada conexão sob `recover`; um escritor
por conexão com fila de 64 mensagens — fila cheia (cliente que não consome) desconecta; linha acima de 64 KB ou JSON inválido →
`bad_request` e fecha (o fluxo perdeu o sincronismo); 33ª conexão → `busy`; conexão sem pedido por `IdleTimeout` (2 min) é fechada,
exceto as inscritas em eventos (a bandeja fica ociosa por horas). Os testes rodam no Linux sobre TCP local.

`internal/core/ipc/server.go`:

```go
package ipc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"sync"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
)

// Backend é o que o serviço oferece pelo pipe. Erros *Error vão como estão;
// outros viram "internal".
type Backend interface {
	Status() Snapshot
	CheckNow(vpn string) error
	Reconnect(vpn string) error
	Pause(vpn string, until *time.Time) error
	Resume(vpn string) error
	SetEnabled(vpn string, enabled bool) error
	AddVPN(raw config.RawVPN) error
	UpdateVPN(name string, raw config.RawVPN) error
	RemoveVPN(name string) error
	ListRasEntries() ([]RasEntry, error)
	GetConfig() config.Config
	SetGlobal(req SetGlobalRequest) error
	LogTail(maxBytes int) (string, error)
	// Subscribe devolve eventos já como mensagens (o primeiro é o snapshot).
	// O canal é fechado se o assinante ficar para trás.
	Subscribe() (<-chan Message, func())
}

// Server atende conexões do pipe.
type Server struct {
	Backend          Backend
	AppVersion       string
	Log              *slog.Logger
	MaxConns         int
	HandshakeTimeout time.Duration
	WriteTimeout     time.Duration
	// IdleTimeout fecha conexões sem pedido nesse prazo, exceto as inscritas
	// em eventos (a bandeja fica ociosa por horas). Padrão 2 min.
	IdleTimeout time.Duration
	OutQueue    int
}

func (s *Server) defaults() {
	if s.MaxConns == 0 {
		s.MaxConns = MaxConns
	}
	if s.HandshakeTimeout == 0 {
		s.HandshakeTimeout = 5 * time.Second
	}
	if s.WriteTimeout == 0 {
		s.WriteTimeout = 5 * time.Second
	}
	if s.OutQueue == 0 {
		s.OutQueue = 64
	}
	if s.IdleTimeout == 0 {
		s.IdleTimeout = 2 * time.Minute
	}
	if s.Log == nil {
		s.Log = slog.New(slog.DiscardHandler)
	}
}

// Serve aceita conexões até ctx terminar; então fecha o listener e todas
// as conexões e espera os atendimentos voltarem.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.defaults()
	var (
		mu    sync.Mutex
		conns = map[net.Conn]struct{}{}
		wg    sync.WaitGroup
	)
	go func() {
		<-ctx.Done()
		ln.Close()
		mu.Lock()
		for c := range conns {
			c.Close()
		}
		mu.Unlock()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			wg.Wait()
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		mu.Lock()
		full := len(conns) >= s.MaxConns
		if !full {
			conns[c] = struct{}{}
		}
		mu.Unlock()
		if full {
			_ = c.SetWriteDeadline(time.Now().Add(time.Second))
			_ = NewCodec(c).Write(ErrorMessage("", &Error{Code: CodeBusy, Message: "conexões demais"}))
			c.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				mu.Lock()
				delete(conns, c)
				mu.Unlock()
				c.Close()
			}()
			s.handle(ctx, c)
		}()
	}
}

type conn struct {
	s      *Server
	c      net.Conn
	codec  *Codec
	out    chan Message
	closed chan struct{}
	once   sync.Once
}

func (cn *conn) close() { cn.once.Do(func() { close(cn.closed); cn.c.Close() }) }

// send enfileira; fila cheia = cliente que não consome: desconecta.
func (cn *conn) send(m Message) {
	select {
	case cn.out <- m:
	case <-cn.closed:
	default:
		cn.s.Log.Warn("cliente do pipe lento; desconectando")
		cn.close()
	}
}

func (cn *conn) writer() {
	for {
		select {
		case <-cn.closed:
			return
		case m := <-cn.out:
			_ = cn.c.SetWriteDeadline(time.Now().Add(cn.s.WriteTimeout))
			if err := cn.codec.Write(m); err != nil {
				cn.close()
				return
			}
		}
	}
}

func (s *Server) handle(ctx context.Context, c net.Conn) {
	cn := &conn{s: s, c: c, codec: NewCodec(c), out: make(chan Message, s.OutQueue), closed: make(chan struct{})}
	defer cn.close()
	defer func() {
		if r := recover(); r != nil {
			s.Log.Error("panic numa conexão do pipe", "panic", fmt.Sprint(r), "pilha", string(debug.Stack()))
		}
	}()
	go cn.writer()

	_ = c.SetReadDeadline(time.Now().Add(s.HandshakeTimeout))
	m, err := cn.codec.Read()
	if err != nil {
		return
	}
	var h Hello
	if m.Type != TypeHello || DecodePayload(m.Payload, &h) != nil {
		cn.send(ErrorMessage(m.ID, &Error{Code: CodeBadRequest, Message: "esperava hello"}))
		cn.flush()
		return
	}
	if h.Protocol != ProtocolVersion {
		cn.send(ErrorMessage(m.ID, &Error{Code: CodeIncompatible,
			Message: fmt.Sprintf("protocolo %d incompatível com o serviço (%d); atualize o VPN Monitor", h.Protocol, ProtocolVersion)}))
		cn.flush()
		return
	}
	cn.send(MustMessage(m.ID, TypeHello, Hello{Protocol: ProtocolVersion, AppVersion: s.AppVersion}))

	var unsubscribe func()
	defer func() {
		if unsubscribe != nil {
			unsubscribe()
		}
	}()
	for {
		if unsubscribe == nil {
			_ = c.SetReadDeadline(time.Now().Add(s.IdleTimeout))
		} else {
			_ = c.SetReadDeadline(time.Time{}) // inscrito: pode ficar ocioso
		}
		m, err := cn.codec.Read()
		if err != nil {
			// Linha grande demais ou inválida: responde e encerra, porque o
			// fluxo perdeu o sincronismo. Outros erros = cliente saiu.
			var de *DecodeError
			if errors.Is(err, ErrTooLarge) || errors.As(err, &de) {
				cn.send(ErrorMessage("", &Error{Code: CodeBadRequest, Message: err.Error()}))
				cn.flush()
			}
			return
		}
		if m.Type == TypeSubscribe {
			if unsubscribe != nil {
				cn.send(MustMessage(m.ID, TypeOK, nil))
				continue
			}
			events, cancel := s.Backend.Subscribe()
			unsubscribe = cancel
			cn.send(MustMessage(m.ID, TypeOK, nil))
			go func() {
				for {
					select {
					case ev, ok := <-events:
						if !ok {
							cn.close() // ficou para trás no barramento
							return
						}
						cn.send(ev)
					case <-cn.closed:
						return
					case <-ctx.Done():
						return
					}
				}
			}()
			continue
		}
		cn.send(s.dispatch(m))
	}
}

// flush dá ao escritor a chance de enviar a última resposta antes de fechar.
func (cn *conn) flush() {
	deadline := time.After(cn.s.WriteTimeout)
	for len(cn.out) > 0 {
		select {
		case <-deadline:
			return
		case <-cn.closed:
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
	time.Sleep(10 * time.Millisecond)
}

func (s *Server) dispatch(m Message) (resp Message) {
	defer func() {
		if r := recover(); r != nil {
			s.Log.Error("panic atendendo pedido", "tipo", m.Type, "panic", fmt.Sprint(r), "pilha", string(debug.Stack()))
			resp = ErrorMessage(m.ID, &Error{Code: CodeInternal, Message: "erro interno"})
		}
	}()
	result, err := s.call(m)
	if err != nil {
		var e *Error
		if !errors.As(err, &e) {
			e = &Error{Code: CodeInternal, Message: err.Error()}
		}
		return ErrorMessage(m.ID, e)
	}
	out, merr := NewMessage(m.ID, TypeOK, result)
	if merr != nil {
		return ErrorMessage(m.ID, &Error{Code: CodeInternal, Message: merr.Error()})
	}
	return out
}

func (s *Server) call(m Message) (any, error) {
	b := s.Backend
	switch m.Type {
	case TypeStatus:
		return b.Status(), nil
	case TypeCheckNow, TypeReconnect, TypeResume:
		var r VPNRef
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		switch m.Type {
		case TypeCheckNow:
			return nil, b.CheckNow(r.VPN)
		case TypeReconnect:
			return nil, b.Reconnect(r.VPN)
		}
		return nil, b.Resume(r.VPN)
	case TypePause:
		var r PauseRequest
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		var until *time.Time
		if r.UntilUnix != nil {
			t := time.Unix(*r.UntilUnix, 0)
			until = &t
		}
		return nil, b.Pause(r.VPN, until)
	case TypeSetEnabled:
		var r SetEnabledRequest
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		return nil, b.SetEnabled(r.VPN, r.Enabled)
	case TypeAddVPN:
		var r AddVPNRequest
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		return nil, b.AddVPN(r.Config)
	case TypeUpdateVPN:
		var r UpdateVPNRequest
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		return nil, b.UpdateVPN(r.Name, r.Config)
	case TypeRemoveVPN:
		var r RemoveVPNRequest
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		return nil, b.RemoveVPN(r.Name)
	case TypeListRasEntries:
		e, err := b.ListRasEntries()
		return RasEntries{Entries: e}, err
	case TypeGetConfig:
		return b.GetConfig(), nil
	case TypeSetGlobal:
		var r SetGlobalRequest
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		return nil, b.SetGlobal(r)
	case TypeLogTail:
		var r LogTailRequest
		if err := DecodePayload(m.Payload, &r); err != nil {
			return nil, err
		}
		if r.MaxBytes <= 0 || r.MaxBytes > MaxMessage-1024 {
			r.MaxBytes = MaxMessage - 1024
		}
		t, err := b.LogTail(r.MaxBytes)
		return LogTail{Text: t}, err
	}
	return nil, &Error{Code: CodeUnknownType, Message: fmt.Sprintf("tipo %q desconhecido", m.Type)}
}
```

`internal/core/ipc/client.go`:

```go
package ipc

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"
)

// Client é um cliente síncrono simples (CLI). A bandeja (Marco B) terá o
// seu, com reconexão e eventos.
type Client struct {
	conn       net.Conn
	codec      *Codec
	next       int
	ServerApp  string
	CallTimout time.Duration
}

// Handshake troca hello sobre uma conexão já aberta.
func Handshake(c net.Conn, appVersion string) (*Client, error) {
	cl := &Client{conn: c, codec: NewCodec(c), CallTimout: 30 * time.Second}
	var h Hello
	if err := cl.Call(TypeHello, Hello{Protocol: ProtocolVersion, AppVersion: appVersion}, &h); err != nil {
		c.Close()
		return nil, err
	}
	cl.ServerApp = h.AppVersion
	return cl, nil
}

// Call envia um pedido e espera a resposta com o mesmo id. Eventos que
// chegarem no meio são ignorados. out pode ser nil.
func (c *Client) Call(typ string, payload any, out any) error {
	c.next++
	id := strconv.Itoa(c.next)
	m, err := NewMessage(id, typ, payload)
	if err != nil {
		return err
	}
	_ = c.conn.SetDeadline(time.Now().Add(c.CallTimout))
	defer c.conn.SetDeadline(time.Time{})
	if err := c.codec.Write(m); err != nil {
		return err
	}
	for {
		r, err := c.codec.Read()
		if err != nil {
			return err
		}
		if r.ID != id {
			continue
		}
		switch r.Type {
		case TypeError:
			var e Error
			if err := json.Unmarshal(r.Payload, &e); err != nil {
				return fmt.Errorf("resposta de erro ilegível: %w", err)
			}
			return &e
		case TypeOK, TypeHello:
			if out != nil && len(r.Payload) > 0 {
				return json.Unmarshal(r.Payload, out)
			}
			return nil
		}
		return fmt.Errorf("resposta inesperada %q", r.Type)
	}
}

// Close fecha a conexão.
func (c *Client) Close() error { return c.conn.Close() }
```

`internal/core/ipc/pipe_windows.go`: `winio.ListenPipe` com `PipeSDDL` (o go-winio já cria a primeira instância e rejeita clientes remotos). `Dial` confere
`GetNamedPipeServerProcessId` contra `svc.ServicePID()` e recusa divergência (§6.1).

```go
//go:build windows

package ipc

import (
	"context"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/svc"
)

// Listen cria o pipe do serviço com a ACL da §6.1. O go-winio já cria a
// primeira instância com FILE_FLAG_FIRST_PIPE_INSTANCE e rejeita clientes
// remotos (PIPE_REJECT_REMOTE_CLIENTS).
func Listen() (net.Listener, error) {
	return winio.ListenPipe(PipeName, &winio.PipeConfig{
		SecurityDescriptor: PipeSDDL,
		InputBufferSize:    MaxMessage,
		OutputBufferSize:   MaxMessage,
	})
}

// Dial conecta ao serviço e confere que o servidor do pipe é o processo do
// serviço VPNMonitor (PID informado pelo SCM).
func Dial(ctx context.Context) (net.Conn, error) {
	return dialVerified(ctx, PipeName, svc.ServicePID)
}

func dialVerified(ctx context.Context, name string, expected func() (uint32, error)) (net.Conn, error) {
	c, err := winio.DialPipeContext(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("serviço VPN Monitor inacessível: %w", err)
	}
	fd, ok := c.(interface{ Fd() uintptr })
	if !ok {
		c.Close()
		return nil, fmt.Errorf("conexão de pipe sem handle")
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(fd.Fd()), &pid); err != nil {
		c.Close()
		return nil, fmt.Errorf("consultando o servidor do pipe: %w", err)
	}
	want, err := expected()
	if err != nil {
		c.Close()
		return nil, err
	}
	if pid != want {
		c.Close()
		return nil, fmt.Errorf("o pipe %s é servido pelo PID %d, não pelo serviço (PID %d); conexão recusada", name, pid, want)
	}
	return c, nil
}
```

`internal/core/ipc/pipe_other.go`:

```go
//go:build !windows

package ipc

import (
	"context"
	"net"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform"
)

// Listen fora do Windows não há named pipe.
func Listen() (net.Listener, error) { return nil, platform.ErrNotSupported }

// Dial fora do Windows não há named pipe.
func Dial(context.Context) (net.Conn, error) { return nil, platform.ErrNotSupported }
```

`internal/core/ipc/pipe_windows_test.go`: Só no job `test-windows`: (1) DACL efetiva do pipe real — `NU` negado e a ACE de `IU` **sem** `FILE_CREATE_PIPE_INSTANCE`;
(2) conferência de PID (o próprio passa, outro é recusado); (3) cliente sem permissão: token restrito (`CreateRestrictedToken` com
Administradores, Interativos e SYSTEM como "somente negação"), personificado numa thread travada, recebe `ERROR_ACCESS_DENIED`.
Sem privilégio para criar ou personificar o token, o teste faz `t.Skip` com o motivo.

```go
//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listenTestPipe(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf(`\\.\pipe\vpnmon-teste-%d-%d`, os.Getpid(), time.Now().UnixNano())
	ln, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: PipeSDDL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return name
}

// Só no job Windows: a DACL efetiva do pipe nega Rede e dá a Usuários
// Interativos leitura e escrita SEM FILE_CREATE_PIPE_INSTANCE.
func TestWindowsPipeDACL(t *testing.T) {
	name := listenTestPipe(t)
	sd, err := windows.GetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	s := sd.String()
	if !strings.Contains(s, "(D;;") || !strings.Contains(s, ";;;NU)") {
		t.Fatalf("Rede deveria ser negada: %s", s)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	iu, err := windows.CreateWellKnownSid(windows.WinInteractiveSid)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !sid.Equals(iu) {
			continue
		}
		found = true
		if uint32(ace.Mask)&FileCreatePipeInstance != 0 {
			t.Fatalf("IU pode criar instância do pipe: máscara %#x", ace.Mask)
		}
	}
	if !found {
		t.Fatalf("sem ACE para IU: %s", s)
	}
}

// Só no job Windows: conferência de PID (o próprio PID passa, outro é recusado).
func TestWindowsPipePIDCheck(t *testing.T) {
	name := listenTestPipe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	self := func() (uint32, error) { return uint32(os.Getpid()), nil }
	c, err := dialVerified(ctx, name, self)
	if err != nil {
		t.Fatalf("PID do próprio processo deveria passar: %v", err)
	}
	c.Close()
	other := func() (uint32, error) { return 4, nil }
	if _, err := dialVerified(ctx, name, other); err == nil {
		t.Fatal("PID diferente do serviço deve ser recusado")
	}
}

var procCreateRestrictedToken = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")

// Só no job Windows: um cliente sem nenhum dos grupos da ACL (Administradores
// e Usuários Interativos viram "somente negação" num token restrito) é
// recusado pelo pipe com ERROR_ACCESS_DENIED.
func TestWindowsPipeRefusesClientWithoutPermission(t *testing.T) {
	name := listenTestPipe(t)

	var proc windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_IMPERSONATE, &proc); err != nil {
		t.Skipf("sem acesso ao token do processo: %v", err)
	}
	defer proc.Close()
	var disable []windows.SIDAndAttributes
	for _, k := range []windows.WELL_KNOWN_SID_TYPE{windows.WinBuiltinAdministratorsSid, windows.WinInteractiveSid, windows.WinLocalSystemSid} {
		sid, err := windows.CreateWellKnownSid(k)
		if err != nil {
			t.Fatal(err)
		}
		disable = append(disable, windows.SIDAndAttributes{Sid: sid})
	}
	const disableMaxPrivilege = 0x1
	var restricted windows.Token
	r, _, e := procCreateRestrictedToken.Call(uintptr(proc), disableMaxPrivilege,
		uintptr(len(disable)), uintptr(unsafe.Pointer(&disable[0])), 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)))
	if r == 0 {
		t.Skipf("CreateRestrictedToken indisponível: %v", e)
	}
	defer restricted.Close()
	var imp windows.Token
	if err := windows.DuplicateTokenEx(restricted, windows.MAXIMUM_ALLOWED, nil,
		windows.SecurityImpersonation, windows.TokenImpersonation, &imp); err != nil {
		t.Skipf("DuplicateTokenEx: %v", err)
	}
	defer imp.Close()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.SetThreadToken(nil, imp); err != nil {
		t.Skipf("sem privilégio para personificar: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := winio.DialPipeContext(ctx, name)
	_ = windows.RevertToSelf()
	if err == nil {
		c.Close()
		t.Fatal("cliente sem permissão conectou no pipe")
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("esperava acesso negado, veio %v", err)
	}
}
```

- [ ] **Step 4: Dependências**

```bash
go get github.com/Microsoft/go-winio@v0.6.2
go mod tidy
```

- [ ] **Step 5: Rodar os testes e confirmar que passam**

Run: `go test -race -count=3 ./internal/core/ipc/`  
Expected: PASS

Run: `go vet ./... && GOOS=windows go vet ./...`  
Expected: sem saída

Run: `GOOS=windows go test -c -o /dev/null ./internal/core/ipc/`  
Expected: compila

Run: `make lint`  
Expected: sem erros (go.mod agora requer go-winio v0.6.2)

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/core/ipc && git commit -m "feat(ipc): servidor com limites e desconexão de cliente lento, cliente da CLI e named pipe com ACL e conferência de PID"
```

---

### Task 21: monitor/service: orquestrador (supervisores, barramento, recuperação, comandos)

**Files:**
- Create: `internal/features/monitor/service/bus.go`
- Create: `internal/features/monitor/service/view.go`
- Create: `internal/features/monitor/service/orchestrator.go`
- Test: `internal/features/monitor/service/orchestrator_test.go`
- Test: `internal/features/monitor/service/helpers_test.go`

**Interfaces:**
- Consumes: `Supervisor`, `Deps`, `Update`, `DialQueue`, interfaces `LinkProber`/`Dialer`/`Fingerprinter` (17–18); `ipc.*` (19); `config.*`; `logging.EventSink`, `logging.ForVPN`, `logging.RecordingSink`; `adapters.NewChecker`; `ras.Client`; `icmp.Pinger`; `shared.ResumeDetector`; `stubWorld` de `supervisor_test.go`.
- Produces: `service.Paths{ConfigFile, StateFile, LogFile string}`; `service.Options{Paths; Clock; RAS; Pinger; TCPDial; Link; Dialer; Creds; Log; Events; Rand; OnGlobals func(config.Config); RestartDelay; CommandTimeout; StopTimeout}`;
  `service.New(Options, config.Config, config.State) *Orchestrator` com `Start(ctx)`, `Stop()`, `Status() ipc.Snapshot`, `Subscribe()`, `Wake()`, `PowerResume()`,
  `CredentialsChanged()`, `CheckNow(name)`, `Reconnect(name)`, `Pause(name, *time.Time)`, `Resume(name)`; `service.ToView(config.VPN, domain.Status, now) ipc.VPNView`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/monitor/service/orchestrator_test.go`:

```go
package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/fake"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/adapters"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

type orchHarness struct {
	t      *testing.T
	o      *Orchestrator
	w      *stubWorld
	clk    *shared.FakeClock
	events *logging.RecordingSink
	paths  Paths
	ras    *fake.RAS
	pinger *fake.Pinger
	cancel context.CancelFunc
}

func vpnNamed(name string) config.VPN {
	return config.RawVPN{Name: name, RasEntry: "VPN " + name,
		Check: &config.RawCheck{Kind: config.CheckPing, Host: "10.0.0.1"}}.Normalize()
}

func cfgWith(vpns ...config.VPN) config.Config {
	c := config.Empty()
	c.VPNs = vpns
	return c
}

func newOrch(t *testing.T, w *stubWorld, cfg config.Config, st config.State) *orchHarness {
	t.Helper()
	return newOrchWith(t, w, cfg, st, nil)
}

func newOrchWith(t *testing.T, w *stubWorld, cfg config.Config, st config.State, tweak func(*Options)) *orchHarness {
	t.Helper()
	dir := t.TempDir()
	h := &orchHarness{t: t, w: w, clk: shared.NewFakeClock(t0), events: &logging.RecordingSink{},
		paths: Paths{ConfigFile: filepath.Join(dir, "config.json"), StateFile: filepath.Join(dir, "state.json"),
			LogFile: filepath.Join(dir, "vpnmon.log")},
		ras: fake.NewRAS("VPN Matriz", "VPN Filial", "VPN Backup")}
	if _, err := config.Save(h.paths.ConfigFile, cfg); err != nil {
		t.Fatal(err)
	}
	h.pinger = fake.NewPinger()
	h.pinger.SetReachable("10.0.0.1", true)
	opts := Options{
		Paths: h.paths, Clock: h.clk, RAS: h.ras, Pinger: h.pinger, Link: w, Dialer: w, Creds: w,
		Events: h.events,
	}
	if tweak != nil {
		tweak(&opts)
	}
	h.o = New(opts, cfg, st)
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.o.Start(ctx)
	t.Cleanup(func() { cancel(); h.o.Stop() })
	return h
}

func (h *orchHarness) waitView(name string, state domain.State) ipc.VPNView {
	h.t.Helper()
	return h.waitViewWhere(name, func(v ipc.VPNView) bool { return v.State == string(state) })
}

// waitViewWhere espera a VPN satisfazer cond (estado e campos juntos, para
// não pegar um retrato intermediário).
func (h *orchHarness) waitViewWhere(name string, cond func(ipc.VPNView) bool) ipc.VPNView {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, v := range h.o.Status().VPNs {
			if v.Name == name && cond(v) {
				return v
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.t.Fatalf("%s não chegou à condição esperada: %+v", name, h.o.Status())
	return ipc.VPNView{}
}

func (h *orchHarness) supOf(name string) *Supervisor {
	h.o.mu.Lock()
	defer h.o.mu.Unlock()
	return h.o.sups[config.NameKey(name)].sup
}

func TestOrchestratorStartsOnePerVPN(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz"), vpnNamed("Filial")), config.State{})
	h.waitView("Matriz", domain.Conectada)
	v := h.waitView("Filial", domain.Conectada)
	if v.Entry != "VPN Filial" || v.LatencyMs != 12 || v.CheckKind != "ping" {
		t.Fatalf("view %+v", v)
	}
}

func TestOrchestratorPartialReload(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz"), vpnNamed("Filial")), config.State{})
	h.waitView("Matriz", domain.Conectada)
	h.waitView("Filial", domain.Conectada)
	matriz, filial := h.supOf("Matriz"), h.supOf("Filial")

	changed := vpnNamed("Filial")
	changed.IntervalSeconds = 60
	h.o.applyMu.Lock()
	h.o.apply(cfgWith(vpnNamed("Matriz"), changed, vpnNamed("Backup")))
	h.o.applyMu.Unlock()
	if h.supOf("Matriz") != matriz {
		t.Fatal("VPN sem mudança não pode ser reiniciada")
	}
	if h.supOf("Filial") == filial {
		t.Fatal("VPN alterada deve ser reiniciada")
	}
	h.waitView("Backup", domain.Conectada)

	h.o.applyMu.Lock()
	h.o.apply(cfgWith(vpnNamed("Matriz")))
	h.o.applyMu.Unlock()
	if n := len(h.o.Status().VPNs); n != 1 {
		t.Fatalf("removidas continuam: %d", n)
	}
}

func TestOrchestratorPausePersistsAcrossRestart(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitView("Matriz", domain.Conectada)
	if err := h.o.Pause("matriz", nil); err != nil {
		t.Fatal(err)
	}
	h.waitView("Matriz", domain.Pausada)
	st, err := config.LoadState(h.paths.StateFile, t0)
	if err != nil || !st.Pauses["matriz"].Indefinite {
		t.Fatalf("state.json: %+v %v", st, err)
	}
	var e *ipc.Error
	if err := h.o.Reconnect("Matriz"); !asIPC(err, &e) || e.Code != ipc.CodePaused {
		t.Fatalf("reconnect em pausa: %v", err)
	}
	if err := h.o.CheckNow("Nenhuma"); !asIPC(err, &e) || e.Code != ipc.CodeNotFound {
		t.Fatalf("VPN inexistente: %v", err)
	}
	past := t0.Add(-time.Minute)
	if err := h.o.Pause("Matriz", &past); !asIPC(err, &e) || e.Code != ipc.CodeBadRequest {
		t.Fatalf("pausa no passado: %v", err)
	}

	h2 := newOrch(t, w, cfgWith(vpnNamed("Matriz")), st)
	h2.waitView("Matriz", domain.Pausada)
}

func TestOrchestratorRecoversPanic(t *testing.T) {
	w := &stubWorld{up: true, network: true, panicOn: "probe"}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	deadline := time.Now().Add(2 * time.Second)
	for len(h.events.Snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	ev := h.events.Snapshot()
	if len(ev) != 1 || ev[0].Level != "error" || !strings.Contains(ev[0].Msg, "pânico") {
		t.Fatalf("Event Log: %+v", ev)
	}
	if !h.clk.WaitForDeadline(5*time.Second, time.Second) {
		t.Fatal("recriação não agendada para 5 s")
	}
	h.clk.Advance(5 * time.Second)
	h.waitView("Matriz", domain.Conectada)
}

func TestOrchestratorSubscribeAndStopping(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	events, cancel := h.o.Subscribe()
	defer cancel()
	first := <-events
	if first.Type != ipc.TypeSnapshot {
		t.Fatalf("primeiro evento %s", first.Type)
	}
	h.waitView("Matriz", domain.Conectada)
	// A VPN pode ter conectado antes da inscrição (aí já veio no snapshot);
	// uma verificação garante um vpnState depois dela.
	if err := h.o.CheckNow("Matriz"); err != nil {
		t.Fatal(err)
	}
	h.waitView("Matriz", domain.Conectada)
	h.o.Stop()
	sawState, sawStopping := false, false
	for m := range drain(events) {
		sawState = sawState || m.Type == ipc.TypeVPNState
		sawStopping = sawStopping || m.Type == ipc.TypeServiceStopping
	}
	if !sawState || !sawStopping {
		t.Fatalf("vpnState=%v serviceStopping=%v", sawState, sawStopping)
	}
}

func TestOrchestratorClockJumpResetsBackoff(t *testing.T) {
	w := &stubWorld{network: true, outcomes: []adapters.DialOutcome{{Err: &domain.DialError{Code: 809}}}}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	v := h.waitViewWhere("Matriz", func(v ipc.VPNView) bool { return v.NextAttemptUnix != 0 })
	if v.State != string(domain.Reconectando) || v.NextAttemptUnix != t0.Add(30*time.Second).Unix() {
		t.Fatalf("primeiro backoff: %+v", v)
	}
	h.clk.Suspend(2 * time.Hour)   // máquina dormiu
	h.clk.Advance(5 * time.Second) // tique do detector: salto de relógio
	if !h.clk.WaitForDeadline(5*time.Second, time.Second) {
		t.Fatal("retomada deveria agendar verificação em 5 s")
	}
	h.clk.Advance(5 * time.Second)
	h.waitView("Matriz", domain.Conectada)
	if n := len(w.get().dials); n != 2 {
		t.Fatalf("discagens = %d", n)
	}
}

func drain(ch <-chan ipc.Message) <-chan ipc.Message {
	out := make(chan ipc.Message, 100)
	go func() {
		defer close(out)
		for {
			select {
			case m, ok := <-ch:
				if !ok {
					return
				}
				out <- m
			case <-time.After(50 * time.Millisecond):
				return
			}
		}
	}()
	return out
}

func TestStopWithHungEchoIsFast(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.pinger.SetHang(true) // IcmpSendEcho2 preso, ignorando ctx
	defer h.pinger.SetHang(false)
	deadline := time.Now().Add(2 * time.Second)
	for h.pinger.Calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	h.o.Stop()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("parada com eco preso levou %s", d)
	}
	if _, err := os.Stat(h.paths.StateFile); err != nil {
		t.Fatal("state.json deveria ser gravado")
	}
}

func TestStopDeadlineWithHungSupervisor(t *testing.T) {
	hang := make(chan struct{})
	defer close(hang)
	w := &stubWorld{network: true, hang: hang} // discador preso ignorando ctx
	h := newOrchWith(t, w, cfgWith(vpnNamed("Matriz")), config.State{}, func(o *Options) { o.StopTimeout = 200 * time.Millisecond })
	deadline := time.Now().Add(2 * time.Second)
	for len(w.get().dials) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	h.o.Stop()
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Stop deve respeitar o prazo global, levou %s", d)
	}
	if _, err := os.Stat(h.paths.StateFile); err != nil {
		t.Fatal("state.json deveria ser gravado mesmo com supervisor preso")
	}
}

func TestRemovedVPNPauseLeavesStateFile(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz"), vpnNamed("Filial")), config.State{})
	h.waitView("Filial", domain.Conectada)
	if err := h.o.Pause("Filial", nil); err != nil {
		t.Fatal(err)
	}
	h.waitView("Filial", domain.Pausada)
	h.o.applyMu.Lock()
	h.o.apply(cfgWith(vpnNamed("Matriz")))
	h.o.applyMu.Unlock()
	st, err := config.LoadState(h.paths.StateFile, t0)
	if err != nil || len(st.Pauses) != 0 {
		t.Fatalf("pausa da VPN removida continua em state.json: %+v %v", st, err)
	}
}
```

`internal/features/monitor/service/helpers_test.go`:

```go
package service

import (
	"errors"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

func asIPC(err error, target **ipc.Error) bool { return errors.As(err, target) }
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/features/monitor/service/`  
Expected: FAIL — `undefined: New`, `undefined: Options`…

- [ ] **Step 3: Implementar**

`apply` reinicia só as VPNs cujo trecho mudou (`config.VPN` é comparável) e, ao remover uma VPN pausada, regrava `state.json` sem a
pausa dela; `Stop` encerra supervisores com **prazo global** (`StopTimeout`, 7 s em tempo real — quem não voltar é abandonado e
registrado), publica `serviceStopping` e grava `state.json` mesmo assim, sem desligar VPNs conectadas (o SCM espera 10 s). Panic de supervisor → log + Event Log e recriação após 5 s
(dobrando até 60 s; zera se rodou mais de 1 min). Atualizações de supervisores antigos são ignoradas. O detector de salto de relógio
(tique de 5 s, limite 2× o menor intervalo) chama `PowerResume`. Assinantes recebem o snapshot primeiro, registrados sob o mesmo lock que
publica, para não perder evento.

`internal/features/monitor/service/bus.go`:

```go
package service

import (
	"sync"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

// bus distribui eventos aos assinantes do pipe. Assinante cuja fila enche
// é removido e tem o canal fechado (o servidor IPC então o desconecta).
type bus struct {
	mu   sync.Mutex
	subs map[int]chan ipc.Message
	next int
	size int
}

func newBus(size int) *bus { return &bus{subs: map[int]chan ipc.Message{}, size: size} }

func (b *bus) subscribe(first ipc.Message) (<-chan ipc.Message, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan ipc.Message, b.size)
	ch <- first
	id := b.next
	b.next++
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
	}
}

func (b *bus) publish(m ipc.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, ch := range b.subs {
		select {
		case ch <- m:
		default:
			delete(b.subs, id)
			close(ch)
		}
	}
}

func (b *bus) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
```

`internal/features/monitor/service/view.go`:

```go
package service

import (
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
)

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// ToView converte o estado de uma VPN para o protocolo.
func ToView(v config.VPN, s domain.Status, now time.Time) ipc.VPNView {
	view := ipc.VPNView{
		Name: v.Name, Entry: v.RasEntry, Enabled: v.Enabled, CheckKind: string(v.Check.Kind),
		State: string(s.State), SinceUnix: unix(s.Since), LastCheckUnix: unix(s.LastCheck),
		LatencyMs: s.LastRTT.Milliseconds(), Failures: s.Failures, Attempt: s.Attempt,
		Reconnects24h: s.Reconnects24h(now), PausedUntilUnix: unix(s.PausedUntil),
		PausedIndefinite: s.PausedIndefinite,
	}
	if s.NextAttempt.After(now) {
		view.NextAttemptUnix = s.NextAttempt.Unix()
	}
	if e := s.LastErr; e != nil {
		view.LastError = &ipc.ErrorInfo{Class: e.Class.String(), Code: e.Code, Message: e.Message}
	}
	return view
}
```

`internal/features/monitor/service/orchestrator.go`:

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/icmp"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/adapters"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// Paths são os arquivos do serviço.
type Paths struct {
	ConfigFile string
	StateFile  string
	LogFile    string
}

// Options são as dependências do orquestrador.
type Options struct {
	Paths   Paths
	Clock   shared.Clock
	RAS     ras.Client
	Pinger  icmp.Pinger
	TCPDial adapters.DialFunc
	Link    LinkProber
	Dialer  Dialer
	Creds   Fingerprinter
	Log     *slog.Logger
	Events  logging.EventSink
	Rand    func() float64
	// OnGlobals aplica logLevel e limites de log quando a config muda.
	OnGlobals func(config.Config)
	// RestartDelay é a espera antes de recriar um supervisor que entrou em
	// pânico (dobra a cada repetição, até 60 s). Padrão 5 s.
	RestartDelay time.Duration
	// CommandTimeout limita a espera por um supervisor. Padrão 5 s.
	CommandTimeout time.Duration
	// StopTimeout é o prazo para os supervisores pararem em Stop (e numa
	// recarga). Padrão 7 s, deixando folga nos 10 s que o SCM espera.
	StopTimeout time.Duration
}

type running struct {
	vpn    config.VPN
	sup    *Supervisor
	last   domain.Status
	cancel context.CancelFunc
	done   chan struct{}
}

// Orchestrator mantém um supervisor por VPN, distribui eventos e é o
// backend do pipe.
type Orchestrator struct {
	opts    Options
	queue   *DialQueue
	bus     *bus
	applyMu sync.Mutex // serializa Apply e mutações de config

	mu          sync.Mutex
	ctx         context.Context
	cfg         config.Config
	state       config.State
	sups        map[string]*running
	lastWritten string // hash do último config.json gravado pelo serviço
	// diskInvalid é o problema do config.json em disco, enquanto ele estiver
	// inválido; nesse estado as mudanças pelo pipe são recusadas para não
	// sobrescrever a edição manual (ou o arquivo inteiro, se inválido desde a partida).
	diskInvalid error
}

// New cria o orquestrador com a config e o estado iniciais.
func New(opts Options, cfg config.Config, st config.State) *Orchestrator {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.Events == nil {
		opts.Events = logging.NopSink{}
	}
	if opts.RestartDelay == 0 {
		opts.RestartDelay = 5 * time.Second
	}
	if opts.CommandTimeout == 0 {
		opts.CommandTimeout = 5 * time.Second
	}
	if opts.StopTimeout == 0 {
		opts.StopTimeout = 7 * time.Second
	}
	if opts.OnGlobals == nil {
		opts.OnGlobals = func(config.Config) {}
	}
	if st.Pauses == nil {
		st.Pauses = map[string]config.Pause{}
	}
	return &Orchestrator{opts: opts, queue: &DialQueue{}, bus: newBus(256), cfg: cfg, state: st, sups: map[string]*running{}}
}

// Start sobe os supervisores e o detector de retomada. Não bloqueia.
func (o *Orchestrator) Start(ctx context.Context) {
	o.mu.Lock()
	o.ctx = ctx
	cfg := o.cfg
	o.mu.Unlock()
	o.applyMu.Lock()
	o.apply(cfg)
	o.applyMu.Unlock()
	go o.watchResume(ctx)
}

// Stop encerra os supervisores (discagens em curso são desligadas), avisa
// os assinantes e grava o estado. VPNs conectadas continuam de pé.
func (o *Orchestrator) Stop() {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	o.mu.Lock()
	all := make([]*running, 0, len(o.sups))
	for k, r := range o.sups {
		all = append(all, r)
		delete(o.sups, k)
	}
	o.mu.Unlock()
	o.stopAll(all)
	o.bus.publish(ipc.MustMessage("", ipc.TypeServiceStopping, struct{}{}))
	o.mu.Lock()
	st := o.state
	o.mu.Unlock()
	if err := config.SaveState(o.opts.Paths.StateFile, st); err != nil {
		o.opts.Log.Error("gravando state.json", "erro", err)
	}
}

// stopAll cancela os supervisores e espera no máximo StopTimeout. Quem não
// voltar no prazo é abandonado (registrado no log): o estado é gravado mesmo
// assim e atualizações tardias são ignoradas por onUpdate.
func (o *Orchestrator) stopAll(rs []*running) {
	for _, r := range rs {
		r.cancel()
	}
	// Prazo em tempo real (não o Clock injetável): é o SCM que está esperando.
	t := time.NewTimer(o.opts.StopTimeout)
	defer t.Stop()
	for _, r := range rs {
		select {
		case <-r.done:
		case <-t.C:
			o.opts.Log.Warn("supervisor não parou no prazo; seguindo sem ele", "vpn", r.vpn.Name)
		}
	}
}

// apply cria, remove ou reinicia só as VPNs cujo trecho mudou (§4.9).
// Chamar com applyMu travado.
func (o *Orchestrator) apply(cfg config.Config) {
	o.mu.Lock()
	o.cfg = cfg
	var stop []*running
	var start []config.VPN
	keep := map[string]bool{}
	for _, v := range cfg.VPNs {
		key := config.NameKey(v.Name)
		keep[key] = true
		if r, ok := o.sups[key]; ok {
			if r.vpn == v {
				continue
			}
			stop = append(stop, r)
			delete(o.sups, key)
		}
		start = append(start, v)
	}
	pausesChanged := false
	for key, r := range o.sups {
		if !keep[key] {
			stop = append(stop, r)
			delete(o.sups, key)
		}
	}
	for key := range o.state.Pauses {
		if !keep[key] {
			delete(o.state.Pauses, key)
			pausesChanged = true
		}
	}
	if pausesChanged {
		if err := config.SaveState(o.opts.Paths.StateFile, o.state); err != nil {
			o.opts.Log.Error("gravando state.json", "erro", err)
		}
	}
	o.mu.Unlock()

	o.stopAll(stop)

	o.mu.Lock()
	for _, v := range start {
		o.sups[config.NameKey(v.Name)] = o.launch(v)
	}
	snap := o.snapshotLocked()
	o.mu.Unlock()
	o.bus.publish(ipc.MustMessage("", ipc.TypeSnapshot, snap))
}

// launch sobe o supervisor sob recover; panic → log, Event Log e recriação
// após RestartDelay (dobrando até 60 s). Chamar com o.mu travado.
func (o *Orchestrator) launch(v config.VPN) *running {
	ctx, cancel := context.WithCancel(o.ctx)
	r := &running{vpn: v, cancel: cancel, done: make(chan struct{})}
	key := config.NameKey(v.Name)
	log := logging.ForVPN(o.opts.Log, v.Name)
	params := domain.ParamsFrom(v)
	r.last = domain.Initial(params, o.opts.Clock.Now(), o.state.Pauses[key])
	initial := r.last

	go func() {
		defer close(r.done)
		delay := o.opts.RestartDelay
		for {
			var sup *Supervisor
			sup = NewSupervisor(v, initial, Deps{
				Clock: o.opts.Clock, Link: o.opts.Link, Dialer: o.opts.Dialer, Queue: o.queue,
				Checker: adapters.NewChecker(v.Check, o.opts.Pinger, o.opts.TCPDial),
				Creds:   o.opts.Creds, Rand: o.opts.Rand, Log: log,
				OnUpdate: func(u Update) { o.onUpdate(r, sup, u) },
			})
			o.mu.Lock()
			r.sup = sup
			o.mu.Unlock()
			started := o.opts.Clock.Now()
			p := runRecovered(ctx, sup)
			if p == nil || ctx.Err() != nil {
				return
			}
			msg := fmt.Sprintf("VPN %s: supervisor em pânico, recriando em %s: %v", v.Name, delay, p)
			log.Error(msg)
			o.opts.Events.Error(msg)
			if o.opts.Clock.Now().Sub(started) > time.Minute {
				delay = o.opts.RestartDelay
			}
			t := o.opts.Clock.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C():
			}
			delay = min(delay*2, time.Minute)
			o.mu.Lock()
			initial = domain.Initial(params, o.opts.Clock.Now(), o.state.Pauses[key])
			o.mu.Unlock()
		}
	}()
	return r
}

func runRecovered(ctx context.Context, sup *Supervisor) (p any) {
	defer func() { p = recover() }()
	sup.Run(ctx)
	return nil
}

func (o *Orchestrator) onUpdate(r *running, sup *Supervisor, u Update) {
	o.mu.Lock()
	defer o.mu.Unlock()
	key := config.NameKey(r.vpn.Name)
	if o.sups[key] != r || r.sup != sup {
		return // supervisor antigo (reiniciado ou removido)
	}
	r.last = u.Status
	if u.PauseChanged {
		if rec := u.Status.PauseRecord(); rec == (config.Pause{}) {
			delete(o.state.Pauses, key)
		} else {
			o.state.Pauses[key] = rec
		}
		if err := config.SaveState(o.opts.Paths.StateFile, o.state); err != nil {
			o.opts.Log.Error("gravando state.json", "erro", err)
		}
	}
	now := o.opts.Clock.Now()
	o.bus.publish(ipc.MustMessage("", ipc.TypeVPNState, ToView(r.vpn, u.Status, now)))
	for _, n := range u.Notices {
		o.bus.publish(ipc.MustMessage("", ipc.TypeNotice, ipc.NoticeEvent{VPN: r.vpn.Name, Kind: string(n.Kind), Text: n.Text}))
	}
}

func (o *Orchestrator) snapshotLocked() ipc.Snapshot {
	now := o.opts.Clock.Now()
	snap := ipc.Snapshot{VPNs: []ipc.VPNView{}, Notifications: o.cfg.Notifications}
	for _, v := range o.cfg.VPNs {
		if r, ok := o.sups[config.NameKey(v.Name)]; ok {
			snap.VPNs = append(snap.VPNs, ToView(v, r.last, now))
		}
	}
	return snap
}

// Status devolve o snapshot atual.
func (o *Orchestrator) Status() ipc.Snapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.snapshotLocked()
}

// Subscribe registra um assinante; o primeiro evento é o snapshot.
func (o *Orchestrator) Subscribe() (<-chan ipc.Message, func()) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.bus.subscribe(ipc.MustMessage("", ipc.TypeSnapshot, o.snapshotLocked()))
}

func (o *Orchestrator) supervisors() []*Supervisor {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]*Supervisor, 0, len(o.sups))
	for _, r := range o.sups {
		if r.sup != nil {
			out = append(out, r.sup)
		}
	}
	return out
}

// Wake reavalia todas as VPNs (mudança de rede, desconexão RAS).
func (o *Orchestrator) Wake() {
	for _, s := range o.supervisors() {
		s.Wake()
	}
}

// PowerResume zera o backoff de todas e verifica em 5 s.
func (o *Orchestrator) PowerResume() {
	o.opts.Log.Info("retomada de energia detectada")
	ctx, cancel := context.WithTimeout(context.Background(), o.opts.CommandTimeout)
	defer cancel()
	for _, s := range o.supervisors() {
		s.PowerResume(ctx)
	}
}

// CredentialsChanged avisa todas as VPNs de que o cofre mudou.
func (o *Orchestrator) CredentialsChanged() {
	ctx, cancel := context.WithTimeout(context.Background(), o.opts.CommandTimeout)
	defer cancel()
	for _, s := range o.supervisors() {
		s.CredentialChanged(ctx)
	}
}

// watchResume detecta suspensão por salto de relógio (§4.6).
func (o *Orchestrator) watchResume(ctx context.Context) {
	const period = 5 * time.Second
	det := shared.ResumeDetector{Period: period}
	det.Observe(o.opts.Clock.Now())
	t := o.opts.Clock.NewTimer(period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
		}
		o.mu.Lock()
		det.Threshold = 2 * minInterval(o.cfg)
		o.mu.Unlock()
		if det.Observe(o.opts.Clock.Now()) {
			o.PowerResume()
		}
		t.Reset(period)
	}
}

func minInterval(c config.Config) time.Duration {
	m := time.Duration(config.DefaultInterval) * time.Second
	for _, v := range c.VPNs {
		if d := time.Duration(v.IntervalSeconds) * time.Second; d < m {
			m = d
		}
	}
	return m
}

func (o *Orchestrator) find(name string) (*Supervisor, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	r, ok := o.sups[config.NameKey(name)]
	if !ok || r.sup == nil {
		return nil, &ipc.Error{Code: ipc.CodeNotFound, Message: fmt.Sprintf("VPN %q não existe", name)}
	}
	return r.sup, nil
}

func replyErr(r domain.Reply, err error) error {
	if errors.Is(err, ErrStopped) || errors.Is(err, context.DeadlineExceeded) {
		return &ipc.Error{Code: ipc.CodeInternal, Message: "supervisor indisponível (reiniciando); tente de novo"}
	}
	if err != nil {
		return err
	}
	codes := map[domain.ReplyCode]string{
		domain.ReplyPaused: ipc.CodePaused, domain.ReplyAlreadyReconnecting: ipc.CodeAlreadyReconnecting,
		domain.ReplyCredentialRejected: ipc.CodeCredentialRejected, domain.ReplyDisabled: ipc.CodeDisabled,
	}
	if c, ok := codes[r.Code]; ok {
		return &ipc.Error{Code: c, Message: r.Message}
	}
	return nil
}

func (o *Orchestrator) command(name string, f func(*Supervisor, context.Context) (domain.Reply, error)) error {
	s, err := o.find(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.opts.CommandTimeout)
	defer cancel()
	return replyErr(f(s, ctx))
}

// CheckNow pede verificação imediata.
func (o *Orchestrator) CheckNow(name string) error {
	return o.command(name, (*Supervisor).CheckNow)
}

// Reconnect pede reconexão manual.
func (o *Orchestrator) Reconnect(name string) error {
	return o.command(name, (*Supervisor).Reconnect)
}

// Pause pausa até until (nil = até retomar).
func (o *Orchestrator) Pause(name string, until *time.Time) error {
	var u time.Time
	if until != nil {
		if !until.After(o.opts.Clock.Now()) {
			return &ipc.Error{Code: ipc.CodeBadRequest, Message: "o fim da pausa já passou"}
		}
		u = *until
	}
	return o.command(name, func(s *Supervisor, ctx context.Context) (domain.Reply, error) { return s.Pause(ctx, u) })
}

// Resume retoma.
func (o *Orchestrator) Resume(name string) error {
	return o.command(name, (*Supervisor).Resume)
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race -count=5 ./internal/features/monitor/service/`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/features/monitor/service && git commit -m "feat(monitor): orquestrador com recarga parcial, recuperação de panic, barramento de eventos e pausa persistida"
```

---

### Task 22: monitor/service: mudanças de config pelo pipe e recarga do config.json

**Files:**
- Create: `internal/features/monitor/service/configops.go`
- Test: `internal/features/monitor/service/configops_test.go`

**Interfaces:**
- Consumes: `Orchestrator`, `apply`, `bus`, `o.mu`/`o.applyMu` (21); `config.Save`, `Validate`, `ValidateVPN`, `Parse`, `NameKey`; `logging.Tail`; `ipc.Backend`.
- Produces: `(*Orchestrator).SetEnabled`, `AddVPN`, `UpdateVPN` (nome imutável), `RemoveVPN`, `SetGlobal`, `GetConfig`, `ListRasEntries`, `LogTail`, `ReloadFromDisk()`, `MarkWritten([]byte)`;
  `(*Orchestrator).MarkDiskInvalid(error)`; `var _ ipc.Backend = (*Orchestrator)(nil)` — o orquestrador passa a ser o backend do pipe.

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/monitor/service/configops_test.go`:

```go
package service

import (
	"os"
	"strings"
	"testing"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/domain"
)

func intp(n int) *int { return &n }

func TestAddUpdateRemoveVPN(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitView("Matriz", domain.Conectada)

	// "Adicionar VPN" da bandeja: só nome, entrada e verificação link.
	if err := h.o.AddVPN(config.RawVPN{Name: "Filial", RasEntry: "VPN Filial", Check: &config.RawCheck{Kind: config.CheckLink}}); err != nil {
		t.Fatal(err)
	}
	h.waitView("Filial", domain.Conectada)
	saved, err := config.Load(h.paths.ConfigFile)
	if err != nil || len(saved.VPNs) != 2 || saved.VPNs[1].IntervalSeconds != 30 {
		t.Fatalf("gravado: %+v %v", saved, err)
	}

	var e *ipc.Error
	err = h.o.AddVPN(config.RawVPN{Name: "FILIAL", RasEntry: "x", Check: &config.RawCheck{Kind: config.CheckLink}})
	if !asIPC(err, &e) || e.Code != ipc.CodeInvalidConfig || e.Fields[0].Field != "name" {
		t.Fatalf("nome repetido: %v", err)
	}
	err = h.o.AddVPN(config.RawVPN{Name: "X", RasEntry: "x", Check: &config.RawCheck{Kind: config.CheckTCP, Host: "h"}})
	if !asIPC(err, &e) || e.Fields[0].Field != "check.port" {
		t.Fatalf("erro junto ao campo: %v", err)
	}

	err = h.o.UpdateVPN("filial", config.RawVPN{RasEntry: "VPN Filial", IntervalSeconds: intp(60), Check: &config.RawCheck{Kind: config.CheckLink}})
	if err != nil {
		t.Fatal(err)
	}
	if c := h.o.GetConfig(); c.VPNs[1].Name != "Filial" || c.VPNs[1].IntervalSeconds != 60 {
		t.Fatalf("update: %+v", c.VPNs[1])
	}
	err = h.o.UpdateVPN("Filial", config.RawVPN{Name: "Outra", RasEntry: "x"})
	if !asIPC(err, &e) || e.Code != ipc.CodeInvalidConfig || !strings.Contains(e.Message, "não pode ser alterado") {
		t.Fatalf("renomear: %v", err)
	}
	if err := h.o.UpdateVPN("Nenhuma", config.RawVPN{RasEntry: "x"}); !asIPC(err, &e) || e.Code != ipc.CodeNotFound {
		t.Fatalf("inexistente: %v", err)
	}

	if err := h.o.SetEnabled("Filial", false); err != nil {
		t.Fatal(err)
	}
	h.waitView("Filial", domain.Desativada)
	if err := h.o.RemoveVPN("Filial"); err != nil {
		t.Fatal(err)
	}
	if n := len(h.o.Status().VPNs); n != 1 {
		t.Fatalf("restaram %d", n)
	}
}

func TestSetGlobalCallsOnGlobals(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(), config.State{})
	var got config.Config
	h.o.opts.OnGlobals = func(c config.Config) { got = c }
	lvl, off := "debug", false
	if err := h.o.SetGlobal(ipc.SetGlobalRequest{LogLevel: &lvl, Notifications: &off}); err != nil {
		t.Fatal(err)
	}
	if got.LogLevel != "debug" || got.Notifications {
		t.Fatalf("%+v", got)
	}
	bad := "verbose"
	var e *ipc.Error
	if err := h.o.SetGlobal(ipc.SetGlobalRequest{LogLevel: &bad}); !asIPC(err, &e) || e.Fields[0].Field != "logLevel" {
		t.Fatalf("nível inválido: %v", err)
	}
	if c, _ := config.Load(h.paths.ConfigFile); c.LogLevel != "debug" {
		t.Fatal("config inválida não pode ser gravada")
	}
}

func TestReloadFromDisk(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitView("Matriz", domain.Conectada)
	events, cancel := h.o.Subscribe()
	defer cancel()
	<-events // snapshot

	// Gravação própria (mutate) não recarrega: o hash bate.
	_ = h.o.SetEnabled("Matriz", true)
	sup := h.supOf("Matriz")
	h.o.ReloadFromDisk()
	if h.supOf("Matriz") != sup {
		t.Fatal("a própria gravação não deve recarregar")
	}

	// Edição manual inválida: mantém a anterior e publica configStatus.
	_ = os.WriteFile(h.paths.ConfigFile, []byte(`{"version":2,"vpns":[{"name":"Matriz","rasEntry":"VPN Matriz","check":{"kind":"ping"}}]}`), 0o600)
	h.o.ReloadFromDisk()
	if h.supOf("Matriz") != sup {
		t.Fatal("config inválida não pode derrubar a anterior")
	}
	var st ipc.ConfigStatus
	for m := range drain(events) {
		if m.Type == ipc.TypeConfigStatus {
			_ = ipc.DecodePayload(m.Payload, &st)
		}
	}
	if st.OK || len(st.Fields) == 0 || st.Fields[0].Field != "vpns[0].check.host" {
		t.Fatalf("configStatus: %+v", st)
	}
	if ev := h.events.Snapshot(); len(ev) == 0 || ev[len(ev)-1].Level != "warning" {
		t.Fatalf("Event Log: %+v", ev)
	}

	// Enquanto o arquivo em disco estiver inválido, mudanças pelo pipe são
	// recusadas: gravar agora apagaria a edição manual.
	var e *ipc.Error
	err := h.o.AddVPN(config.RawVPN{Name: "Nova", RasEntry: "x", Check: &config.RawCheck{Kind: config.CheckLink}})
	if !asIPC(err, &e) || e.Code != ipc.CodeInvalidConfig || !strings.Contains(e.Message, "corrija o arquivo") {
		t.Fatalf("mudança com arquivo inválido: %v", err)
	}
	if b, _ := os.ReadFile(h.paths.ConfigFile); !strings.Contains(string(b), `"kind":"ping"}}]}`) {
		t.Fatalf("arquivo inválido foi sobrescrito: %s", b)
	}

	// Arquivo momentaneamente vazio (editor gravando em dois passos): mantém.
	_ = os.WriteFile(h.paths.ConfigFile, nil, 0o600)
	h.o.ReloadFromDisk()
	if h.supOf("Matriz") != sup {
		t.Fatal("arquivo vazio não pode derrubar a config anterior")
	}

	// Edição manual válida: aplica.
	c := cfgWith(vpnNamed("Matriz"), vpnNamed("Filial"))
	data, _ := config.Marshal(c)
	_ = os.WriteFile(h.paths.ConfigFile, data, 0o600)
	h.o.ReloadFromDisk()
	h.waitView("Filial", domain.Conectada)
	if err := h.o.SetEnabled("Filial", true); err != nil {
		t.Fatalf("após recarga válida as mudanças voltam a valer: %v", err)
	}
}

func TestListRasEntriesMarksMonitored(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	got, err := h.o.ListRasEntries()
	if err != nil || len(got) != 3 || !got[0].Monitored || got[1].Monitored {
		t.Fatalf("%+v %v", got, err)
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./internal/features/monitor/service/`  
Expected: FAIL — `h.o.AddVPN undefined`…

- [ ] **Step 3: Implementar**

Toda mudança valida a config inteira antes de gravar (inválida nunca é gravada) e devolve `invalid_config` com `Fields`
relativos à VPN para a interface mostrar junto ao campo. A recarga ignora a própria gravação comparando o hash; arquivo inválido **ou
vazio** (editor gravando em dois passos — item 3 do Review Focus) mantém a config anterior, registra no log e no Event Log e publica `configStatus`.
Enquanto o arquivo em disco estiver inválido (na recarga, ou desde a partida via `MarkDiskInvalid`), toda mudança pelo pipe é recusada
com `invalid_config` e o motivo — gravar apagaria a edição manual.

`internal/features/monitor/service/configops.go`:

```go
package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
)

var _ ipc.Backend = (*Orchestrator)(nil)

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func invalid(probs []config.FieldError) error {
	msgs := make([]string, len(probs))
	for i, p := range probs {
		msgs[i] = p.Field + ": " + p.Message
	}
	return &ipc.Error{Code: ipc.CodeInvalidConfig, Message: "config inválida: " + strings.Join(msgs, "; "), Fields: probs}
}

// mutate aplica f a uma cópia da config, valida, grava e aplica. Uma config
// inválida nunca é gravada (§5.2).
func (o *Orchestrator) mutate(f func(c *config.Config) error) error {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	o.mu.Lock()
	if bad := o.diskInvalid; bad != nil {
		o.mu.Unlock()
		return &ipc.Error{Code: ipc.CodeInvalidConfig,
			Message: "config.json em disco está inválido; corrija o arquivo antes de alterar pela bandeja ou CLI: " + bad.Error()}
	}
	c := o.cfg
	c.VPNs = slices.Clone(o.cfg.VPNs)
	o.mu.Unlock()
	if err := f(&c); err != nil {
		return err
	}
	if err := config.Validate(c); err != nil {
		var ve *config.ValidationError
		if errors.As(err, &ve) {
			return invalid(ve.Problems)
		}
		return err
	}
	data, err := config.Save(o.opts.Paths.ConfigFile, c)
	if err != nil {
		return &ipc.Error{Code: ipc.CodeInternal, Message: "gravando config.json: " + err.Error()}
	}
	o.mu.Lock()
	o.lastWritten = hashBytes(data)
	o.mu.Unlock()
	o.opts.OnGlobals(c)
	o.apply(c)
	o.bus.publish(ipc.MustMessage("", ipc.TypeConfigStatus, ipc.ConfigStatus{OK: true}))
	return nil
}

func indexOf(c *config.Config, name string) int {
	key := config.NameKey(name)
	for i, v := range c.VPNs {
		if config.NameKey(v.Name) == key {
			return i
		}
	}
	return -1
}

func notFound(name string) error {
	return &ipc.Error{Code: ipc.CodeNotFound, Message: fmt.Sprintf("VPN %q não existe", name)}
}

// SetEnabled ativa ou desativa uma VPN.
func (o *Orchestrator) SetEnabled(name string, enabled bool) error {
	return o.mutate(func(c *config.Config) error {
		i := indexOf(c, name)
		if i < 0 {
			return notFound(name)
		}
		c.VPNs[i].Enabled = enabled
		return nil
	})
}

// AddVPN adiciona uma VPN; campos omitidos recebem os padrões.
func (o *Orchestrator) AddVPN(raw config.RawVPN) error {
	return o.mutate(func(c *config.Config) error {
		v := raw.Normalize()
		probs := config.ValidateVPN(v)
		if indexOf(c, v.Name) >= 0 {
			probs = append(probs, config.FieldError{Field: "name", Message: "já existe uma VPN com esse nome"})
		}
		if len(probs) > 0 {
			return invalid(probs)
		}
		c.VPNs = append(c.VPNs, v)
		return nil
	})
}

// UpdateVPN troca a config de uma VPN. O nome é imutável (§5.2).
func (o *Orchestrator) UpdateVPN(name string, raw config.RawVPN) error {
	return o.mutate(func(c *config.Config) error {
		i := indexOf(c, name)
		if i < 0 {
			return notFound(name)
		}
		if raw.Name != "" && config.NameKey(raw.Name) != config.NameKey(name) {
			return invalid([]config.FieldError{{Field: "name", Message: "o nome não pode ser alterado; remova e adicione de novo"}})
		}
		raw.Name = c.VPNs[i].Name
		v := raw.Normalize()
		if probs := config.ValidateVPN(v); len(probs) > 0 {
			return invalid(probs)
		}
		c.VPNs[i] = v
		return nil
	})
}

// RemoveVPN remove uma VPN (a credencial no cofre fica; use credential clear).
func (o *Orchestrator) RemoveVPN(name string) error {
	return o.mutate(func(c *config.Config) error {
		i := indexOf(c, name)
		if i < 0 {
			return notFound(name)
		}
		c.VPNs = slices.Delete(c.VPNs, i, i+1)
		return nil
	})
}

// SetGlobal muda avisos e nível de log.
func (o *Orchestrator) SetGlobal(req ipc.SetGlobalRequest) error {
	return o.mutate(func(c *config.Config) error {
		if req.Notifications != nil {
			c.Notifications = *req.Notifications
		}
		if req.LogLevel != nil {
			c.LogLevel = *req.LogLevel
		}
		return nil
	})
}

// GetConfig devolve a config atual.
func (o *Orchestrator) GetConfig() config.Config {
	o.mu.Lock()
	defer o.mu.Unlock()
	c := o.cfg
	c.VPNs = slices.Clone(o.cfg.VPNs)
	return c
}

// ListRasEntries lista o catálogo de todos os usuários, marcando as monitoradas.
func (o *Orchestrator) ListRasEntries() ([]ipc.RasEntry, error) {
	names, err := o.opts.RAS.Entries()
	if err != nil {
		return nil, err
	}
	c := o.GetConfig()
	out := make([]ipc.RasEntry, 0, len(names))
	for _, n := range names {
		mon := slices.ContainsFunc(c.VPNs, func(v config.VPN) bool { return strings.EqualFold(v.RasEntry, n) })
		out = append(out, ipc.RasEntry{Name: n, Monitored: mon})
	}
	return out, nil
}

// LogTail devolve o fim do log.
func (o *Orchestrator) LogTail(maxBytes int) (string, error) {
	return logging.Tail(o.opts.Paths.LogFile, int64(maxBytes))
}

// ReloadFromDisk relê config.json após edição manual. Ignora a própria
// gravação (mesmo hash). Inválida: mantém a anterior, registra no log e no
// Event Log e publica configStatus com o motivo.
func (o *Orchestrator) ReloadFromDisk() {
	data, err := os.ReadFile(o.opts.Paths.ConfigFile)
	if err != nil {
		o.opts.Log.Error("lendo config.json", "erro", err)
		return
	}
	h := hashBytes(data)
	o.mu.Lock()
	same := h == o.lastWritten
	o.mu.Unlock()
	if same {
		return
	}
	c, err := config.Parse(data)
	if err != nil {
		o.MarkDiskInvalid(err)
		msg := "config.json inválido; mantendo a config anterior: " + err.Error()
		o.opts.Log.Error(msg)
		o.opts.Events.Warning(msg)
		st := ipc.ConfigStatus{OK: false, Message: err.Error()}
		var ve *config.ValidationError
		if errors.As(err, &ve) {
			st.Fields = ve.Problems
		}
		o.bus.publish(ipc.MustMessage("", ipc.TypeConfigStatus, st))
		return
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	o.mu.Lock()
	o.lastWritten = h
	o.diskInvalid = nil
	o.mu.Unlock()
	o.opts.Log.Info("config.json recarregado")
	o.opts.OnGlobals(c)
	o.apply(c)
	o.bus.publish(ipc.MustMessage("", ipc.TypeConfigStatus, ipc.ConfigStatus{OK: true}))
}

// MarkWritten registra o hash do config.json gravado fora do orquestrador
// (seed no primeiro início), para o observador não recarregá-lo à toa.
func (o *Orchestrator) MarkWritten(data []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastWritten = hashBytes(data)
}

// MarkDiskInvalid registra que o config.json em disco está inválido (na
// partida, pela montagem; depois, pela recarga). Mudanças pelo pipe ficam
// recusadas até uma recarga válida.
func (o *Orchestrator) MarkDiskInvalid(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.diskInvalid = err
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race -count=3 ./internal/features/monitor/service/`  
Expected: PASS

Run: `go vet ./... && GOOS=windows go vet ./...`  
Expected: sem saída

- [ ] **Step 5: Commit**

```bash
git add internal/features/monitor/service && git commit -m "feat(monitor): mudanças de config validadas pelo pipe e recarga do config.json editado à mão"
```

---

### Task 23: vpnmon-svc: CLI local (version, config validate, credential, install/uninstall)

**Files:**
- Create: `cmd/vpnmon-svc/main.go`
- Create: `cmd/vpnmon-svc/paths.go`
- Create: `cmd/vpnmon-svc/platform.go`
- Create: `cmd/vpnmon-svc/platform_windows.go`
- Create: `cmd/vpnmon-svc/platform_other.go`
- Create: `cmd/vpnmon-svc/cli.go`
- Test: `cmd/vpnmon-svc/cli_test.go`

**Interfaces:**
- Consumes: `config.*`, `credentials.Vault` (16), `svc.IsElevated/Install/Uninstall/IsService/Run/Hooks` (11), `ipc.Dial/Handshake/Client` (20), `dpapi.New`, `fake.DPAPI`, plataforma (8–10), `service.Paths` (21).
- Produces: `env{stdin; stdout, stderr; dataDir; elevated; dpapi; readPassword; dial; platform; install; uninstall; isService; runService; now}` e `defaultEnv()`;
  `runCLI(args, env) int` (0 ok, 1 erro, 2 uso), `dispatch`, `usageError`, `parseFlags(fs, args)` (flags antes ou depois dos posicionais);
  `layout{Dir, Credentials; service.Paths}`, `newLayout(dir)`, `dataDir()` (`VPNMON_DATA_DIR` ou `%ProgramData%\VPNMonitor`);
  `Platform{RAS; Pinger; Net; DPAPI; ACL; Listen func() (net.Listener, error); Events; ReadSeed}`, `realPlatform()`, `readPassword(io.Reader)`;
  variáveis `version`, `commit`, `date` (ldflags).

- [ ] **Step 1: Escrever o teste que falha**

`cmd/vpnmon-svc/cli_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/fake"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/svc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/credentials"
)

type testEnv struct {
	env
	out, errb *bytes.Buffer
	dir       string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	te := &testEnv{out: &bytes.Buffer{}, errb: &bytes.Buffer{}, dir: dir}
	te.env = env{
		stdin: strings.NewReader(""), stdout: te.out, stderr: te.errb,
		dataDir:      func() (string, error) { return dir, nil },
		elevated:     func() bool { return true },
		dpapi:        fake.DPAPI{},
		readPassword: func(io.Reader) (string, error) { return "digitada", nil },
		dial: func(context.Context) (*ipc.Client, error) {
			return nil, errors.New("serviço VPN Monitor inacessível")
		},
		platform:   func() (Platform, error) { return Platform{}, errors.New("sem plataforma") },
		install:    func(string) error { return nil },
		uninstall:  func() error { return nil },
		isService:  func() (bool, error) { return false, nil },
		runService: func(svc.Hooks) error { return nil },
		now:        func() time.Time { return time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC) },
	}
	return te
}

func (te *testEnv) run(args ...string) int {
	te.out.Reset()
	te.errb.Reset()
	return runCLI(args, te.env)
}

func (te *testEnv) writeConfig(t *testing.T, vpns ...string) {
	t.Helper()
	c := config.Empty()
	for _, n := range vpns {
		c.VPNs = append(c.VPNs, config.RawVPN{Name: n, RasEntry: "VPN " + n, Check: &config.RawCheck{Kind: config.CheckLink}}.Normalize())
	}
	if _, err := config.Save(filepath.Join(te.dir, "config.json"), c); err != nil {
		t.Fatal(err)
	}
}

func TestVersionWorksWithoutElevation(t *testing.T) {
	te := newTestEnv(t)
	te.elevated = func() bool { return false }
	if code := te.run("version"); code != 0 || !strings.HasPrefix(te.out.String(), "vpnmon-svc dev") {
		t.Fatalf("%d %q", code, te.out)
	}
	if code := te.run("status"); code != 1 || !strings.Contains(te.errb.String(), "administrador") {
		t.Fatalf("sem elevação: %d %q", code, te.errb)
	}
}

func TestUsageErrors(t *testing.T) {
	te := newTestEnv(t)
	for _, args := range [][]string{{}, {"formatar"}, {"credential"}, {"credential", "set", "Matriz"}, {"vpn", "add", "--name", "x"}} {
		if code := te.run(args...); code != 2 {
			t.Errorf("%v: código %d", args, code)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	if code := te.run("config", "validate"); code != 0 || !strings.Contains(te.out.String(), "válido (1 VPN(s))") {
		t.Fatalf("%d %q %q", code, te.out, te.errb)
	}
	bad := filepath.Join(te.dir, "ruim.json")
	_ = os.WriteFile(bad, []byte(`{"version":2,"vpns":[{"name":"","rasEntry":"x","check":{"kind":"link"}}]}`), 0o600)
	if code := te.run("config", "validate", bad); code != 1 || !strings.Contains(te.out.String(), "vpns[0].name") {
		t.Fatalf("%d %q", code, te.out)
	}
}

func TestCredentialSetClearList(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz", "Filial")
	// PowerShell manda CRLF; espaços fazem parte da senha.
	te.stdin = strings.NewReader(" s3nha \r\n")
	if code := te.run("credential", "set", "Matriz", "--user", "ana", "--password-stdin"); code != 0 {
		t.Fatalf("set: %q", te.errb)
	}
	vault := credentials.Vault{Dir: filepath.Join(te.dir, "credentials"), DPAPI: fake.DPAPI{}}
	if u, pw, ok, _ := vault.Get("Matriz"); !ok || u != "ana" || pw.Reveal() != " s3nha " {
		t.Fatalf("cofre: %q %q %v", u, pw.Reveal(), ok)
	}
	if strings.Contains(te.out.String()+te.errb.String(), "s3nha") {
		t.Fatal("senha ecoada na saída")
	}
	if code := te.run("credential", "set", "--user", "bia", "Filial"); code != 0 { // sem stdin: leitura sem eco
		t.Fatalf("set interativo: %q", te.errb)
	}
	if _, pw, _, _ := vault.Get("Filial"); pw.Reveal() != "digitada" {
		t.Fatal("senha digitada")
	}
	if code := te.run("credential", "set", "Inexistente", "--user", "x", "--password-stdin"); code != 1 {
		t.Fatal("VPN inexistente deve falhar")
	}
	te.run("credential", "list")
	if !strings.Contains(te.out.String(), "Matriz\tcofre") || !strings.Contains(te.out.String(), "Filial\tcofre") {
		t.Fatalf("list: %q", te.out)
	}
	if code := te.run("credential", "clear", "Matriz"); code != 0 || vault.Has("Matriz") {
		t.Fatal("clear")
	}
	te.run("credential", "list")
	if !strings.Contains(te.out.String(), "Matriz\t—") {
		t.Fatalf("list após clear: %q", te.out)
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./cmd/vpnmon-svc/`  
Expected: FAIL — `undefined: env`, `undefined: runCLI`…

- [ ] **Step 3: Implementar**

Todos os comandos, menos `version`/`help`, exigem elevação. `credential set` grava direto no cofre (o serviço percebe
pelo observador da pasta, tarefa 24) e confere se a VPN existe na config. Com `--password-stdin`, só o fim de linha (`\n`/`\r\n` do
PowerShell) é removido — espaços fazem parte da senha (item 5 do Review Focus).

`cmd/vpnmon-svc/main.go`:

```go
// Comando vpnmon-svc: o serviço VPNMonitor e a CLI de administração.
package main

import (
	"os"
)

// Preenchidos por -ldflags no build de release.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	os.Exit(runCLI(os.Args[1:], defaultEnv()))
}
```

`cmd/vpnmon-svc/paths.go`:

```go
package main

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/service"
)

// dataDirEnv permite apontar outra pasta de dados (testes, desenvolvimento).
const dataDirEnv = "VPNMON_DATA_DIR"

// layout são os caminhos dentro da pasta de dados (§5.1).
type layout struct {
	Dir         string
	Credentials string
	service.Paths
}

func newLayout(dir string) layout {
	return layout{
		Dir:         dir,
		Credentials: filepath.Join(dir, "credentials"),
		Paths: service.Paths{
			ConfigFile: filepath.Join(dir, "config.json"),
			StateFile:  filepath.Join(dir, "state.json"),
			LogFile:    filepath.Join(dir, "logs", "vpnmon.log"),
		},
	}
}

// dataDir resolve a pasta: VPNMON_DATA_DIR ou %ProgramData%\VPNMonitor.
func dataDir() (string, error) {
	if d := os.Getenv(dataDirEnv); d != "" {
		return d, nil
	}
	d, err := defaultDataDir()
	if err != nil {
		return "", errors.New("pasta de dados indefinida: defina " + dataDirEnv)
	}
	return d, nil
}
```

`cmd/vpnmon-svc/platform.go`:

```go
package main

import (
	"net"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/acl"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/dpapi"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/icmp"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/netwatch"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

// Platform reúne o acesso ao SO usado pelo serviço; no Windows vem de
// realPlatform, nos testes de fakes.
type Platform struct {
	RAS      ras.Client
	Pinger   icmp.Pinger
	Net      netwatch.Watcher
	DPAPI    dpapi.Protector
	ACL      acl.Securer
	Listen   func() (net.Listener, error)
	Events   logging.EventSink
	ReadSeed config.SeedReader
}
```

`cmd/vpnmon-svc/platform_windows.go`: Monta a plataforma real e lê a senha sem eco (`SetConsoleMode` sem `ENABLE_ECHO_INPUT`). Só compila no Windows (verificado por `GOOS=windows go vet`).

```go
//go:build windows

package main

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/acl"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/dpapi"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/icmp"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/netwatch"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

func defaultDataDir() (string, error) {
	pd, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(pd, "VPNMonitor"), nil
}

// realPlatform monta as implementações Windows.
func realPlatform() (Platform, error) {
	r, err := ras.NewClient()
	if err != nil {
		return Platform{}, err
	}
	nw, err := netwatch.New()
	if err != nil {
		return Platform{}, err
	}
	ev, err := logging.OpenEventSink()
	if err != nil {
		ev = logging.NopSink{} // origem não registrada (sem install/MSI): segue sem Event Log
	}
	return Platform{
		RAS: r, Pinger: icmp.New(), Net: nw, DPAPI: dpapi.New(), ACL: acl.New(),
		Listen: ipc.Listen, Events: ev, ReadSeed: config.ReadSeedRegistry,
	}, nil
}

// readPassword lê uma linha do console sem eco.
func readPassword(in io.Reader) (string, error) {
	h := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return "", errors.New("sem console para ler a senha; use --password-stdin")
	}
	if err := windows.SetConsoleMode(h, mode&^windows.ENABLE_ECHO_INPUT); err != nil {
		return "", err
	}
	defer windows.SetConsoleMode(h, mode)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
```

`cmd/vpnmon-svc/platform_other.go`:

```go
//go:build !windows

package main

import (
	"errors"
	"io"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform"
)

func defaultDataDir() (string, error) { return "", platform.ErrNotSupported }

func realPlatform() (Platform, error) { return Platform{}, platform.ErrNotSupported }

func readPassword(io.Reader) (string, error) {
	return "", errors.New("leitura sem eco só no Windows; use --password-stdin")
}
```

`cmd/vpnmon-svc/cli.go`:

```go
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
			pw = strings.TrimRight(line, "\r\n")
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
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race ./cmd/vpnmon-svc/`  
Expected: PASS

Run: `go vet ./... && GOOS=windows go vet ./...`  
Expected: sem saída

Run: `GOOS=windows CGO_ENABLED=0 go build -o /dev/null ./cmd/vpnmon-svc`  
Expected: compila

- [ ] **Step 5: Commit**

```bash
git add cmd/vpnmon-svc && git commit -m "feat(cli): vpnmon-svc com version, config validate, credential e install/uninstall"
```

---

### Task 24: vpnmon-svc: montagem do serviço (modo serviço e `run`) com teste de integração no Linux

**Files:**
- Create: `cmd/vpnmon-svc/app.go`
- Modify: `cmd/vpnmon-svc/cli.go` (`runCLI` sem argumentos e `case "run"`)
- Test: `cmd/vpnmon-svc/app_test.go`

**Interfaces:**
- Consumes: Tudo das tarefas 4–23: `config.LoadOrCreate`, `LoadState`, `logging.OpenRotating/New/ParseLevel`, `credentials.Vault/Resolver`, `adapters.LinkProber/Dialer/Credentials`, `service.New/Options/Orchestrator`, `netwatch.Debounce`, `shared.NewPollWatcher`, `ipc.Server`, `svc.Hooks`, `Platform`, `layout`, `env`.
- Produces: `serve(ctx, Platform, layout, shared.Clock, ready func(*service.Orchestrator)) error`; `credBridge` (credentials → adapters);
  `serviceMain(env) int`; `cmdRun(env) error`; helpers de teste `startService(t, *testEnv) *testService` e `waitFor(t, cond)` (usados na tarefa 25).

- [ ] **Step 1: Escrever o teste que falha**

`cmd/vpnmon-svc/app_test.go`:

```go
package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/fake"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/svc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/service"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// testService é o serviço inteiro rodando no Linux com plataforma falsa e o
// pipe trocado por TCP local.
type testService struct {
	o      *service.Orchestrator
	ras    *fake.RAS
	events *logging.RecordingSink
	cancel context.CancelFunc
	done   chan error
}

func startService(t *testing.T, te *testEnv) *testService {
	t.Helper()
	ts := &testService{ras: fake.NewRAS("VPN Matriz", "VPN Filial"), events: &logging.RecordingSink{}, done: make(chan error, 1)}
	pinger := fake.NewPinger()
	pinger.SetReachable("10.0.0.1", true)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := Platform{
		RAS: ts.ras, Pinger: pinger, Net: fake.NewNet(), DPAPI: fake.DPAPI{}, ACL: &fake.ACL{},
		Listen: func() (net.Listener, error) { return ln, nil }, Events: ts.events,
		ReadSeed: func() (config.Seed, bool, error) {
			return config.Seed{VPNEntry: "VPN Matriz", VPNName: "Matriz", CheckHost: "10.0.0.1"}, true, nil
		},
	}
	te.platform = func() (Platform, error) { return p, nil }
	te.dial = func(ctx context.Context) (*ipc.Client, error) {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return nil, err
		}
		return ipc.Handshake(c, "teste")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ts.cancel = cancel
	ready := make(chan *service.Orchestrator, 1)
	go func() {
		ts.done <- serve(ctx, p, newLayout(te.dir), shared.RealClock{}, func(o *service.Orchestrator) { ready <- o })
	}()
	select {
	case ts.o = <-ready:
	case err := <-ts.done:
		t.Fatalf("serviço não subiu: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-ts.done:
		case <-time.After(10 * time.Second):
		}
	})
	return ts
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condição não atingida em 5 s")
}

func TestServeStartsAndStops(t *testing.T) {
	te := newTestEnv(t)
	ts := startService(t, te)

	// O seed gerou a config e a VPN conectou (discagem do fake).
	stateOf := func() string {
		var snap ipc.Snapshot
		c, err := te.dial(context.Background())
		if err != nil {
			return ""
		}
		defer c.Close()
		if err := c.Call(ipc.TypeStatus, nil, &snap); err != nil || len(snap.VPNs) == 0 {
			return ""
		}
		return snap.VPNs[0].State
	}
	waitFor(t, func() bool { return stateOf() == "Conectada" })
	if c, err := config.Load(filepath.Join(te.dir, "config.json")); err != nil || c.VPNs[0].Name != "Matriz" {
		t.Fatalf("seed: %+v %v", c, err)
	}

	// Edição manual do config.json é recarregada.
	c := config.Empty()
	c.VPNs = []config.VPN{
		config.RawVPN{Name: "Matriz", RasEntry: "VPN Matriz", Check: &config.RawCheck{Kind: config.CheckLink}}.Normalize(),
		config.RawVPN{Name: "Filial", RasEntry: "VPN Filial", Check: &config.RawCheck{Kind: config.CheckLink}}.Normalize(),
	}
	data, _ := config.Marshal(c)
	_ = os.WriteFile(filepath.Join(te.dir, "config.json"), data, 0o600)
	waitFor(t, func() bool { return len(ts.o.Status().VPNs) == 2 })

	start := time.Now()
	ts.cancel()
	if err := <-ts.done; err != nil {
		t.Fatal(err)
	}
	ts.done <- nil
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("parada levou %s (máx. 10 s)", d)
	}
	if _, err := os.Stat(filepath.Join(te.dir, "state.json")); err != nil {
		t.Fatal("state.json deveria ser gravado na parada")
	}
	ev := ts.events.Snapshot()
	if len(ev) < 2 || !strings.Contains(ev[0].Msg, "iniciado") || !strings.Contains(ev[len(ev)-1].Msg, "parado") {
		t.Fatalf("Event Log: %+v", ev)
	}
	if !ts.ras.IsActive("VPN Matriz") {
		t.Fatal("parar o serviço não pode derrubar VPN conectada")
	}
}

func TestServeSurvivesInvalidConfig(t *testing.T) {
	te := newTestEnv(t)
	_ = os.WriteFile(filepath.Join(te.dir, "config.json"), []byte(`{"version":9}`), 0o600)
	ts := startService(t, te)
	if n := len(ts.o.Status().VPNs); n != 0 {
		t.Fatalf("config inválida: sobe sem VPNs, veio %d", n)
	}
	if b, _ := os.ReadFile(filepath.Join(te.dir, "config.json")); string(b) != `{"version":9}` {
		t.Fatal("arquivo inválido não pode ser sobrescrito")
	}
	if ev := ts.events.Snapshot(); len(ev) == 0 || ev[0].Level != "error" {
		t.Fatalf("Event Log: %+v", ev)
	}
	err := ts.o.AddVPN(config.RawVPN{Name: "Nova", RasEntry: "VPN Filial", Check: &config.RawCheck{Kind: config.CheckLink}})
	if err == nil || !strings.Contains(err.Error(), "corrija o arquivo") {
		t.Fatalf("vpn add com config.json inválido deve ser recusado: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(te.dir, "config.json")); string(b) != `{"version":9}` {
		t.Fatal("arquivo inválido não pode ser sobrescrito por vpn add")
	}
}

func TestServeExitsCleanlyWhenPipeIsTaken(t *testing.T) {
	te := newTestEnv(t)
	rasFake := fake.NewRAS("VPN Matriz")
	_ = os.WriteFile(filepath.Join(te.dir, "state.json"), []byte(`{"pauses":{"matriz":{"indefinite":true}}}`), 0o600)
	p := Platform{
		RAS: rasFake, Pinger: fake.NewPinger(), Net: fake.NewNet(), DPAPI: fake.DPAPI{}, ACL: &fake.ACL{},
		Listen: func() (net.Listener, error) { return nil, errors.New("Access is denied") },
		Events: &logging.RecordingSink{},
		ReadSeed: func() (config.Seed, bool, error) {
			return config.Seed{VPNEntry: "VPN Matriz", CheckHost: "10.0.0.1"}, true, nil
		},
	}
	err := serve(context.Background(), p, newLayout(te.dir), shared.RealClock{}, nil)
	if err == nil || !strings.Contains(err.Error(), "outro VPN Monitor") {
		t.Fatalf("serve deve falhar cedo: %v", err)
	}
	if len(rasFake.Calls()) != 0 {
		t.Fatalf("segunda instância não pode discar: %v", rasFake.Calls())
	}
	if b, _ := os.ReadFile(filepath.Join(te.dir, "state.json")); string(b) != `{"pauses":{"matriz":{"indefinite":true}}}` {
		t.Fatalf("segunda instância não pode regravar state.json: %s", b)
	}
	if _, err := os.Stat(filepath.Join(te.dir, "config.json")); err == nil {
		t.Fatal("segunda instância não pode criar config.json")
	}
}

func TestNoArgsAsServiceRunsService(t *testing.T) {
	te := newTestEnv(t)
	te.isService = func() (bool, error) { return true, nil }
	called := false
	te.runService = func(h svc.Hooks) error { called = h.Run != nil && h.OnResume != nil; return nil }
	if code := te.run(); code != 0 || !called {
		t.Fatalf("%d %v", code, called)
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./cmd/vpnmon-svc/`  
Expected: FAIL — `undefined: serve`, `undefined: serviceMain`…

- [ ] **Step 3: Implementar**

Sequência de subida: ACL da pasta → log → **pipe** (é a trava de instância única: o go-winio cria a primeira instância com
`FILE_FLAG_FIRST_PIPE_INSTANCE`; se outro `vpnmon-svc` já roda, sai aqui sem discar nem tocar em `config.json`/`state.json`) →
config (seed no primeiro início; inválida → sobe sem VPNs, marca `MarkDiskInvalid` e não sobrescreve) → `state.json` → cofre →
orquestrador → observadores (config 250 ms/1 s, cofre 1 s/1 s, desconexão RAS, rede com espera de 2 s) → servidor do pipe.
`OnResume` chama `go o.PowerResume()`: o laço do SCM não pode esperar os supervisores.
Parada: observadores → `o.Stop()` (supervisores, `serviceStopping`, `state.json`) → fecha o pipe; prazo ≤ 10 s, sem derrubar VPNs.
O teste de integração roda o serviço inteiro no Linux com a plataforma falsa e o pipe trocado por TCP.

`cmd/vpnmon-svc/app.go`:

```go
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/signal"
	"sync/atomic"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/netwatch"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/svc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/credentials"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/adapters"
	"github.com/guibsu/vpn-tray-monitor/internal/features/monitor/service"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
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

func fileHash(path string) func() string {
	return func() string {
		b, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
}

// serve roda o serviço até ctx terminar. A parada encerra supervisores
// (discagens em curso desligam), avisa serviceStopping, grava o estado e
// fecha o pipe — sem derrubar VPNs conectadas. ready, se não nil, recebe o
// orquestrador quando tudo está de pé.
func serve(ctx context.Context, p Platform, l layout, clock shared.Clock, ready func(*service.Orchestrator)) error {
	if _, err := p.ACL.EnsureDir(l.Dir); err != nil {
		p.Events.Error("pasta de dados: " + err.Error())
		return fmt.Errorf("pasta de dados %s: %w", l.Dir, err)
	}
	for _, d := range []string{l.Credentials, l.Dir + string(os.PathSeparator) + "logs"} {
		if err := os.MkdirAll(d, 0o700); err != nil {
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

	// O pipe é aberto antes de qualquer outra coisa: o go-winio cria a
	// primeira instância com FILE_FLAG_FIRST_PIPE_INSTANCE, então ele também é
	// a trava de instância única. Se outro vpnmon-svc já roda, saímos aqui sem
	// discar nem tocar em config.json/state.json.
	ln, err := p.Listen()
	if err != nil {
		msg := "abrindo o pipe " + ipc.PipeName + " (outro VPN Monitor em execução?): " + err.Error()
		log.Error(msg)
		p.Events.Error(msg)
		return errors.New(msg)
	}
	defer ln.Close()

	cfg, boot, cfgErr := config.LoadOrCreate(l.ConfigFile, p.ReadSeed)
	if cfgErr != nil {
		// Degrada em vez de cair: sobe sem VPNs e espera o arquivo ser corrigido.
		msg := "config.json inválido; o serviço segue sem VPNs até ser corrigido: " + cfgErr.Error()
		log.Error(msg)
		p.Events.Error(msg)
		cfg = config.Empty()
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
		if data, err := os.ReadFile(l.ConfigFile); err == nil {
			o.MarkWritten(data)
		}
	} else {
		o.MarkDiskInvalid(cfgErr) // não deixa "vpn add" sobrescrever o arquivo
	}
	octx, ocancel := context.WithCancel(context.Background())
	defer ocancel()
	o.Start(octx)

	wctx, wcancel := context.WithCancel(ctx)
	defer wcancel()
	cfgWatch := shared.NewPollWatcher(clock, 250*time.Millisecond, time.Second, fileHash(l.ConfigFile))
	credWatch := shared.NewPollWatcher(clock, time.Second, time.Second, vault.DirFingerprint)
	go cfgWatch.Run(wctx)
	go credWatch.Run(wctx)
	rasDown, err := p.RAS.WatchDisconnects(wctx)
	if err != nil {
		log.Warn("sem aviso de desconexão do RAS; seguindo só com o temporizador", "erro", err)
	}
	netChanged := netwatch.Debounce(wctx, clock, p.Net.Changes(), netwatch.DefaultQuiet)
	go func() {
		for {
			select {
			case <-wctx.Done():
				return
			case <-cfgWatch.C():
				o.ReloadFromDisk()
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
	srvDone := make(chan error, 1)
	go func() { srvDone <- srv.Serve(sctx, ln) }()

	log.Info("serviço iniciado", "versao", version, "vpns", len(cfg.VPNs))
	p.Events.Info(fmt.Sprintf("VPN Monitor %s iniciado", version))
	if ready != nil {
		ready(o)
	}

	<-ctx.Done()
	log.Info("parando o serviço")
	wcancel()
	o.Stop()
	ocancel()
	scancel()
	<-srvDone
	log.Info("serviço parado")
	p.Events.Info("VPN Monitor parado")
	return nil
}

// serviceMain é o caminho quando o SCM inicia o processo.
func serviceMain(e env) int {
	var orch atomic.Pointer[service.Orchestrator]
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
		// Fora do laço do SCM: PowerResume fala com cada supervisor e não pode
		// atrasar a resposta a stop/preshutdown.
		OnResume: func() {
			if o := orch.Load(); o != nil {
				go o.PowerResume()
			}
		},
	})
	if err != nil {
		return 1
	}
	return 0
}

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
```

Em `cmd/vpnmon-svc/cli.go`, sem argumentos e iniciado pelo SCM, o processo vira serviço: trocar

```go
	if len(args) == 0 {
		fmt.Fprint(e.stderr, usage)
		return 2
	}
```

por

```go
	if len(args) == 0 {
		if ok, _ := e.isService(); ok {
			return serviceMain(e)
		}
		fmt.Fprint(e.stderr, usage)
		return 2
	}
```

Em `cmd/vpnmon-svc/cli.go`, ligar o modo console: trocar

```go
	switch cmd {
	case "install":
```

por

```go
	switch cmd {
	case "run":
		return cmdRun(e)
	case "install":
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race -count=2 ./cmd/vpnmon-svc/`  
Expected: PASS

Run: `go vet ./... && GOOS=windows go vet ./...`  
Expected: sem saída

Run: `GOOS=windows CGO_ENABLED=0 go build -o /dev/null ./cmd/vpnmon-svc`  
Expected: compila

- [ ] **Step 5: Commit**

```bash
git add cmd/vpnmon-svc && git commit -m "feat(svc): montagem do serviço com observadores, parada em até 10 s e modo console"
```

---

### Task 25: vpnmon-svc: comandos via pipe (status, vpn add/remove/list) e check local

**Files:**
- Create: `cmd/vpnmon-svc/pipecmds.go`
- Modify: `cmd/vpnmon-svc/cli.go` (`dispatch`)
- Test: `cmd/vpnmon-svc/pipecmds_test.go`

**Interfaces:**
- Consumes: `env`, `withClient` usa `env.dial`; `ipc.Client.Call` e tipos (19–20); `config.RawVPN`; `adapters.LinkProber`, `adapters.NewChecker` (15); `domain.FormatOutage`; `startService`/`waitFor` (24).
- Produces: `cmdStatus(env)`, `formatStatus(ipc.Snapshot, now) string`, `cmdVPN(args, env)`, `parseVPNAdd(args) (config.RawVPN, error)` (só os campos passados; o resto fica com os padrões),
  `explain(err)` (erro de campo → nome da flag), `cmdCheck(args, env)` (verificação única no próprio processo, sem discar).

- [ ] **Step 1: Escrever o teste que falha**

`cmd/vpnmon-svc/pipecmds_test.go`:

```go
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

func TestCLIAgainstRunningService(t *testing.T) {
	te := newTestEnv(t)
	startService(t, te)
	status := func() string { te.run("status"); return te.out.String() }
	waitFor(t, func() bool { return strings.Contains(status(), "Conectada") })

	if code := te.run("vpn", "add", "--name", "Filial", "--entry", "VPN Filial", "--check", "link"); code != 0 {
		t.Fatalf("vpn add: %q", te.errb)
	}
	te.run("vpn", "list")
	if !strings.Contains(te.out.String(), "Filial") || !strings.Contains(te.out.String(), "ping 10.0.0.1") {
		t.Fatalf("vpn list: %q", te.out)
	}
	if code := te.run("vpn", "add", "--name", "X", "--entry", "E", "--check", "tcp", "--host", "h"); code != 1 || !strings.Contains(te.errb.String(), "--port") {
		t.Fatalf("erro por campo: %d %q", code, te.errb)
	}
	waitFor(t, func() bool { return strings.Count(status(), "Conectada") == 2 })
	if code := te.run("vpn", "remove", "Filial"); code != 0 {
		t.Fatalf("vpn remove: %q", te.errb)
	}
	if code := te.run("vpn", "remove", "Filial"); code != 1 || !strings.Contains(te.errb.String(), "não existe") {
		t.Fatalf("remover de novo: %d %q", code, te.errb)
	}
}

func TestStatusWithoutServiceFailsClearly(t *testing.T) {
	te := newTestEnv(t)
	if code := te.run("status"); code != 1 || !strings.Contains(te.errb.String(), "inacessível") {
		t.Fatalf("%d %q", code, te.errb)
	}
}

func TestParseVPNAdd(t *testing.T) {
	raw, err := parseVPNAdd([]string{"--name", "Matriz", "--entry", "VPN Matriz", "--host", "10.0.0.1", "--interval", "20"})
	if err != nil {
		t.Fatal(err)
	}
	v := raw.Normalize()
	if v.Check.Kind != config.CheckPing || v.IntervalSeconds != 20 || v.FailuresBeforeReconnect != 3 {
		t.Fatalf("%+v", v)
	}
	raw, _ = parseVPNAdd([]string{"--name", "B", "--entry", "E", "--disabled"})
	if v := raw.Normalize(); v.Check.Kind != config.CheckLink || v.Enabled {
		t.Fatalf("sem host vira link; --disabled: %+v", v)
	}
	if _, err := parseVPNAdd([]string{"--name", "B"}); err == nil {
		t.Fatal("--entry é obrigatório")
	}
}

func TestFormatStatus(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	snap := ipc.Snapshot{VPNs: []ipc.VPNView{
		{Name: "Matriz", State: "Conectada", CheckKind: "ping", SinceUnix: now.Add(-3 * time.Hour).Unix(),
			LastCheckUnix: now.Add(-10 * time.Second).Unix(), LatencyMs: 12, Reconnects24h: 2},
		{Name: "Filial", State: "Reconectando", Attempt: 3, NextAttemptUnix: now.Add(40 * time.Second).Unix(),
			LastError: &ipc.ErrorInfo{Code: 809, Message: "sem resposta"}},
		{Name: "Backup", State: "Pausada", PausedUntilUnix: time.Date(2026, 10, 7, 15, 30, 0, 0, time.Local).Unix()},
	}}
	out := formatStatus(snap, now)
	for _, want := range []string{"há 3 h", "ping 12ms", "2 reconexão(ões) em 24 h", "tentativa 3", "próxima em 40 s", "erro 809: sem resposta", "até 15:30"} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %q em:\n%s", want, out)
		}
	}
	if !strings.Contains(formatStatus(ipc.Snapshot{}, now), "nenhuma VPN") {
		t.Error("vazio")
	}
}
```

- [ ] **Step 2: Rodar e confirmar a falha**

Run: `go test ./cmd/vpnmon-svc/`  
Expected: FAIL — `undefined: parseVPNAdd`, `undefined: formatStatus`.

- [ ] **Step 3: Implementar**

`cmd/vpnmon-svc/pipecmds.go`:

```go
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

func withClient(e env, f func(*ipc.Client) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := e.dial(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return f(c)
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
		if v.NextAttemptUnix > 0 {
			detail = append(detail, fmt.Sprintf("próxima em %s", domain.FormatOutage(time.Unix(v.NextAttemptUnix, 0).Sub(now))))
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
				return explain(err)
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
				return explain(err)
			}
			fmt.Fprintf(e.stdout, "VPN %q removida (a credencial no cofre, se houver, continua: credential clear)\n", args[1])
			return nil
		})
	case "list":
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

// explain detalha erros de validação campo a campo.
func explain(err error) error {
	var ie *ipc.Error
	if errors.As(err, &ie) && len(ie.Fields) > 0 {
		parts := make([]string, len(ie.Fields))
		for i, f := range ie.Fields {
			parts[i] = fmt.Sprintf("--%s: %s", flagFor(f.Field), f.Message)
		}
		return errors.New(strings.Join(parts, "; "))
	}
	return err
}

var fieldFlags = map[string]string{
	"name": "name", "rasEntry": "entry", "check.kind": "check", "check.host": "host", "check.port": "port",
	"check.timeoutSeconds": "timeout", "intervalSeconds": "interval", "failuresBeforeReconnect": "failures",
	"graceAfterConnectSeconds": "grace", "connectTimeoutSeconds": "connect-timeout", "maxBackoffSeconds": "max-backoff",
}

func flagFor(field string) string {
	if f, ok := fieldFlags[field]; ok {
		return f
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

// cmdCheck faz uma verificação única no próprio processo, sem discar.
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
	link, err := adapters.LinkProber{RAS: p.RAS, Net: p.Net}.Probe(ctx, vpn.RasEntry)
	if err != nil {
		return fmt.Errorf("consultando o RAS: %w", err)
	}
	switch {
	case !link.Network:
		fmt.Fprintf(e.stdout, "%s: sem rede física com rota padrão\n", vpn.Name)
	case !link.Up:
		fmt.Fprintf(e.stdout, "%s: enlace caído (entrada %q não conectada)\n", vpn.Name, vpn.RasEntry)
	case vpn.Check.Kind == config.CheckLink:
		fmt.Fprintf(e.stdout, "%s: enlace de pé (verificação link)\n", vpn.Name)
	default:
		r := adapters.NewChecker(vpn.Check, p.Pinger, nil).Check(ctx)
		switch {
		case r.OK:
			fmt.Fprintf(e.stdout, "%s: enlace de pé; %s %s respondeu em %dms\n", vpn.Name, vpn.Check.Kind, vpn.Check.Host, r.RTT.Milliseconds())
		case r.Err != nil:
			fmt.Fprintf(e.stdout, "%s: enlace de pé; %s %s falhou: %v\n", vpn.Name, vpn.Check.Kind, vpn.Check.Host, r.Err)
		default:
			fmt.Fprintf(e.stdout, "%s: enlace de pé; %s %s sem resposta\n", vpn.Name, vpn.Check.Kind, vpn.Check.Host)
		}
	}
	return nil
}
```

Em `cmd/vpnmon-svc/cli.go`, ligar os comandos: trocar

```go
	case "run":
		return cmdRun(e)
	case "install":
```

por

```go
	case "run":
		return cmdRun(e)
	case "status":
		return cmdStatus(e)
	case "check":
		return cmdCheck(rest, e)
	case "vpn":
		return cmdVPN(rest, e)
	case "install":
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test -race ./...`  
Expected: PASS em todos os pacotes

Run: `make lint && make build`  
Expected: `build/vpnmon-svc.exe` gerado

Run: `GOOS=windows go vet ./... && for p in $(go list ./...); do GOOS=windows go test -c -o /dev/null $p || exit 1; done`  
Expected: todos os testes compilam para Windows

- [ ] **Step 5: Commit**

```bash
git add cmd/vpnmon-svc && git commit -m "feat(cli): status, vpn add/remove/list pelo pipe e check local"
```

---

## Cobertura do spec (Marco A)

| Spec | Onde |
|---|---|
| §3.1/§3.2 estrutura e dependências | Mapa de arquivos; Tasks 8–11 (platform + fakes), 16 (credentials sem importar monitor), 24 (`credBridge`) |
| §4.1 ator + `Decide` puro | Tasks 13, 14, 18 |
| §4.2 estados | Task 12 |
| §4.3 ciclo (enlace, alcance, zumbi, carência, `SemRede`) | Tasks 10, 13, 15 |
| §4.4 discagem assíncrona, callback único, fila global, catálogo, credencial salva, pshpack4, adoção | Tasks 7, 8, 15, 17, 18 (`TestAdoptsExistingConnectionWithoutDialing`) |
| §8 parada ≤ 10 s mesmo com verificação presa | Tasks 15 (`TestPingCheckerAbandonsHungEcho`), 21 (`TestStopWithHungEchoIsFast`, `TestStopDeadlineWithHungSupervisor`) |
| §4.5 classificação de erros | Task 7 (tabela), 13 (comportamento) |
| §4.6 despertares (timer, RAS, rede com 2 s, energia/salto) | Tasks 1, 8, 10, 11, 18, 21, 24 |
| §4.7 pausa e comandos manuais | Task 14, 18, 21 |
| §4.8 avisos | Tasks 12, 13, 21 |
| §4.9 orquestrador, recarga parcial, recover | Task 21 |
| §5.1 pasta e ACL | Tasks 10, 24 |
| §5.2 config, leitura estrita, gravação atômica, recarga manual, nome imutável | Tasks 3, 4, 22 |
| §5.3 seed | Task 5, 24 |
| §5.4 cofre, resolução, CLI, observação da pasta | Tasks 9, 16, 23, 24 |
| §5.5 `state.json` | Tasks 5, 21 |
| §5.6 logs e Event Log | Tasks 6, 21, 24 |
| §6 pipe, ACL, PID, limites, protocolo | Tasks 19, 20 |
| §8 serviço e CLI | Tasks 11, 23, 24, 25 |
| §10.1 `ci.yml` mínimo e §10.4 `Makefile` | Task 1 |
| §11 testes (unidade, integração Windows, VPN real opcional) | todas; `VPNMON_REAL_ENTRY` na Task 8 |
| §14 problemas da v1 (2, 4–6, 8, 9, 13, backoff, `Secret`, instância única) | Tasks 2, 7, 13, 18, 21, 23 |

**Fora deste plano:** cliente IPC com reconexão, view-model e bandeja walk (Marco B); MSI WiX, `ci.yml` completo (golangci-lint,
govulncheck, cobertura ≥ 80 %, build com `go-winres`, e2e), `release.yml`, `scripts/sign.ps1`, README final e `docs/TESTE-MANUAL.md` (Marco C).
