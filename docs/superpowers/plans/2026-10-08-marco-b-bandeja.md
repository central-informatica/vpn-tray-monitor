# Marco B — Bandeja: Plano de Implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Entregar o `vpnmon-tray.exe` (windowsgui), uma bandeja por sessão que mostra em tempo real o estado das VPNs do serviço, emite balões reais e permite gerenciar as VPNs (verificar, reconectar, pausar, ativar/desativar, remover, adicionar e editar alvos e intervalos) sem editar JSON.

**Architecture:** MVVM dentro de `internal/features/tray` (§3.1): `client` mantém uma conexão inscrita no pipe `\\.\pipe\vpnmon` e reconecta sozinho; `viewmodel` (puro, sem walk, testado no Linux) transforma snapshot, eventos e estado da conexão num modelo de tela imutável; `view` (só Windows, `github.com/tailscale/walk`) desenha esse modelo e repassa cliques. A bandeja fala com o serviço só pelos tipos de `core/ipc` — nenhuma feature importa outra. `cmd/vpnmon-tray` monta tudo atrás de um mutex por sessão.

**Tech Stack:** Go 1.26 (`go 1.26.5`), `github.com/tailscale/walk` `v0.0.0-20260702185836-28b80ea70d3b` (traz `github.com/tailscale/win`), `github.com/tc-hib/go-winres` v0.3.3 (rodado por `go run …@v0.3.3`, fora do `go.mod`), `golang.org/x/sys` v0.47.0, `github.com/Microsoft/go-winio` v0.6.2 (já no Marco A), GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-10-07-vpn-monitor-v2-design.md` (Marco B na §13 item 2; leia as §§3, 6, 7 e 14 antes de começar). Decisões e pendências do Marco A: `docs/superpowers/plans/2026-10-07-marco-a-decisoes-e-pendencias.md`.

## Global Constraints

- Go `1.26.5` no `go.mod`; build de produção `GOOS=windows GOARCH=amd64 CGO_ENABLED=0` (só x64); a bandeja com `-ldflags "-H windowsgui"`.
- Dependências novas permitidas no Marco B: **só** `github.com/tailscale/walk` (versão fixada acima; ela traz `github.com/tailscale/win`, `github.com/dblohm7/wingoes`, `golang.org/x/exp` e `gopkg.in/Knetic/govaluate.v3` como indiretas) e o `go-winres` v0.3.3 como ferramenta (`go run github.com/tc-hib/go-winres@v0.3.3`, que não entra no `go.mod`).
- Estrutura da §3.1 e regras da §3.2: `features/tray/{client,viewmodel,view}`; **nenhuma feature importa outra** (`tray` não importa `monitor` nem `credentials`; fala só pelos tipos de `core/ipc`); `tray/viewmodel` não conhece walk; todo acesso ao SO via `core/platform` (o mutex vai em `core/platform/instance`).
- Código walk só em arquivos `_windows.go`; o pacote `view` tem um `doc.go` sem build tag para `go vet ./...` no Linux. A view **só compila** no Linux: toda tarefa termina com `go vet ./... && GOOS=windows go vet ./...` limpo e, a partir da Task 12, `GOOS=windows CGO_ENABLED=0 go build ./cmd/vpnmon-tray`.
- Cliente: reconexão automática com backoff até **10 s**; serviço ausente → ícone cinza "serviço parado"; protocolo incompatível → "atualize o VPN Monitor"; servidor do pipe com PID ≠ serviço → conexão recusada (`ipc.Dial` confere, §6.1).
- Ícone = pior estado entre as VPNs ativas (verde, âmbar, vermelho, cinza); tooltip resume as VPNs; balões de `notice` via `NotifyIcon.ShowMessage` (ShowInfo/ShowWarning/ShowError) **respeitando `notifications`**; tempos relativos com tique local de **1 s**; truncamento **por runas** (tooltip e balão também limitados em unidades UTF-16: 127 e 255).
- Menu exatamente com as entradas da §7: cabeçalho "N de M VPNs conectadas", submenu por VPN (detalhe, Verificar agora, Reconectar agora, Pausar ▸ 15 min / 1 h / até retomar, Retomar, Desativar, Remover…), Adicionar VPN ▸ (entradas RAS não monitoradas, criadas com verificação `link`), Configurações…, Abrir log, Sobre / versão, Sair da bandeja (fecha só a bandeja).
- Configurações: lista de VPNs; nome **somente leitura** para existentes (§5.2); Salvar envia `updateVpn`/`addVpn` com o objeto **completo**; erros de validação do serviço (`FieldError`) junto ao campo; recusa sem campos (ex.: "config.json foi alterado no disco…") no aviso geral; opções globais (avisos, nível de log).
- Uma bandeja por sessão: mutex `Local\VPNMonitorTray`. O registro no `HKLM\…\Run` é do MSI (Marco C); a bandeja não se autoinstala.
- Toda `MainWindow` da bandeja chama `SetExitOnClose(false)` (no walk, fechar uma `MainWindow` encerra o aplicativo).
- A dica de credencial é sempre o comando completo que a CLI aceita: `vpnmon-svc credential set "<vpn>" --user <usuário>` (sem `--user` a
  CLI recusa), numa linha própria, nunca truncada.
- Textos ao usuário em português; commits convencionais em pt-BR; nenhum segredo no protocolo nem na interface (credenciais só pela CLI de administrador).

## Review Focus

Entradas e condições que o spec implica, que nenhum teste "natural" das tarefas pegaria e que mais provavelmente atingiriam quem usa a bandeja — cada uma já tem teste na tarefa dona:

1. **Fechar a janela de Configurações ou a de log** não pode encerrar a bandeja (no walk, fechar uma `MainWindow` chama `App().Exit` por padrão) — `TestEveryWindowKeepsTrayAlive` (Task 11).
2. **Serviço mais novo que a bandeja** (atualização parcial) manda tipo de evento ou campo que esta versão não conhece: a bandeja segue funcionando, sem cair em laço de reconexão, e conta o que deixou passar para "Sobre" — `TestSessionConnectedThenSnapshotThenEvents`, `TestSessionIncompatible` (Task 4) e `TestAbout` (Task 8).
3. **Nome de VPN ou de entrada RAS com `&`, acentos, emoji ou longo demais**: o menu não vira atalho nem corta runa ao meio, e o tooltip não estoura as 127 unidades UTF-16 do Windows — `TestVPNItemLabels`, `TestVPNItemDetails`, `TestModelToolTip`, `TestAddEntries` (Tasks 7–8).
4. **Bandeja aberta (login, reconexão) com o `config.json` já inválido**: o serviço só publica `configStatus` quando muda, então o aviso tem de vir no snapshot e não pode sumir ao reconectar — `TestSnapshotCarriesConfigStatus` (Task 1) e `TestModelConfigStatusFromSnapshot` (Task 8).
5. **Bandeja sobe no login antes do serviço, ou o serviço é parado/atualizado com ela aberta**: ícone cinza com o motivo e volta sozinha, sem martelar o pipe (backoff até 10 s) — `TestClientServiceAbsent`, `TestClientReconnectsAfterServiceRestart`, `TestClientBackoffUpTo10s` (Task 5).

## Mapa de arquivos

```
assets/                          assets.go (go:embed dos .ico + leitor de ICO), *.ico (já existem; gerados por tools/geniconos)
cmd/vpnmon-tray/                 main.go (versão), main_windows.go (montagem), main_other.go (stub), winres/winres.json
internal/core/ipc/               protocol.go (+BlockedUntilUnix, ErrNotService, State*/Class*), pipe_windows.go (ErrNotService)
internal/core/platform/instance/ instance.go, instance_windows.go, instance_other.go (mutex por sessão)
internal/features/monitor/service/view.go   (+BlockedUntilUnix no ToView)
internal/features/tray/
  client/                        types.go (ConnState, Conn, Event), session.go (uma conexão), client.go (Run com reconexão, Call)
  viewmodel/                     format.go, vpn.go (submenu), vm.go (Model, VM), commands.go (pedidos), settings.go (formulário)
  view/                          doc.go, tray_windows.go, menu_windows.go, settings_windows.go, logwin_windows.go
Makefile (winres, cover-tray, build do tray), .github/workflows/ci.yml, .gitignore (*.syso), README.md
```

## Decisões tomadas neste plano (não estavam no spec)

- **LastErr sintético de credencial restaurada: dado do serviço, texto da bandeja.** O serviço passa a publicar `VPNView.BlockedUntilUnix` (fim da janela de 15 min de uma `CredencialInvalida` restaurada do `state.json`, que não tem `LastError`); o view-model escreve "Rejeitada antes do reinício do serviço; nova tentativa às HH:MM" no fuso do usuário, mais a dica `vpnmon-svc credential set "<vpn>"` em toda credencial rejeitada (Task 1 e Task 7). Inventar um `LastErr` no serviço poria texto de interface no protocolo e formataria hora no fuso do LocalSystem.
- **Nomes de estado e de classe de erro viram constantes de `core/ipc`** (`ipc.StateConectada`…, `ipc.ClassCredencial`…), amarradas a `domain.State`/`ras.Class` por teste no pacote `service`: a bandeja não pode importar o monitor.
- **`ipc.ErrNotService`** embrulha a recusa por PID divergente no `Dial`, para a bandeja distinguir "pipe de outro processo" de "serviço parado".
- **O menu é montado na hora de abrir** (`ShowingContextMenu`): um menu já aberto não se redesenha no Windows. O tique de 1 s atualiza ícone e tooltip; cada abertura do menu mostra tempos atuais.
- **Cores do ícone:** vermelho = `Desconectada`, `CredencialInvalida`, `ErroConfig`; âmbar = `Degradada`, `Reconectando`, `Desconhecido`, `SemRede` e estados futuros; verde = `Conectada`; `Pausada` e `Desativada` não contam; sem VPN ativa, ou sem conexão, cinza. "Conectadas" conta `Conectada` + `Degradada` (enlace de pé); M = VPNs não desativadas (a pausada conta, como no exemplo da §7).
- **Backoff do cliente:** 500 ms dobrando até 10 s, ±20 %; zera se a sessão durou ≥ 10 s. Incompatível e PID divergente continuam tentando no mesmo ritmo (o serviço pode ser atualizado ou reiniciado).
- **Decodificação tolerante na bandeja** (hello e eventos), estrita no serviço: um serviço mais novo não derruba a bandeja (completa a nota "hello tolerante" do Marco A pelo lado do cliente).
- **Ícones** num pacote raiz `assets` (`go:embed`), com leitor de `.ico` (PNG por tamanho) para escolher pelo DPI; mapeamento verde/âmbar/vermelho/cinza = `conectada`/`conectando`/`desconectada`/`inativa` da v1, gerados por `go run ./tools/geniconos` (inalterado).
- **Recursos do exe por `go-winres` v0.3.3 via `go run …@v0.3.3`** (não entra no `go.mod`: as dependências dele, como `golang.org/x/image` antigo, ficariam no grafo do módulo); `winres.json` versionado, `.syso` gerado por `make winres` e no CI, não versionado. Manifest: comctl32 v6, DPI por monitor v2, `asInvoker`, Windows 10+. Só o `vpnmon-tray` ganha recursos agora; o `vpnmon-svc` ganha no Marco C.
- **"Adicionar VPN"** usa o nome da entrada (sem espaços nas pontas, até 64 runas) e acrescenta " (2)", " (3)"… se já houver uma VPN com o nome (sem diferenciar maiúsculas).
- **Reconectar ao serviço** esquece o `configStatus` ruim e a lista de entradas RAS (o serviço pode ter reiniciado com o arquivo corrigido); a lista RAS é pedida de novo a cada snapshot.
- **A bandeja não grava log** (o usuário não tem pasta própria definida no spec); erros de pedidos aparecem em caixa de mensagem, erros fatais de partida em `MessageBox`.
- **`configStatus` no snapshot** (`Snapshot.Config`): o serviço guarda o último estado da config (inclusive o arquivo inválido desde a
  partida, via `MarkDiskInvalid`) e o manda a cada snapshot; a bandeja não o esquece ao reconectar se o snapshot não o trouxer.
- **Aviso de credencial com `--user`**: o texto do `notice` do domínio (§4.8 dizia só `credential set "X"`) passa a
  `rode vpnmon-svc credential set "X" --user <usuário>`, que é o que a CLI aceita; a bandeja mostra o mesmo comando, inteiro, numa linha
  própria do submenu.
- **Vários avisos de uma vez viram um balão só** ("3 VPNs caíram: A, B, C"; tipos misturados → uma linha por tipo), com o ícone do mais
  grave: o Windows enfileiraria uma rajada de toasts.
- **Contagem do que a decodificação tolerante deixou passar** (eventos descartados, mensagens com campos novos) mostrada em "Sobre", já
  que a bandeja não grava log.
- **Entre o `Connected` e o primeiro snapshot** a bandeja ainda mostra "Conectando…" (não "Nenhuma VPN configurada").
- **`ERROR_ACCESS_DENIED` no `CreateMutex`** (mutex de uma bandeja elevada na mesma sessão) também conta como "já aberta".
- **Troca de DPI percebida no tique de 1 s** (`ni.DPI()` mudou → ícones gerados de novo no tamanho certo): o `NotifyIcon` do walk não
  expõe o `WM_DPICHANGED`.
- **Configurações**: botões de salvar desabilitados até o `getConfig` responder; a janela só copia valores entre controles e o
  view-model (`Form.Text/WithText`, `KindIndex`, `SelectIndex`…), e o "Desativar/Ativar" usa `VPNItem.ToggleCommand()`.
- **Versão fora de tag**: o `git describe` já começa pelo commit, que então não se repete entre parênteses em "Sobre".
- **Remover…** pede confirmação e lembra que a credencial fica no cofre (`vpnmon-svc credential clear`).

## Como verificar cada tarefa

Cada tarefa traz o teste, o comando que deve falhar antes e passar depois, e o commit. A view (`*_windows.go`) e o `main_windows.go`
**só são compilados** no Linux (`GOOS=windows go vet ./...` e, da Task 12 em diante, `GOOS=windows CGO_ENABLED=0 go build ./cmd/vpnmon-tray`);
os `*_windows_test.go` rodam no job `test-windows` do CI. Ao fim de cada tarefa: `make lint` limpo.

O código deste plano já foi executado tarefa a tarefa numa cópia do repositório (Linux, Go 1.26.5): cada teste falha antes da
implementação e passa depois (com `-race`), `gofmt` e `go vet` (linux e windows) limpos, `GOOS=windows CGO_ENABLED=0 go build ./cmd/vpnmon-tray`
e `make build` produzindo os dois exes, com o manifest conferido por `go-winres extract`. A interface walk em si **não foi executada**
(sem Windows): o primeiro uso real é o roteiro manual do fim do plano.

---

### Task 1: Protocolo: prazo do bloqueio de credencial, `ErrNotService` e nomes de estado

**Files:**
- Modify: `internal/core/ipc/protocol.go`
- Modify: `internal/core/ipc/pipe_windows.go`
- Modify: `internal/core/ipc/pipe_windows_test.go`
- Modify: `internal/features/monitor/service/view.go`
- Modify: `internal/features/monitor/service/orchestrator.go`
- Modify: `internal/features/monitor/service/configops.go`
- Modify: `internal/features/monitor/domain/state.go`
- Modify: `internal/features/monitor/domain/decide_test.go`
- Modify: `internal/features/monitor/service/orchestrator_test.go`
- Modify: `internal/features/monitor/service/configops_test.go`
- Create: `internal/features/monitor/service/view_test.go`

**Interfaces:**
- Consumes: `domain.Status.BlockedUntil` (Marco A), `ras.Class.String()`.
- Produces: `ipc.VPNView.BlockedUntilUnix int64` (`json:"blockedUntilUnix,omitempty"`); `var ipc.ErrNotService error` (o `ipc.Dial` do
  Windows devolve `fmt.Errorf("%w: …", ErrNotService, …)`); constantes `ipc.StateDesconhecido`, `StateConectada`, `StateDegradada`,
  `StateReconectando`, `StateDesconectada`, `StateCredencialInvalida`, `StateErroConfig`, `StatePausada`, `StateSemRede`, `StateDesativada`,
  `ipc.ClassTransitorio`, `ClassCredencial`, `ClassConfiguracao`; `ipc.Snapshot.Config *ipc.ConfigStatus` (`json:"config,omitempty"`), que o
  serviço preenche em todo snapshot com o último `configStatus` (inclusive o `config.json` inválido desde a partida); o aviso de credencial
  rejeitada passa a dizer `rode vpnmon-svc credential set "<vpn>" --user <usuário>` (a CLI exige `--user`).

- [ ] **Step 1: Escrever o teste que falha**

Teste novo em `internal/features/monitor/service/view_test.go` (unidade do `ToView` e amarração dos nomes) e um teste de
orquestrador acrescentado a `internal/features/monitor/service/orchestrator_test.go`, logo **antes** de
`// Fora da janela, a rejeição antiga do state.json não vale.` (usa `stubWorld`, `newOrch`, `cfgWith`, `vpnNamed`, `t0` e `waitView`, que já
existem nos testes do pacote).

`internal/features/monitor/service/view_test.go`:

```go
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
```

Acrescentar a `internal/features/monitor/service/orchestrator_test.go`:

```go
// O estado restaurado do state.json chega à bandeja sem LastErr e com o fim
// da janela, para ela explicar o bloqueio.
func TestStartupRejectionViewCarriesBlockedUntil(t *testing.T) {
	w := &stubWorld{network: true}
	st := config.State{Rejections: map[string]config.Rejection{
		"matriz": {RejectedAtUnix: t0.Add(-5 * time.Minute).Unix()},
	}}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), st)
	v := h.waitView("Matriz", domain.CredencialInvalida)
	if v.LastError != nil || v.BlockedUntilUnix != t0.Add(10*time.Minute).Unix() {
		t.Fatalf("visão restaurada: %+v", v)
	}
}
```

Acrescentar ao fim de `internal/features/monitor/service/configops_test.go` (usa `newOrch`, `stubWorld`, `h.paths`; `errors`, `os` e `strings` já são importados):

```go
// O estado do config.json vai em todo snapshot: uma bandeja que se inscreve
// depois do configStatus ruim (ou com o arquivo inválido desde a partida)
// também mostra o aviso; a recarga válida limpa.
func TestSnapshotCarriesConfigStatus(t *testing.T) {
	w := &stubWorld{up: true, network: true}
	h := newOrch(t, w, cfgWith(vpnNamed("Matriz")), config.State{})
	h.waitView("Matriz", domain.Conectada)
	if c := h.o.Status().Config; c == nil || !c.OK {
		t.Fatalf("partida válida: %+v", c)
	}
	h.o.MarkDiskInvalid(errors.New("JSON inválido: unexpected EOF")) // como a montagem faz na partida
	if c := h.o.Status().Config; c == nil || c.OK || !strings.Contains(c.Message, "unexpected EOF") {
		t.Fatalf("inválido na partida: %+v", c)
	}
	_ = os.WriteFile(h.paths.ConfigFile, []byte(`{"version":2,"vpns":[{"name":"Matriz","rasEntry":"VPN Matriz","check":{"kind":"ping"}}]}`), 0o600)
	h.o.ReloadFromDisk()
	events, cancel := h.o.Subscribe()
	defer cancel()
	var snap ipc.Snapshot
	if err := ipc.DecodePayload((<-events).Payload, &snap); err != nil {
		t.Fatal(err)
	}
	if c := snap.Config; c == nil || c.OK || len(c.Fields) == 0 || c.Fields[0].Field != "vpns[0].check.host" {
		t.Fatalf("snapshot de quem se inscreve depois: %+v", c)
	}
	_ = os.WriteFile(h.paths.ConfigFile, []byte(`{"version":2,"vpns":[{"name":"Matriz","rasEntry":"VPN Matriz","check":{"kind":"link"}}]}`), 0o600)
	h.o.ReloadFromDisk()
	if c := h.o.Status().Config; c == nil || !c.OK {
		t.Fatalf("depois de corrigir: %+v", c)
	}
}
```

Em `internal/features/monitor/domain/decide_test.go` (`TestDialResults`), o texto esperado do aviso passa a ter o `--user`:

```go
	if k := noticeKinds(d); len(k) != 1 || k[0] != NoticeCredential || d.Notices[0].Text != "VPN Matriz: credencial rejeitada — rode vpnmon-svc credential set \"Matriz\" --user <usuário>" {
```

Em `internal/core/ipc/pipe_windows_test.go`, `TestWindowsPipePIDCheck` passa a exigir o erro sentinela (só roda no job Windows):

```go
	other := func() (uint32, error) { return 4, nil }
	if _, err := dialVerified(ctx, name, other); !errors.Is(err, ErrNotService) {
		t.Fatalf("PID diferente do serviço deve ser recusado com ErrNotService: %v", err)
	}
```

- [ ] **Step 2: Rodar o teste e ver falhar**

Run: `go test ./internal/features/monitor/... -run "TestToViewBlockedUntil|TestStateNamesMatchProtocol|TestStartupRejectionViewCarriesBlockedUntil|TestSnapshotCarriesConfigStatus|TestDialResults"`
Expected: FAIL — build failed: `v.BlockedUntilUnix undefined (type ipc.VPNView has no field or method BlockedUntilUnix)`, `undefined: ipc.StateDesconhecido`

- [ ] **Step 3: Implementar**

Em `internal/core/ipc/protocol.go`: acrescentar `"errors"` aos imports; logo antes de `// Message é o envelope de toda linha.`:

```go
// ErrNotService: o pipe existe, mas o processo que o serve não é o serviço
// VPNMonitor (PID diferente do informado pelo SCM). A conexão é recusada.
var ErrNotService = errors.New("o pipe não é servido pelo serviço VPN Monitor")
```

logo antes de `// Payloads de pedidos.`:

```go
// Valores de VPNView.State (os mesmos de domain.State; um teste do serviço
// os amarra) e de ErrorInfo.Class: a bandeja os usa sem importar o monitor.
const (
	StateDesconhecido       = "Desconhecido"
	StateConectada          = "Conectada"
	StateDegradada          = "Degradada"
	StateReconectando       = "Reconectando"
	StateDesconectada       = "Desconectada"
	StateCredencialInvalida = "CredencialInvalida"
	StateErroConfig         = "ErroConfig"
	StatePausada            = "Pausada"
	StateSemRede            = "SemRede"
	StateDesativada         = "Desativada"

	ClassTransitorio  = "transitorio"
	ClassCredencial   = "credencial"
	ClassConfiguracao = "configuracao"
)
```

e, em `VPNView`, depois de `PausedIndefinite`:

```go
		PausedIndefinite bool       `json:"pausedIndefinite,omitempty"`
		// BlockedUntilUnix: em CredencialInvalida restaurada do state.json
		// (sem LastError), o instante em que o serviço tenta de novo sozinho.
		BlockedUntilUnix int64 `json:"blockedUntilUnix,omitempty"`
	}
```

Em `internal/core/ipc/pipe_windows.go`, a recusa por PID embrulha o sentinela:

```go
	if pid != want {
		c.Close()
		return nil, fmt.Errorf("%w: %s é servido pelo PID %d, não pelo serviço (PID %d); conexão recusada", ErrNotService, name, pid, want)
	}
```

Em `internal/features/monitor/service/view.go` (`ToView`), depois do bloco de `NextAttemptUnix`:

```go
	if s.State == domain.CredencialInvalida && s.BlockedUntil.After(now) {
		view.BlockedUntilUnix = s.BlockedUntil.Unix()
	}
```

`BlockedUntil` só é preenchido pela memória vinda do `state.json` (`rejectionMemory`) e zera quando a credencial muda ou a janela vence;
uma rejeição ao vivo continua com `LastError` (691…) e sem prazo.

Em `internal/core/ipc/protocol.go`, `Snapshot` ganha o estado da config:

```go
	Snapshot struct {
		VPNs          []VPNView `json:"vpns"`
		Notifications bool      `json:"notifications"`
		// Config é o estado atual do config.json: quem se inscreve depois de
		// um configStatus ruim fica sabendo pelo snapshot.
		Config *ConfigStatus `json:"config,omitempty"`
	}
```

Em `internal/features/monitor/service/orchestrator.go`: no struct `Orchestrator`, depois de `cfg        config.Config`,

```go
	// cfgStatus é o último configStatus publicado; vai em todo snapshot.
	cfgStatus ipc.ConfigStatus
```

em `New`, o literal termina com `removed: map[string]removedCred{}, cfgStatus: ipc.ConfigStatus{OK: true}}`, e `snapshotLocked` começa com

```go
	cs := o.cfgStatus
	snap := ipc.Snapshot{VPNs: []ipc.VPNView{}, Notifications: o.cfg.Notifications, Config: &cs}
```

Em `internal/features/monitor/service/configops.go`, `MarkDiskInvalid` passa a guardar o motivo e ganha dois ajudantes:

```go
// MarkDiskInvalid registra que o config.json em disco está inválido (na
// partida, pela montagem; depois, pela recarga). Mudanças pelo pipe ficam
// recusadas até uma recarga válida.
//
// O motivo passa a ir no snapshot (Snapshot.Config): uma bandeja que se
// inscreve depois, inclusive com o arquivo já inválido na partida, o vê.
func (o *Orchestrator) MarkDiskInvalid(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.diskInvalid = err
	o.cfgStatus = invalidStatus(err)
}

// invalidStatus é o configStatus de um config.json inválido, com os
// problemas por campo quando a validação os aponta.
func invalidStatus(err error) ipc.ConfigStatus {
	st := ipc.ConfigStatus{OK: false, Message: err.Error()}
	var ve *config.ValidationError
	if errors.As(err, &ve) {
		st.Fields = ve.Problems
	}
	return st
}

// publishConfigStatus guarda o estado da config (vai em todo snapshot) e o
// publica, sob o mesmo lock do snapshot: quem se inscreve não perde a troca.
func (o *Orchestrator) publishConfigStatus(st ipc.ConfigStatus) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cfgStatus = st
	o.bus.publish(ipc.MustMessage("", ipc.TypeConfigStatus, st))
}
```

e as cinco publicações de `configStatus` do arquivo passam por ele:

- em `mutate` e nos dois `OK` de `ReloadFromDisk`: `o.publishConfigStatus(ipc.ConfigStatus{OK: true})` no lugar de
  `o.bus.publish(ipc.MustMessage("", ipc.TypeConfigStatus, ipc.ConfigStatus{OK: true}))`;
- no ramo de `config.Parse` com erro em `ReloadFromDisk`, as seis linhas que montavam `st` (com `Fields`) e publicavam viram
  `o.publishConfigStatus(invalidStatus(err))` (o `MarkDiskInvalid(err)` logo antes continua);
- em `ConfigUnreadable`: `o.publishConfigStatus(ipc.ConfigStatus{OK: false, Message: msg})`.

Em `internal/features/monitor/domain/state.go`, `noticeCredential` (a CLI recusa `credential set` sem `--user`):

```go
func noticeCredential(name string) Notice {
	return Notice{NoticeCredential, fmt.Sprintf("VPN %s: credencial rejeitada — rode vpnmon-svc credential set \"%s\" --user <usuário>", name, name)}
}
```

- [ ] **Step 4: Rodar os testes e ver passar**

Run: `go test -race ./internal/features/monitor/... ./internal/core/ipc/`
Expected: PASS

- [ ] **Step 5: Formatação e vet (linux e windows)**

Run: `gofmt -l . && go vet ./... && GOOS=windows go vet ./...`
Expected: nenhuma saída de erro (gofmt sem arquivos listados)

- [ ] **Step 6: Commit**

```bash
git add internal/core/ipc internal/features/monitor/service
git commit -m "feat(ipc): prazo do bloqueio de credencial, estado da config no snapshot, ErrNotService e nomes de estado"
```

---

### Task 2: Ícones embutidos (`assets`) com leitor de `.ico`

**Files:**
- Create: `assets/assets.go`
- Test: `assets/assets_test.go`

**Interfaces:**
- Consumes: `assets/{conectada,conectando,desconectada,inativa}.ico` (já versionados; PNGs de 16, 20, 24, 32 e 48 px gerados por
  `go run ./tools/geniconos`, que não muda).
- Produces: pacote `github.com/guibsu/vpn-tray-monitor/assets` com as constantes `Conectada`, `Conectando`, `Desconectada`, `Inativa`
  (string), `ICO(name string) ([]byte, error)` e `Image(name string, size int) (image.Image, error)` (menor lado ≥ size, ou o maior).

- [ ] **Step 1: Escrever o teste que falha**

`assets/assets_test.go`:

```go
package assets

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestImageEveryIcon(t *testing.T) {
	for _, name := range []string{Conectada, Conectando, Desconectada, Inativa} {
		for _, tc := range []struct{ ask, want int }{{16, 16}, {17, 20}, {32, 32}, {40, 48}, {256, 48}, {0, 16}} {
			img, err := Image(name, tc.ask)
			if err != nil {
				t.Fatalf("%s %d: %v", name, tc.ask, err)
			}
			if got := img.Bounds().Dx(); got != tc.want {
				t.Fatalf("%s pedido %d: veio %d, esperava %d", name, tc.ask, got, tc.want)
			}
		}
	}
}

func TestICOBytes(t *testing.T) {
	b, err := ICO(Conectada)
	if err != nil || len(b) < 6 || binary.LittleEndian.Uint16(b[2:]) != 1 {
		t.Fatalf("ICO: %d bytes, %v", len(b), err)
	}
	if _, err := ICO("../go.mod"); err == nil {
		t.Fatal("nome fora da lista deveria falhar")
	}
	if _, err := Image("nenhum", 16); err == nil {
		t.Fatal("ícone inexistente deveria falhar")
	}
}

func TestParseICORejectsGarbage(t *testing.T) {
	good, _ := ICO(Inativa)
	cases := map[string][]byte{
		"curto":             {0, 0, 1},
		"tipo cursor":       append([]byte{0, 0, 2, 0}, good[4:]...),
		"sem entradas":      {0, 0, 1, 0, 0, 0},
		"diretório cortado": good[:10],
		"dados fora":        withOffset(good, 1<<20),
		"não PNG":           withData(good, []byte("BMxxxxxxxx")),
	}
	for name, b := range cases {
		if _, err := pickPNG(b, 16); err == nil {
			t.Errorf("%s: deveria falhar", name)
		}
	}
}

// withOffset aponta a primeira entrada para fora do arquivo.
func withOffset(ico []byte, off uint32) []byte {
	b := bytes.Clone(ico)
	binary.LittleEndian.PutUint32(b[6+12:], off)
	return b
}

// withData troca o conteúdo de todas as entradas por data (só cabeçalhos válidos).
func withData(ico []byte, data []byte) []byte {
	n := int(binary.LittleEndian.Uint16(ico[4:]))
	b := bytes.Clone(ico[:6+16*n])
	off := len(b)
	for i := 0; i < n; i++ {
		e := b[6+16*i:]
		binary.LittleEndian.PutUint32(e[8:], uint32(len(data)))
		binary.LittleEndian.PutUint32(e[12:], uint32(off))
	}
	return append(b, data...)
}
```

- [ ] **Step 2: Rodar o teste e ver falhar**

Run: `go test ./assets/`
Expected: FAIL — build failed: `undefined: Conectada`, `undefined: Image`, `undefined: pickPNG`

- [ ] **Step 3: Implementar**

`assets/assets.go`:

```go
// Package assets embute os ícones da bandeja. Os .ico são gerados por
// `go run ./tools/geniconos` (paleta no código) e versionados; cada um traz
// PNGs de 16, 20, 24, 32 e 48 px.
package assets

import (
	"bytes"
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"slices"
)

//go:embed *.ico
var files embed.FS

// Ícones por cor: verde, âmbar, vermelho e cinza (§7).
const (
	Conectada    = "conectada"
	Conectando   = "conectando"
	Desconectada = "desconectada"
	Inativa      = "inativa"
)

var names = []string{Conectada, Conectando, Desconectada, Inativa}

// ICO devolve o arquivo .ico do ícone.
func ICO(name string) ([]byte, error) {
	if !slices.Contains(names, name) {
		return nil, fmt.Errorf("ícone %q desconhecido", name)
	}
	return files.ReadFile(name + ".ico")
}

// Image devolve a imagem do ícone com o menor lado ≥ size (a maior, se
// nenhuma alcança): a bandeja pede o tamanho do DPI atual.
func Image(name string, size int) (image.Image, error) {
	b, err := ICO(name)
	if err != nil {
		return nil, err
	}
	return pickPNG(b, size)
}

// pickPNG lê o diretório do .ico (ICONDIR + ICONDIRENTRY de 16 bytes) e
// decodifica a entrada escolhida, que tem de ser PNG.
func pickPNG(b []byte, size int) (image.Image, error) {
	if len(b) < 6 || binary.LittleEndian.Uint16(b[0:]) != 0 || binary.LittleEndian.Uint16(b[2:]) != 1 {
		return nil, errors.New("não é um .ico")
	}
	n := int(binary.LittleEndian.Uint16(b[4:]))
	if n == 0 || len(b) < 6+16*n {
		return nil, errors.New(".ico sem entradas ou com diretório cortado")
	}
	best, bestSide := -1, 0
	for i := 0; i < n; i++ {
		side := int(b[6+16*i])
		if side == 0 {
			side = 256
		}
		better := best < 0 ||
			(side >= size && (bestSide < size || side < bestSide)) ||
			(side < size && bestSide < size && side > bestSide)
		if better {
			best, bestSide = i, side
		}
	}
	e := b[6+16*best:]
	length := int64(binary.LittleEndian.Uint32(e[8:]))
	off := int64(binary.LittleEndian.Uint32(e[12:]))
	if off+length > int64(len(b)) {
		return nil, errors.New("entrada do .ico aponta para fora do arquivo")
	}
	data := b[off : off+length]
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return nil, errors.New("entrada do .ico não é PNG")
	}
	return png.Decode(bytes.NewReader(data))
}
```

- [ ] **Step 4: Rodar os testes e ver passar**

Run: `go test -cover ./assets/`
Expected: PASS

- [ ] **Step 5: Formatação e vet (linux e windows)**

Run: `gofmt -l . && go vet ./... && GOOS=windows go vet ./...`
Expected: nenhuma saída de erro (gofmt sem arquivos listados)

- [ ] **Step 6: Commit**

```bash
git add assets/assets.go assets/assets_test.go
git commit -m "feat(assets): ícones da bandeja embutidos e leitor de .ico"
```

---

### Task 3: Trava de instância única por sessão (`core/platform/instance`)

**Files:**
- Create: `internal/core/platform/instance/instance.go`
- Create: `internal/core/platform/instance/instance_other.go`
- Create: `internal/core/platform/instance/instance_windows.go`
- Test: `internal/core/platform/instance/instance_test.go`
- Test: `internal/core/platform/instance/instance_windows_test.go`

**Interfaces:**
- Consumes: `platform.ErrNotSupported`.
- Produces: `instance.TrayMutex` (= `Local\VPNMonitorTray`), `var instance.ErrAlreadyRunning error`,
  `instance.Acquire(name string) (release func(), err error)` — no Windows, `CreateMutex`; já existente **ou acesso negado** (mutex de uma
  bandeja elevada na mesma sessão) → `ErrAlreadyRunning`; `release` idempotente. Fora do Windows devolve `platform.ErrNotSupported`.
  (Interno, testado no Linux) `classify(err, exists, denied error) error`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/core/platform/instance/instance_test.go`:

```go
package instance

import (
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform"
)

func TestTrayMutexName(t *testing.T) {
	if TrayMutex != `Local\VPNMonitorTray` {
		t.Fatalf("nome do mutex: %q (§4.9)", TrayMutex)
	}
}

func TestAcquireOutsideWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("só fora do Windows")
	}
	if _, err := Acquire(TrayMutex); !errors.Is(err, platform.ErrNotSupported) {
		t.Fatalf("esperava ErrNotSupported: %v", err)
	}
}

// Já existe, ou acesso negado (mutex criado por uma bandeja elevada na mesma
// sessão, cuja DACL não deixa esta abri-lo): as duas são "já aberta".
func TestClassify(t *testing.T) {
	exists, denied, other := errors.New("existe"), errors.New("negado"), errors.New("outro")
	if !errors.Is(classify(exists, exists, denied), ErrAlreadyRunning) {
		t.Fatal("já existe")
	}
	if !errors.Is(classify(fmt.Errorf("x: %w", denied), exists, denied), ErrAlreadyRunning) {
		t.Fatal("acesso negado")
	}
	if got := classify(other, exists, denied); got != other {
		t.Fatalf("outro erro: %v", got)
	}
	if classify(nil, exists, denied) != nil {
		t.Fatal("nil")
	}
}
```

`internal/core/platform/instance/instance_windows_test.go`:

```go
//go:build windows

package instance

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// Só no job Windows: a segunda trava do mesmo nome falha; depois de
// liberada, volta a valer.
func TestWindowsAcquireTwice(t *testing.T) {
	name := fmt.Sprintf(`Local\VPNMonitorTrayTeste-%d-%d`, os.Getpid(), time.Now().UnixNano())
	release, err := Acquire(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(name); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("segunda instância: %v", err)
	}
	release()
	release() // idempotente
	again, err := Acquire(name)
	if err != nil {
		t.Fatalf("após liberar: %v", err)
	}
	again()
}
```

- [ ] **Step 2: Rodar o teste e ver falhar**

Run: `go test ./internal/core/platform/instance/`
Expected: FAIL — build failed: `undefined: TrayMutex`, `undefined: Acquire`

- [ ] **Step 3: Implementar**

`internal/core/platform/instance/instance.go`:

```go
// Package instance garante uma única bandeja por sessão com um mutex
// nomeado (§4.9).
package instance

import "errors"

// TrayMutex é o mutex da bandeja: "Local\" o limita à sessão do usuário.
const TrayMutex = `Local\VPNMonitorTray`

// ErrAlreadyRunning: outra instância já detém a trava nesta sessão.
var ErrAlreadyRunning = errors.New("o VPN Monitor já está aberto nesta sessão")

// classify traduz o erro de criar o mutex: "já existe" e "acesso negado"
// (o mutex foi criado por uma bandeja elevada na mesma sessão, e a DACL dele
// não deixa este processo abri-lo) significam que outra instância o detém.
func classify(err, exists, denied error) error {
	if err != nil && (errors.Is(err, exists) || errors.Is(err, denied)) {
		return ErrAlreadyRunning
	}
	return err
}
```

`internal/core/platform/instance/instance_other.go`:

```go
//go:build !windows

package instance

import "github.com/guibsu/vpn-tray-monitor/internal/core/platform"

// Acquire fora do Windows não há mutex nomeado.
func Acquire(string) (func(), error) { return nil, platform.ErrNotSupported }
```

`internal/core/platform/instance/instance_windows.go`:

```go
//go:build windows

package instance

import (
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sys/windows"
)

// Acquire cria o mutex nomeado; se ele já existir (ou for de uma instância
// elevada, acesso negado), outra instância o detém e
// devolve ErrAlreadyRunning. release fecha o handle (idempotente); o Windows
// também o fecha quando o processo termina.
func Acquire(name string) (release func(), err error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateMutex(nil, false, p)
	if errors.Is(classify(err, windows.ERROR_ALREADY_EXISTS, windows.ERROR_ACCESS_DENIED), ErrAlreadyRunning) {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		return nil, fmt.Errorf("criando o mutex %s: %w", name, err)
	}
	var once sync.Once
	return func() { once.Do(func() { windows.CloseHandle(h) }) }, nil
}
```

O `windows.CreateMutex` do `x/sys` devolve o handle **e** `err == ERROR_ALREADY_EXISTS` quando o nome já existe; por isso o handle é
fechado nesse caso. O teste Windows só roda no CI (aqui ele é compilado pelo `GOOS=windows go vet`).

- [ ] **Step 4: Rodar os testes e ver passar**

Run: `go test ./internal/core/platform/instance/`
Expected: PASS

- [ ] **Step 5: Formatação e vet (linux e windows)**

Run: `gofmt -l . && go vet ./... && GOOS=windows go vet ./...`
Expected: nenhuma saída de erro (gofmt sem arquivos listados)

- [ ] **Step 6: Commit**

```bash
git add internal/core/platform/instance
git commit -m "feat(platform): trava de instância única por sessão (mutex nomeado)"
```

---

### Task 4: Cliente da bandeja: tipos e sessão (hello, inscrição, eventos, pedidos)

**Files:**
- Create: `internal/features/tray/client/types.go`
- Create: `internal/features/tray/client/session.go`
- Test: `internal/features/tray/client/helpers_test.go`
- Test: `internal/features/tray/client/session_test.go`

**Interfaces:**
- Consumes: `ipc.Codec`/`ipc.NewCodec`, `ipc.MustMessage`, `ipc.NewMessage`, `ipc.Hello`, `ipc.Error`, `ipc.ProtocolVersion`, tipos de payload
  de `core/ipc`; nos testes, `ipc.Server` real sobre TCP local (`Serve(ctx, net.Listener)`).
- Produces:
  - `type ConnState int` com `Connecting`, `Connected`, `Unavailable`, `Stopping`, `Incompatible`, `NotService` e `String()`.
  - `type Conn struct{ State ConnState; Message, ServerVersion string }`.
  - `type EventKind int` com `EvConn`, `EvSnapshot`, `EvVPNState`, `EvNotice`, `EvConfigStatus`.
  - `type Event struct{ Kind EventKind; Conn Conn; Snapshot ipc.Snapshot; VPN ipc.VPNView; Notice ipc.NoticeEvent; ConfigStatus ipc.ConfigStatus }`.
  - `var ErrNotConnected error`.
  - `type Stats struct{ DroppedEvents, UnknownFields int64 }` (o que a decodificação tolerante descartou ou aproveitou sem campos novos).
  - (interno) `type counters struct{ events, fields atomic.Int64 }` com `stats() Stats`;
    `openSession(conn net.Conn, appVersion string, timeout time.Duration, emit func(Event) bool, ready func(*session), cnt *counters) (*session, error)`;
    `(*session).call(ctx, typ string, payload, out any) error`, `close()`, `done() <-chan struct{}`, `stopping() bool`, `decode(raw, out) error`.
  - Nos testes: `fakeBackend`/`newBackend()`, `startServer(t, b) *server` (`addr`, `stop()`), `dialTCP(addr)`, `next(t, ch)` e
    `fakeHelloServer(t, reply)` — reaproveitados pela Task 5.

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/tray/client/helpers_test.go`:

```go
package client

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

// fakeBackend é o serviço visto pelo servidor ipc real dos testes.
type fakeBackend struct {
	mu     sync.Mutex
	events chan ipc.Message
	calls  []string
}

func newBackend() *fakeBackend {
	b := &fakeBackend{events: make(chan ipc.Message, 16)}
	b.events <- ipc.MustMessage("", ipc.TypeSnapshot, ipc.Snapshot{
		VPNs: []ipc.VPNView{{Name: "Matriz", State: "Conectada"}}, Notifications: true})
	return b
}

func (b *fakeBackend) rec(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, s)
}

func (b *fakeBackend) Status() ipc.Snapshot {
	return ipc.Snapshot{VPNs: []ipc.VPNView{{Name: "Matriz", State: "Conectada"}}}
}
func (b *fakeBackend) CheckNow(v string) error { b.rec("checkNow " + v); return nil }
func (b *fakeBackend) Reconnect(string) error {
	return &ipc.Error{Code: ipc.CodePaused, Message: "VPN pausada"}
}
func (b *fakeBackend) Pause(string, *time.Time) error          { return nil }
func (b *fakeBackend) Resume(string) error                     { return nil }
func (b *fakeBackend) SetEnabled(string, bool) error           { return nil }
func (b *fakeBackend) AddVPN(config.RawVPN) error              { return nil }
func (b *fakeBackend) UpdateVPN(string, config.RawVPN) error   { return nil }
func (b *fakeBackend) RemoveVPN(string) error                  { return nil }
func (b *fakeBackend) ListRasEntries() ([]ipc.RasEntry, error) { return nil, nil }
func (b *fakeBackend) GetConfig() config.Config                { return config.Empty() }
func (b *fakeBackend) SetGlobal(ipc.SetGlobalRequest) error    { return nil }
func (b *fakeBackend) LogTail(int) (string, error)             { return "fim do log\n", nil }
func (b *fakeBackend) Subscribe() (<-chan ipc.Message, func()) { return b.events, func() {} }

// server é um ipc.Server real em TCP local.
type server struct {
	addr   string
	cancel context.CancelFunc
	done   chan error
}

func startServer(t *testing.T, b ipc.Backend) *server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &ipc.Server{Backend: b, AppVersion: "2.1.0-svc", HandshakeTimeout: time.Second, WriteTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	srv := &server{addr: ln.Addr().String(), cancel: cancel, done: make(chan error, 1)}
	go func() { srv.done <- s.Serve(ctx, ln) }()
	t.Cleanup(srv.stop)
	return srv
}

func (s *server) stop() {
	s.cancel()
	<-s.done
	s.done <- nil // stop idempotente
}

func dialTCP(addr string) func(context.Context) (net.Conn, error) {
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
}

// next lê o próximo evento ou falha após 2 s.
func next(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("canal de eventos fechado")
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("nenhum evento em 2 s")
	}
	return Event{}
}
```

`internal/features/tray/client/session_test.go`:

```go
package client

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

func openTest(t *testing.T, addr string) (*session, chan Event) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 16)
	s, err := openSession(conn, "2.1.0-tray", time.Second, func(ev Event) bool { events <- ev; return true }, func(*session) {}, &counters{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	return s, events
}

func TestSessionConnectedThenSnapshotThenEvents(t *testing.T) {
	b := newBackend()
	srv := startServer(t, b)
	s, events := openTest(t, srv.addr)
	if ev := next(t, events); ev.Kind != EvConn || ev.Conn.State != Connected || ev.Conn.ServerVersion != "2.1.0-svc" {
		t.Fatalf("1º evento: %+v", ev)
	}
	if ev := next(t, events); ev.Kind != EvSnapshot || len(ev.Snapshot.VPNs) != 1 || !ev.Snapshot.Notifications {
		t.Fatalf("2º evento: %+v", ev)
	}
	b.events <- ipc.MustMessage("", ipc.TypeVPNState, ipc.VPNView{Name: "Matriz", State: "Reconectando", Attempt: 2})
	b.events <- ipc.MustMessage("", ipc.TypeNotice, ipc.NoticeEvent{VPN: "Matriz", Kind: "down", Text: "VPN Matriz caiu"})
	b.events <- ipc.MustMessage("", ipc.TypeConfigStatus, ipc.ConfigStatus{OK: false, Message: "config.json inválido"})
	b.events <- ipc.MustMessage("", "eventoDoFuturo", nil)
	// Serviço mais novo: campo que esta bandeja não conhece não derruba nada.
	b.events <- ipc.MustMessage("", ipc.TypeVPNState, map[string]any{"name": "Filial", "state": "Conectada", "campoNovo": 1})
	if ev := next(t, events); ev.Kind != EvVPNState || ev.VPN.Attempt != 2 {
		t.Fatalf("vpnState: %+v", ev)
	}
	if ev := next(t, events); ev.Kind != EvNotice || ev.Notice.Text != "VPN Matriz caiu" {
		t.Fatalf("notice: %+v", ev)
	}
	if ev := next(t, events); ev.Kind != EvConfigStatus || ev.ConfigStatus.OK {
		t.Fatalf("configStatus: %+v", ev)
	}
	if ev := next(t, events); ev.Kind != EvVPNState || ev.VPN.Name != "Filial" || ev.VPN.State != "Conectada" {
		t.Fatalf("vpnState com campo novo: %+v", ev)
	}
	// Nada vai a log em disco: as contagens aparecem em "Sobre".
	if got := s.cnt.stats(); got != (Stats{DroppedEvents: 1, UnknownFields: 1}) {
		t.Fatalf("contagens: %+v", got)
	}
}

func TestSessionCall(t *testing.T) {
	b := newBackend()
	srv := startServer(t, b)
	s, _ := openTest(t, srv.addr)
	ctx := context.Background()
	var snap ipc.Snapshot
	if err := s.call(ctx, ipc.TypeStatus, nil, &snap); err != nil || snap.VPNs[0].Name != "Matriz" {
		t.Fatalf("status: %+v %v", snap, err)
	}
	if err := s.call(ctx, ipc.TypeCheckNow, ipc.VPNRef{VPN: "Matriz"}, nil); err != nil {
		t.Fatal(err)
	}
	var e *ipc.Error
	if err := s.call(ctx, ipc.TypeReconnect, ipc.VPNRef{VPN: "Matriz"}, nil); !errors.As(err, &e) || e.Code != ipc.CodePaused {
		t.Fatalf("erro do serviço: %v", err)
	}
	var tail ipc.LogTail
	if err := s.call(ctx, ipc.TypeLogTail, ipc.LogTailRequest{MaxBytes: 1000}, &tail); err != nil || tail.Text != "fim do log\n" {
		t.Fatalf("logTail: %+v %v", tail, err)
	}
	// Chamadas concorrentes não se misturam.
	errs := make(chan error, 8)
	for range 8 {
		go func() {
			var sn ipc.Snapshot
			errs <- s.call(ctx, ipc.TypeStatus, nil, &sn)
		}()
	}
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestSessionCallEndsWhenConnectionDrops(t *testing.T) {
	b := newBackend()
	srv := startServer(t, b)
	s, _ := openTest(t, srv.addr)
	srv.stop()
	select {
	case <-s.done():
	case <-time.After(2 * time.Second):
		t.Fatal("sessão não percebeu a queda")
	}
	if err := s.call(context.Background(), ipc.TypeStatus, nil, nil); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("chamada após a queda: %v", err)
	}
}

func TestSessionCallHonoursContext(t *testing.T) {
	// Servidor que responde ao hello e ao subscribe e depois se cala.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		codec := ipc.NewCodec(c)
		for i := 0; ; i++ {
			m, err := codec.Read()
			if err != nil {
				return
			}
			switch m.Type {
			case ipc.TypeHello:
				_ = codec.Write(ipc.MustMessage(m.ID, ipc.TypeHello, ipc.Hello{Protocol: ipc.ProtocolVersion, AppVersion: "x"}))
			case ipc.TypeSubscribe:
				_ = codec.Write(ipc.MustMessage(m.ID, ipc.TypeOK, nil))
			}
		}
	}()
	s, _ := openTest(t, ln.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.call(ctx, ipc.TypeStatus, nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sem resposta: %v", err)
	}
}

// fakeHelloServer responde ao hello com reply e fecha.
func fakeHelloServer(t *testing.T, reply func(id string) ipc.Message) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
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
			codec := ipc.NewCodec(c)
			if m, err := codec.Read(); err == nil {
				_ = codec.Write(reply(m.ID))
			}
			c.Close()
		}
	}()
	return ln.Addr().String()
}

func TestSessionIncompatible(t *testing.T) {
	cases := map[string]func(id string) ipc.Message{
		// Serviço mais novo recusa este cliente.
		"recusa do serviço": func(id string) ipc.Message {
			return ipc.ErrorMessage(id, &ipc.Error{Code: ipc.CodeIncompatible, Message: "protocolo 1 incompatível com o serviço (2); atualize o VPN Monitor"})
		},
		// Serviço de outra versão aceita, mas fala outro protocolo; o hello
		// dele pode trazer campos que este cliente não conhece.
		"hello de outra versão": func(id string) ipc.Message {
			m := ipc.MustMessage(id, ipc.TypeHello, map[string]any{"protocol": 2, "appVersion": "3.0.0", "features": []string{"x"}})
			return m
		},
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			conn, err := net.Dial("tcp", fakeHelloServer(t, reply))
			if err != nil {
				t.Fatal(err)
			}
			_, err = openSession(conn, "2.1.0-tray", time.Second, func(Event) bool { return true }, func(*session) {}, &counters{})
			var e *ipc.Error
			if !errors.As(err, &e) || e.Code != ipc.CodeIncompatible || !strings.Contains(e.Message, "atualize") {
				t.Fatalf("esperava incompatible: %v", err)
			}
		})
	}
}

func TestSessionBusy(t *testing.T) {
	addr := fakeHelloServer(t, func(string) ipc.Message {
		return ipc.ErrorMessage("", &ipc.Error{Code: ipc.CodeBusy, Message: "conexões demais"})
	})
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_, err = openSession(conn, "x", time.Second, func(Event) bool { return true }, func(*session) {}, &counters{})
	var e *ipc.Error
	if !errors.As(err, &e) || e.Code != ipc.CodeBusy {
		t.Fatalf("esperava busy: %v", err)
	}
}
```

- [ ] **Step 2: Rodar o teste e ver falhar**

Run: `go test ./internal/features/tray/client/`
Expected: FAIL — build failed: `undefined: Event`, `undefined: openSession`, `undefined: Connected`

- [ ] **Step 3: Implementar**

`internal/features/tray/client/types.go`:

```go
// Package client é o cliente do pipe da bandeja: mantém uma conexão inscrita
// nos eventos do serviço, reconecta sozinho e repassa os pedidos do menu.
// Não conhece walk nem o view-model; fala só pelos tipos de core/ipc.
package client

import (
	"errors"
	"sync/atomic"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

// ConnState é o estado da conexão com o serviço.
type ConnState int

const (
	// Connecting: ainda não houve resposta (início da bandeja).
	Connecting ConnState = iota
	// Connected: hello trocado e inscrito nos eventos.
	Connected
	// Unavailable: serviço ausente, parado ou conexão perdida; tentando de novo.
	Unavailable
	// Stopping: o serviço avisou que está parando (serviceStopping).
	Stopping
	// Incompatible: protocolo diferente; "atualize o VPN Monitor".
	Incompatible
	// NotService: o pipe é servido por outro processo (PID ≠ serviço).
	NotService
)

func (s ConnState) String() string {
	switch s {
	case Connecting:
		return "conectando"
	case Connected:
		return "conectado"
	case Unavailable:
		return "indisponível"
	case Stopping:
		return "parando"
	case Incompatible:
		return "incompatível"
	case NotService:
		return "pipe não é do serviço"
	}
	return "?"
}

// Conn descreve a conexão. Message traz o motivo (erro do dial, do hello…).
type Conn struct {
	State         ConnState
	Message       string
	ServerVersion string
}

// EventKind diz qual campo de Event vale.
type EventKind int

const (
	EvConn EventKind = iota
	EvSnapshot
	EvVPNState
	EvNotice
	EvConfigStatus
)

// Event é o que a bandeja recebe: mudança de conexão ou evento do serviço.
type Event struct {
	Kind         EventKind
	Conn         Conn
	Snapshot     ipc.Snapshot
	VPN          ipc.VPNView
	Notice       ipc.NoticeEvent
	ConfigStatus ipc.ConfigStatus
}

// ErrNotConnected: não há conexão com o serviço para enviar o pedido.
var ErrNotConnected = errors.New("sem conexão com o serviço VPN Monitor")

// Stats conta o que a decodificação tolerante deixou passar (serviço mais
// novo que a bandeja): eventos de tipo desconhecido ou ilegíveis, que são
// descartados, e mensagens com campos que esta bandeja não conhece, que são
// aproveitadas sem eles. Aparece em "Sobre" (a bandeja não grava log).
type Stats struct {
	DroppedEvents int64
	UnknownFields int64
}

type counters struct{ events, fields atomic.Int64 }

func (c *counters) stats() Stats {
	return Stats{DroppedEvents: c.events.Load(), UnknownFields: c.fields.Load()}
}
```

`internal/features/tray/client/session.go`:

```go
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

// session é uma conexão aberta: hello trocado, inscrita em eventos, com um
// leitor que entrega respostas aos pedidos pendentes e eventos ao emit.
type session struct {
	conn  net.Conn
	codec *ipc.Codec
	emit  func(Event) bool
	cnt   *counters
	// writeTimeout limita cada escrita (servidor travado não prende o menu).
	writeTimeout time.Duration

	mu       sync.Mutex
	writeMu  sync.Mutex
	next     int
	pending  map[string]chan ipc.Message
	finished chan struct{}
	isStop   bool
	once     sync.Once
}

// openSession troca o hello (prazo timeout), chama ready (quem chama passa
// a aceitar pedidos por esta sessão), emite Conn{Connected}, liga o leitor e
// se inscreve nos eventos. cnt recebe as contagens da decodificação tolerante. Erros do serviço chegam como *ipc.Error
// (incompatible, busy…); a conexão é fechada em qualquer erro.
func openSession(conn net.Conn, appVersion string, timeout time.Duration, emit func(Event) bool, ready func(*session), cnt *counters) (*session, error) {
	s := &session{conn: conn, codec: ipc.NewCodec(conn), emit: emit, cnt: cnt, writeTimeout: timeout,
		pending: map[string]chan ipc.Message{}, finished: make(chan struct{})}
	serverApp, err := s.hello(appVersion, timeout)
	if err != nil {
		conn.Close()
		return nil, err
	}
	ready(s)
	if !emit(Event{Kind: EvConn, Conn: Conn{State: Connected, ServerVersion: serverApp}}) {
		conn.Close()
		return nil, context.Canceled
	}
	go s.reader()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := s.call(ctx, ipc.TypeSubscribe, nil, nil); err != nil {
		s.close()
		return nil, fmt.Errorf("inscrição nos eventos: %w", err)
	}
	return s, nil
}

// hello é síncrono (antes do leitor). A resposta é lida de forma tolerante:
// um serviço mais novo pode mandar campos que este cliente não conhece, e o
// que importa é o protocolo.
func (s *session) hello(appVersion string, timeout time.Duration) (string, error) {
	_ = s.conn.SetDeadline(time.Now().Add(timeout))
	defer s.conn.SetDeadline(time.Time{})
	if err := s.codec.Write(ipc.MustMessage("0", ipc.TypeHello, ipc.Hello{Protocol: ipc.ProtocolVersion, AppVersion: appVersion})); err != nil {
		return "", err
	}
	m, err := s.codec.Read()
	if err != nil {
		return "", err
	}
	switch m.Type {
	case ipc.TypeError:
		return "", decodeError(m)
	case ipc.TypeHello:
	default:
		return "", fmt.Errorf("resposta inesperada ao hello: %q", m.Type)
	}
	var h ipc.Hello
	if err := s.decode(m.Payload, &h); err != nil {
		return "", fmt.Errorf("hello ilegível: %w", err)
	}
	if h.Protocol != ipc.ProtocolVersion {
		return "", &ipc.Error{Code: ipc.CodeIncompatible, Message: fmt.Sprintf(
			"o serviço (versão %s) fala o protocolo %d e esta bandeja o %d; atualize o VPN Monitor",
			h.AppVersion, h.Protocol, ipc.ProtocolVersion)}
	}
	return h.AppVersion, nil
}

func decodeError(m ipc.Message) error {
	var e ipc.Error
	if err := json.Unmarshal(m.Payload, &e); err != nil {
		return fmt.Errorf("resposta de erro ilegível: %w", err)
	}
	return &e
}

// reader entrega respostas e eventos até a conexão cair.
func (s *session) reader() {
	defer s.close()
	for {
		m, err := s.codec.Read()
		if err != nil {
			return
		}
		if m.ID != "" {
			s.mu.Lock()
			ch := s.pending[m.ID]
			delete(s.pending, m.ID)
			s.mu.Unlock()
			if ch != nil {
				ch <- m // buffer 1
			}
			continue
		}
		ev, ok := s.event(m)
		if !ok {
			continue // tipo desconhecido (serviço mais novo): ignora
		}
		if !s.emit(ev) {
			return
		}
	}
}

func (s *session) event(m ipc.Message) (Event, bool) {
	var ev Event
	var target any
	switch m.Type {
	case ipc.TypeSnapshot:
		ev.Kind, target = EvSnapshot, &ev.Snapshot
	case ipc.TypeVPNState:
		ev.Kind, target = EvVPNState, &ev.VPN
	case ipc.TypeNotice:
		ev.Kind, target = EvNotice, &ev.Notice
	case ipc.TypeConfigStatus:
		ev.Kind, target = EvConfigStatus, &ev.ConfigStatus
	case ipc.TypeServiceStopping:
		s.mu.Lock()
		s.isStop = true
		s.mu.Unlock()
		return Event{Kind: EvConn, Conn: Conn{State: Stopping, Message: "o serviço VPN Monitor está parando"}}, true
	default:
		s.cnt.events.Add(1)
		return Event{}, false
	}
	if s.decode(m.Payload, target) != nil {
		s.cnt.events.Add(1)
		return Event{}, false
	}
	return ev, true
}

// decode é tolerante: tenta o estrito do protocolo e, se só sobrarem campos
// desconhecidos (serviço mais novo), aproveita a mensagem sem eles e conta.
// Erro só se nem a leitura tolerante der certo.
func (s *session) decode(raw json.RawMessage, out any) error {
	if len(raw) == 0 || ipc.DecodePayload(raw, out) == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	s.cnt.fields.Add(1)
	return nil
}

// call envia um pedido e espera a resposta com o mesmo id.
func (s *session) call(ctx context.Context, typ string, payload any, out any) error {
	s.mu.Lock()
	select {
	case <-s.finished:
		s.mu.Unlock()
		return ErrNotConnected
	default:
	}
	s.next++
	id := strconv.Itoa(s.next)
	ch := make(chan ipc.Message, 1)
	s.pending[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()
	m, err := ipc.NewMessage(id, typ, payload)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
	err = s.codec.Write(m)
	s.writeMu.Unlock()
	if err != nil {
		s.close()
		return ErrNotConnected
	}
	var r ipc.Message
	select {
	case r = <-ch:
	case <-s.finished:
		// A resposta pode ter chegado junto com a queda.
		select {
		case r = <-ch:
		default:
			return ErrNotConnected
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	switch r.Type {
	case ipc.TypeError:
		return decodeError(r)
	case ipc.TypeOK:
		if out != nil && len(r.Payload) > 0 {
			return json.Unmarshal(r.Payload, out)
		}
		return nil
	}
	return fmt.Errorf("resposta inesperada %q", r.Type)
}

func (s *session) close() {
	s.once.Do(func() {
		s.mu.Lock()
		close(s.finished)
		s.mu.Unlock()
		s.conn.Close()
	})
}

func (s *session) done() <-chan struct{} { return s.finished }

// stopping diz se o serviço avisou a parada antes de a conexão cair.
func (s *session) stopping() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isStop
}
```

Pontos que os testes fixam: o `Conn{Connected}` é emitido **antes** do snapshot (o `ready` corre antes dele, para um clique logo
após conectar já ter sessão); a resposta ao hello e os eventos são lidos de forma **tolerante** — estrito primeiro e, se só sobrarem campos
desconhecidos, sem eles, contando em `UnknownFields`; tipo de evento desconhecido ou payload ilegível é descartado e contado em
`DroppedEvents` (as contagens vão para "Sobre"; a bandeja não grava log); erro de nível de conexão (`busy`, sem id) no hello volta como
`*ipc.Error`.

- [ ] **Step 4: Rodar os testes e ver passar**

Run: `go test -race -count=3 ./internal/features/tray/client/`
Expected: PASS

- [ ] **Step 5: Formatação e vet (linux e windows)**

Run: `gofmt -l . && go vet ./... && GOOS=windows go vet ./...`
Expected: nenhuma saída de erro (gofmt sem arquivos listados)

- [ ] **Step 6: Commit**

```bash
git add internal/features/tray/client
git commit -m "feat(tray): sessão do cliente do pipe (hello, inscrição, eventos e pedidos)"
```

---

### Task 5: Cliente da bandeja: reconexão com backoff até 10 s

**Files:**
- Create: `internal/features/tray/client/client.go`
- Test: `internal/features/tray/client/client_test.go`

**Interfaces:**
- Consumes: `openSession`, `session`, `Event`, `Conn`, `ConnState` (Task 4); `shared.Backoff` (Marco A); `ipc.ErrNotService` (Task 1).
- Produces:
  - `type Options struct{ Dial func(context.Context) (net.Conn, error); AppVersion string; Timeout time.Duration; Backoff shared.Backoff;
    After func(time.Duration) <-chan time.Time; Rand func() float64; EventBuffer int }` (padrões: 5 s; 500 ms→10 s ±20 %; `time.After`;
    `rand.Float64`; 64).
  - `New(o Options) *Client`; `(*Client).Run(ctx)` (fecha `Events()` ao voltar); `(*Client).Events() <-chan Event`;
    `(*Client).Call(ctx, typ string, payload, out any) error` (`ErrNotConnected` sem sessão; erro do serviço como `*ipc.Error`);
    `(*Client).Stats() Stats`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/tray/client/client_test.go`:

```go
package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// fastOptions reconecta em milissegundos.
func fastOptions(dial func(context.Context) (net.Conn, error)) Options {
	return Options{Dial: dial, AppVersion: "2.1.0-tray", Timeout: time.Second,
		Backoff: shared.Backoff{Base: 5 * time.Millisecond, Max: 20 * time.Millisecond, Jitter: 0.2}}
}

func runClient(t *testing.T, o Options) *Client {
	t.Helper()
	c := New(o)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		for range c.Events() { // drena até Run fechar o canal
		}
		<-done
	})
	return c
}

// nextConn pula eventos até um de conexão.
func nextConn(t *testing.T, ch <-chan Event) Conn {
	t.Helper()
	for {
		if ev := next(t, ch); ev.Kind == EvConn {
			return ev.Conn
		}
	}
}

// waitConn espera um evento de conexão com o estado dado.
func waitConn(t *testing.T, ch <-chan Event, want ConnState) Conn {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cn := nextConn(t, ch); cn.State == want {
			return cn
		}
	}
	t.Fatalf("estado %v não chegou", want)
	return Conn{}
}

func TestClientConnectsAndCalls(t *testing.T) {
	srv := startServer(t, newBackend())
	c := New(fastOptions(dialTCP(srv.addr)))
	if err := c.Call(context.Background(), ipc.TypeStatus, nil, nil); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("antes de conectar: %v", err)
	}
	c = runClient(t, fastOptions(dialTCP(srv.addr)))
	if cn := nextConn(t, c.Events()); cn.State != Connected || cn.ServerVersion != "2.1.0-svc" {
		t.Fatalf("conexão: %+v", cn)
	}
	if ev := next(t, c.Events()); ev.Kind != EvSnapshot {
		t.Fatalf("depois de conectar vem o snapshot: %+v", ev)
	}
	var snap ipc.Snapshot
	if err := c.Call(context.Background(), ipc.TypeStatus, nil, &snap); err != nil || snap.VPNs[0].Name != "Matriz" {
		t.Fatalf("status: %+v %v", snap, err)
	}
	if got := c.Stats(); got != (Stats{}) {
		t.Fatalf("nada descartado com serviço da mesma versão: %+v", got)
	}
}

// addr muda de servidor durante o teste (serviço reiniciado).
type addr struct{ v atomic.Value }

func (a *addr) set(s string) { a.v.Store(s) }
func (a *addr) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", a.v.Load().(string))
}

func TestClientReconnectsAfterServiceRestart(t *testing.T) {
	b1 := newBackend()
	srv1 := startServer(t, b1)
	var a addr
	a.set(srv1.addr)
	c := runClient(t, fastOptions(a.dial))
	waitConn(t, c.Events(), Connected)

	b1.events <- ipc.MustMessage("", ipc.TypeServiceStopping, struct{}{})
	if cn := waitConn(t, c.Events(), Stopping); !strings.Contains(cn.Message, "parando") {
		t.Fatalf("parando: %+v", cn)
	}
	srv1.stop()
	if cn := waitConn(t, c.Events(), Unavailable); !strings.Contains(cn.Message, "parou") {
		t.Fatalf("depois da parada: %+v", cn)
	}
	if err := c.Call(context.Background(), ipc.TypeStatus, nil, nil); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("sem serviço: %v", err)
	}

	srv2 := startServer(t, newBackend())
	a.set(srv2.addr)
	waitConn(t, c.Events(), Connected)
	if ev := next(t, c.Events()); ev.Kind != EvSnapshot {
		t.Fatalf("reconexão traz snapshot novo: %+v", ev)
	}
	if err := c.Call(context.Background(), ipc.TypeCheckNow, ipc.VPNRef{VPN: "Matriz"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestClientConnectionLostWithoutNotice(t *testing.T) {
	srv := startServer(t, newBackend())
	c := runClient(t, fastOptions(dialTCP(srv.addr)))
	waitConn(t, c.Events(), Connected)
	if ev := next(t, c.Events()); ev.Kind != EvSnapshot { // inscrição concluída
		t.Fatalf("snapshot: %+v", ev)
	}
	srv.stop()
	if cn := waitConn(t, c.Events(), Unavailable); !strings.Contains(cn.Message, "perdida") {
		t.Fatalf("queda sem aviso: %+v", cn)
	}
}

func TestClientNotService(t *testing.T) {
	var dials atomic.Int32
	dial := func(context.Context) (net.Conn, error) {
		dials.Add(1)
		return nil, fmt.Errorf("%w: servido pelo PID 4242", ipc.ErrNotService)
	}
	c := runClient(t, fastOptions(dial))
	if cn := waitConn(t, c.Events(), NotService); !strings.Contains(cn.Message, "4242") {
		t.Fatalf("PID divergente: %+v", cn)
	}
	waitConn(t, c.Events(), NotService) // continua tentando
	if dials.Load() < 2 {
		t.Fatalf("tentativas: %d", dials.Load())
	}
}

func TestClientIncompatibleKeepsTrying(t *testing.T) {
	var a addr
	a.set(fakeHelloServer(t, func(id string) ipc.Message {
		return ipc.ErrorMessage(id, &ipc.Error{Code: ipc.CodeIncompatible, Message: "atualize o VPN Monitor"})
	}))
	c := runClient(t, fastOptions(a.dial))
	if cn := waitConn(t, c.Events(), Incompatible); !strings.Contains(cn.Message, "atualize") {
		t.Fatalf("incompatível: %+v", cn)
	}
	// Serviço atualizado: a bandeja volta sozinha.
	srv := startServer(t, newBackend())
	a.set(srv.addr)
	waitConn(t, c.Events(), Connected)
}

func TestClientServiceAbsent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().String()
	ln.Close()
	c := runClient(t, fastOptions(dialTCP(dead)))
	// O motivo é o erro do Dial como veio (o ipc.Dial já diz "serviço VPN
	// Monitor inacessível: …"); a bandeja não repete o prefixo.
	if cn := waitConn(t, c.Events(), Unavailable); !strings.Contains(cn.Message, dead) ||
		strings.Contains(cn.Message, "inacessível") {
		t.Fatalf("motivo: %+v", cn)
	}
}

func TestClientBackoffUpTo10s(t *testing.T) {
	var mu sync.Mutex
	var delays []time.Duration
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fired := make(chan time.Time)
	close(fired)
	o := Options{
		Dial:       func(context.Context) (net.Conn, error) { return nil, errors.New("pipe inexistente") },
		AppVersion: "x",
		Rand:       func() float64 { return 0.5 }, // sem jitter
		After: func(d time.Duration) <-chan time.Time {
			mu.Lock()
			defer mu.Unlock()
			delays = append(delays, d)
			if len(delays) == 8 {
				cancel()
			}
			return fired
		},
	}
	c := New(o)
	go func() {
		for range c.Events() {
		}
	}()
	c.Run(ctx)
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second,
		8 * time.Second, 10 * time.Second, 10 * time.Second, 10 * time.Second}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(delays) != fmt.Sprint(want) {
		t.Fatalf("esperas %v, esperava %v", delays, want)
	}
}

func TestClientRunClosesEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := New(fastOptions(func(context.Context) (net.Conn, error) { return nil, errors.New("x") }))
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	cancel()
	for range c.Events() {
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run não voltou após o cancelamento")
	}
}
```

- [ ] **Step 2: Rodar o teste e ver falhar**

Run: `go test ./internal/features/tray/client/`
Expected: FAIL — build failed: `undefined: New`, `undefined: Options`

- [ ] **Step 3: Implementar**

`internal/features/tray/client/client.go`:

```go
package client

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"sync"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// Options configura o cliente. Só Dial é obrigatório.
type Options struct {
	// Dial abre a conexão; no Windows, ipc.Dial (confere o PID do servidor e
	// devolve ipc.ErrNotService se divergir).
	Dial       func(context.Context) (net.Conn, error)
	AppVersion string
	// Timeout vale para o dial, o hello, a inscrição e cada escrita. Padrão 5 s.
	Timeout time.Duration
	// Backoff entre tentativas; padrão 500 ms dobrando até 10 s, ±20 % (§7).
	Backoff shared.Backoff
	// After e Rand são injetáveis para os testes; padrão time.After e rand.Float64.
	After func(time.Duration) <-chan time.Time
	Rand  func() float64
	// EventBuffer é o tamanho do canal de eventos; padrão 64.
	EventBuffer int
}

// Client mantém a conexão com o serviço e reconecta sozinho.
type Client struct {
	o      Options
	events chan Event
	cnt    counters
	mu     sync.Mutex
	cur    *session
}

// New cria o cliente; a conexão só começa em Run.
func New(o Options) *Client {
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Backoff == (shared.Backoff{}) {
		o.Backoff = shared.Backoff{Base: 500 * time.Millisecond, Max: 10 * time.Second, Jitter: 0.2}
	}
	if o.After == nil {
		o.After = time.After
	}
	if o.Rand == nil {
		o.Rand = rand.Float64
	}
	if o.EventBuffer == 0 {
		o.EventBuffer = 64
	}
	return &Client{o: o, events: make(chan Event, o.EventBuffer)}
}

// Events entrega mudanças de conexão e eventos do serviço, em ordem. Depois
// de Conn{Connected} vem sempre um snapshot completo. O canal fecha quando
// Run volta. Quem consome precisa acompanhar: o leitor espera por ele, e um
// serviço que não consegue entregar desconecta a bandeja (que reconecta).
func (c *Client) Events() <-chan Event { return c.events }

// Run conecta, se inscreve e reconecta com backoff até ctx terminar.
func (c *Client) Run(ctx context.Context) {
	defer close(c.events)
	emit := func(ev Event) bool {
		select {
		case c.events <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	attempt := 0
	for ctx.Err() == nil {
		cn, lasted := c.connectOnce(ctx, emit)
		if ctx.Err() != nil {
			return
		}
		if lasted >= c.o.Backoff.Max {
			attempt = 0 // sessão longa: a queda não é um laço de falhas
		}
		if !emit(Event{Kind: EvConn, Conn: cn}) {
			return
		}
		attempt++
		select {
		case <-ctx.Done():
			return
		case <-c.o.After(c.o.Backoff.Delay(attempt, c.o.Rand())):
		}
	}
}

// connectOnce faz uma tentativa e, se conectar, fica até a sessão cair.
// Devolve o estado a informar e quanto tempo a sessão durou.
func (c *Client) connectOnce(ctx context.Context, emit func(Event) bool) (Conn, time.Duration) {
	dctx, cancel := context.WithTimeout(ctx, c.o.Timeout)
	conn, err := c.o.Dial(dctx)
	cancel()
	if err != nil {
		if errors.Is(err, ipc.ErrNotService) {
			return Conn{State: NotService, Message: err.Error()}, 0
		}
		// ipc.Dial já explica ("serviço VPN Monitor inacessível: …").
		return Conn{State: Unavailable, Message: err.Error()}, 0
	}
	s, err := openSession(conn, c.o.AppVersion, c.o.Timeout, emit, c.setCurrent, &c.cnt)
	if err != nil {
		c.setCurrent(nil)
		var e *ipc.Error
		if errors.As(err, &e) && e.Code == ipc.CodeIncompatible {
			return Conn{State: Incompatible, Message: e.Message}, 0
		}
		return Conn{State: Unavailable, Message: "serviço VPN Monitor não respondeu: " + err.Error()}, 0
	}
	start := time.Now()
	select {
	case <-s.done():
	case <-ctx.Done():
		s.close()
	}
	c.setCurrent(nil)
	if s.stopping() {
		return Conn{State: Unavailable, Message: "o serviço VPN Monitor parou"}, time.Since(start)
	}
	return Conn{State: Unavailable, Message: "conexão com o serviço VPN Monitor perdida"}, time.Since(start)
}

// setCurrent troca a sessão que atende Call (nil = sem conexão).
func (c *Client) setCurrent(s *session) {
	c.mu.Lock()
	c.cur = s
	c.mu.Unlock()
}

// Call envia um pedido pela conexão atual. Sem conexão: ErrNotConnected.
// Erros do serviço chegam como *ipc.Error.
func (c *Client) Call(ctx context.Context, typ string, payload any, out any) error {
	c.mu.Lock()
	s := c.cur
	c.mu.Unlock()
	if s == nil {
		return ErrNotConnected
	}
	return s.call(ctx, typ, payload, out)
}

// Stats devolve as contagens da decodificação tolerante desde o início.
func (c *Client) Stats() Stats { return c.cnt.stats() }
```

Estados informados: falha do `Dial` → `Unavailable` com o erro como veio (o `ipc.Dial` já diz "serviço VPN Monitor inacessível: …"; a
bandeja não repete o prefixo) ou `NotService` se `errors.Is(err, ipc.ErrNotService)`; `incompatible` no hello → `Incompatible` com a mensagem do serviço; queda depois de `serviceStopping` →
`Unavailable` "o serviço VPN Monitor parou"; queda sem aviso → `Unavailable` "conexão com o serviço VPN Monitor perdida". Esperado na
cobertura: ≥ 85 %.

- [ ] **Step 4: Rodar os testes e ver passar**

Run: `go test -race -count=10 -shuffle=on -cover ./internal/features/tray/client/`
Expected: PASS

- [ ] **Step 5: Formatação e vet (linux e windows)**

Run: `gofmt -l . && go vet ./... && GOOS=windows go vet ./...`
Expected: nenhuma saída de erro (gofmt sem arquivos listados)

- [ ] **Step 6: Commit**

```bash
git add internal/features/tray/client
git commit -m "feat(tray): cliente com reconexão automática (backoff até 10 s), incompatível e PID divergente"
```

---

### Task 6: View-model: textos (durações, horários, truncamento por runas)

**Files:**
- Create: `internal/features/tray/viewmodel/format.go`
- Test: `internal/features/tray/viewmodel/format_test.go`

**Interfaces:**
- Consumes: nada além da biblioteca padrão.
- Produces: `viewmodel.Duration(d time.Duration) string` ("45 s", "4 min", "3 h 5 min", "2 d"); `viewmodel.ClockTime(unix int64, now time.Time)
  string` ("15:30" no mesmo dia, "09/10 08:05" em outro, no fuso de `now`); `viewmodel.Truncate(s string, max int) string` (runas, "…");
  (interno) `truncateUTF16(s string, max int) string`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/tray/viewmodel/format_test.go`:

```go
package viewmodel

import (
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

func TestDuration(t *testing.T) {
	cases := map[time.Duration]string{
		-time.Second:                    "0 s",
		0:                               "0 s",
		1500 * time.Millisecond:         "1 s",
		59 * time.Second:                "59 s",
		time.Minute:                     "1 min",
		59*time.Minute + 59*time.Second: "59 min",
		time.Hour:                       "1 h",
		3*time.Hour + 5*time.Minute:     "3 h 5 min",
		47*time.Hour + 59*time.Minute:   "47 h 59 min",
		48 * time.Hour:                  "2 d",
		100 * time.Hour:                 "4 d",
	}
	for d, want := range cases {
		if got := Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, esperava %q", d, got, want)
		}
	}
}

func TestClockTime(t *testing.T) {
	loc := time.FixedZone("BRT", -3*3600)
	now := time.Date(2026, 10, 8, 14, 0, 0, 0, loc)
	if got := ClockTime(time.Date(2026, 10, 8, 15, 30, 0, 0, loc).Unix(), now); got != "15:30" {
		t.Fatalf("mesmo dia: %q", got)
	}
	// Horário no fuso de quem vê (now), não em UTC.
	if got := ClockTime(time.Date(2026, 10, 8, 18, 30, 0, 0, time.UTC).Unix(), now); got != "15:30" {
		t.Fatalf("fuso: %q", got)
	}
	if got := ClockTime(time.Date(2026, 10, 9, 8, 5, 0, 0, loc).Unix(), now); got != "09/10 08:05" {
		t.Fatalf("outro dia: %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"Matriz", 10, "Matriz"},
		{"Matriz", 6, "Matriz"},
		{"Conexão São Paulo", 8, "Conexão…"},
		{"ãããã", 3, "ãã…"},
		{"abc", 1, "…"},
		{"abc", 0, ""},
		{"", 5, ""},
	}
	for _, c := range cases {
		got := Truncate(c.in, c.max)
		if got != c.want {
			t.Errorf("Truncate(%q, %d) = %q, esperava %q", c.in, c.max, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("Truncate(%q, %d) cortou uma runa ao meio", c.in, c.max)
		}
	}
}

// O tooltip do Windows conta unidades UTF-16 (szTip tem 128 com o NUL):
// emoji fora do BMP ocupam 2 e nunca são partidos.
func TestTruncateUTF16(t *testing.T) {
	if got := truncateUTF16("VPN 😀😀", 7); got != "VPN 😀…" {
		t.Fatalf("emoji: %q", got)
	}
	if got := truncateUTF16("VPN 😀", 6); got != "VPN 😀" {
		t.Fatalf("cabe exato: %q", got)
	}
	long := ""
	for range 200 {
		long += "ç"
	}
	got := truncateUTF16(long, 127)
	if n := len(utf16.Encode([]rune(got))); n != 127 {
		t.Fatalf("tamanho %d", n)
	}
	if truncateUTF16("abc", 0) != "" {
		t.Fatal("max 0")
	}
}
```

- [ ] **Step 2: Rodar o teste e ver falhar**

Run: `go test ./internal/features/tray/viewmodel/`
Expected: FAIL — build failed: `undefined: Duration`, `undefined: truncateUTF16`

- [ ] **Step 3: Implementar**

`internal/features/tray/viewmodel/format.go`:

```go
// Package viewmodel transforma o estado do serviço (snapshot, eventos e
// estado da conexão) no modelo de tela da bandeja: itens de menu, rótulos,
// habilitados, ícone, tooltip e balões. É puro (sem walk, sem relógio
// próprio) e testado no Linux; toda a apresentação, inclusive textos e
// truncamento por runas, mora aqui (§7).
package viewmodel

import (
	"fmt"
	"time"
	"unicode/utf16"
)

// Duration escreve uma duração curta: "45 s", "4 min", "3 h", "3 h 5 min", "2 d".
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		h, m := int(d.Hours()), int(d.Minutes())%60
		if m == 0 {
			return fmt.Sprintf("%d h", h)
		}
		return fmt.Sprintf("%d h %d min", h, m)
	}
	return fmt.Sprintf("%d d", int(d.Hours())/24)
}

// ClockTime escreve um instante Unix no fuso de now: "15:30" no mesmo dia,
// "09/10 08:05" em outro.
func ClockTime(unix int64, now time.Time) string {
	t := time.Unix(unix, 0).In(now.Location())
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("02/01 15:04")
}

// Truncate limita s a max runas, terminando em "…" quando corta (§14 item 12:
// nunca por bytes).
func Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// truncateUTF16 limita s a max unidades UTF-16 (o tooltip e o balão do
// Windows contam assim), sem partir um par substituto.
func truncateUTF16(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(utf16.Encode([]rune(s))) <= max {
		return s
	}
	n := 0
	out := []rune{}
	for _, r := range s {
		w := utf16.RuneLen(r)
		if w < 0 {
			w = 1
		}
		if n+w > max-1 { // reserva 1 para o "…"
			break
		}
		out = append(out, r)
		n += w
	}
	return string(out) + "…"
}
```

- [ ] **Step 4: Rodar os testes e ver passar**

Run: `go test -cover ./internal/features/tray/viewmodel/`
Expected: PASS

- [ ] **Step 5: Formatação e vet (linux e windows)**

Run: `gofmt -l . && go vet ./... && GOOS=windows go vet ./...`
Expected: nenhuma saída de erro (gofmt sem arquivos listados)

- [ ] **Step 6: Commit**

```bash
git add internal/features/tray/viewmodel
git commit -m "feat(tray): textos do view-model (durações, horários, truncamento por runas)"
```

---

### Task 7: View-model: submenu por VPN (rótulo, detalhes, dica de credencial, ações)

**Files:**
- Create: `internal/features/tray/viewmodel/vpn.go`
- Test: `internal/features/tray/viewmodel/vpn_test.go`

**Interfaces:**
- Consumes: `Duration`, `ClockTime`, `Truncate` (Task 6); `ipc.VPNView`, `ipc.ErrorInfo`, `ipc.State*`, `ipc.Class*` (Task 1).
- Produces: `type Icon int` com `IconGray`, `IconGreen`, `IconAmber`, `IconRed` e `String()`; `type VPNItem struct{ Name, Label string;
  Details []string; CanCheck, CanReconnect, CanPause, CanResume, Enabled bool; ToggleLabel string }`; `MenuEscape(s string) string`;
  (internos) `vpnItem(v ipc.VPNView, now time.Time) VPNItem`, `shortState(v, now) string`, `severity(state string) Icon`,
  `inactive(state string) bool`, `maxDetail = 96`; `CredentialCommand(vpn string) string` (`vpnmon-svc credential set "<vpn>" --user <usuário>`). Nos testes: `brt`, `now`, `ago(d)`, `ahead(d)` (usados pelas Tasks 8–10).

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/tray/viewmodel/vpn_test.go`:

```go
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
```

- [ ] **Step 2: Rodar o teste e ver falhar**

Run: `go test ./internal/features/tray/viewmodel/`
Expected: FAIL — build failed: `undefined: vpnItem`, `undefined: IconGreen`

- [ ] **Step 3: Implementar**

`internal/features/tray/viewmodel/vpn.go`:

```go
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
	return [...]string{"cinza", "verde", "âmbar", "vermelho"}[i]
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
	return fmt.Sprintf(`vpnmon-svc credential set "%s" --user <usuário>`, vpn)
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
		CanPause:     usable,
		CanResume:    v.State == ipc.StatePausada,
		Enabled:      v.Enabled,
		ToggleLabel:  "Desativar",
	}
	if !it.Enabled {
		it.ToggleLabel = "Ativar"
	}
	return it
}
```

Aqui se cumprem as duas pendências do Marco A que tocam a bandeja: `CredencialInvalida` sem `LastError` e com `BlockedUntilUnix`
mostra "Rejeitada antes do reinício do serviço; nova tentativa às HH:MM", e toda credencial rejeitada termina com duas linhas,
"Corrija como administrador:" e `vpnmon-svc credential set "<vpn>" --user <usuário>` — o comando numa linha própria e **nunca truncado**
(a CLI exige `--user`; ver `cmd/vpnmon-svc/cli.go`, `credential set`).

- [ ] **Step 4: Rodar os testes e ver passar**

Run: `go test -cover ./internal/features/tray/viewmodel/`
Expected: PASS

- [ ] **Step 5: Formatação e vet (linux e windows)**

Run: `gofmt -l . && go vet ./... && GOOS=windows go vet ./...`
Expected: nenhuma saída de erro (gofmt sem arquivos listados)

- [ ] **Step 6: Commit**

```bash
git add internal/features/tray/viewmodel
git commit -m "feat(tray): submenu por VPN no view-model (rótulos, detalhes, ações)"
```

---

### Task 8: View-model: modelo de tela (cabeçalho, ícone, tooltip, conexão, balões, entradas RAS)

**Files:**
- Create: `internal/features/tray/viewmodel/vm.go`
- Test: `internal/features/tray/viewmodel/vm_test.go`

**Interfaces:**
- Consumes: `vpnItem`, `shortState`, `severity`, `inactive`, `MenuEscape`, `Icon*` (Task 7); `truncateUTF16`, `Truncate` (Task 6);
  `client.Event`, `client.Conn`, `client.ConnState` (Tasks 4–5).
- Produces: `type BalloonKind int` (`BalloonInfo`, `BalloonWarning`, `BalloonError`); `type Balloon struct{ Title, Text string; Kind BalloonKind }`;
  `type AddEntry struct{ Entry, Label string }`; `type Model struct{ Icon Icon; ToolTip, Header, Notice string; Connected bool; VPNs []VPNItem;
  AddEntries []AddEntry; AddNote string }`; `New(appVersion string) *VM`; `(*VM).Apply(client.Event)`; `(*VM).Model(now time.Time) Model`
  (antes do primeiro snapshot de cada conexão, ainda "Conectando…"); `(*VM).TakeBalloon() (Balloon, bool)` (vários avisos pendentes viram
  um balão só); `(*VM).SetRasEntries([]ipc.RasEntry)`; `(*VM).Names() []string`; `(*VM).About(stats client.Stats) string`. O aviso de
  config vem do `configStatus` e do `Snapshot.Config` (Task 1) e não se perde ao reconectar.

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/tray/viewmodel/vm_test.go`:

```go
package viewmodel

import (
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/client"
)

func connected(vm *VM, vpns ...ipc.VPNView) {
	vm.Apply(client.Event{Kind: client.EvConn, Conn: client.Conn{State: client.Connected, ServerVersion: "2.0.0"}})
	vm.Apply(client.Event{Kind: client.EvSnapshot, Snapshot: ipc.Snapshot{VPNs: vpns, Notifications: true}})
}

func TestModelConnectionStates(t *testing.T) {
	cases := []struct {
		conn             client.Conn
		header, tip, msg string
	}{
		{client.Conn{State: client.Connecting}, "Conectando ao serviço VPN Monitor…", "VPN Monitor — conectando ao serviço", ""},
		{client.Conn{State: client.Unavailable, Message: "serviço VPN Monitor inacessível: pipe inexistente"},
			"Serviço VPN Monitor parado", "VPN Monitor — serviço parado", "serviço VPN Monitor inacessível: pipe inexistente"},
		{client.Conn{State: client.Stopping, Message: "o serviço VPN Monitor está parando"},
			"Serviço VPN Monitor parado", "VPN Monitor — serviço parado", "o serviço VPN Monitor está parando"},
		{client.Conn{State: client.Incompatible, Message: "protocolo 2; atualize o VPN Monitor"},
			"Atualize o VPN Monitor", "VPN Monitor — atualize o VPN Monitor", "protocolo 2; atualize o VPN Monitor"},
		{client.Conn{State: client.NotService, Message: "pipe servido pelo PID 4242"},
			"Conexão recusada: o pipe não é do serviço", "VPN Monitor — conexão recusada", "pipe servido pelo PID 4242"},
	}
	for _, c := range cases {
		vm := New("2.0.0")
		connected(vm, ipc.VPNView{Name: "Matriz", State: ipc.StateConectada})
		vm.Apply(client.Event{Kind: client.EvConn, Conn: c.conn})
		m := vm.Model(now)
		if m.Icon != IconGray || m.Header != c.header || m.ToolTip != c.tip || m.Notice != c.msg || m.Connected || len(m.VPNs) != 0 {
			t.Errorf("%v: %+v", c.conn.State, m)
		}
	}
}

func TestModelInitialIsConnecting(t *testing.T) {
	m := New("2.0.0").Model(now)
	if m.Connected || m.Icon != IconGray || !strings.HasPrefix(m.Header, "Conectando") {
		t.Fatalf("%+v", m)
	}
}

func TestModelHeaderAndIcon(t *testing.T) {
	v := func(name, st string) ipc.VPNView {
		return ipc.VPNView{Name: name, State: st, Enabled: st != ipc.StateDesativada}
	}
	cases := []struct {
		name   string
		vpns   []ipc.VPNView
		header string
		icon   Icon
	}{
		{"nenhuma", nil, "Nenhuma VPN configurada", IconGray},
		{"todas desativadas", []ipc.VPNView{v("A", ipc.StateDesativada)}, "Todas as VPNs estão desativadas", IconGray},
		{"uma ok", []ipc.VPNView{v("A", ipc.StateConectada)}, "● 1 de 1 VPN conectada", IconGreen},
		{"exemplo do spec", []ipc.VPNView{v("Matriz", ipc.StateConectada), v("Filial", ipc.StateReconectando), v("Backup", ipc.StatePausada)},
			"● 1 de 3 VPNs conectadas", IconAmber},
		{"degradada conta como conectada", []ipc.VPNView{v("A", ipc.StateConectada), v("B", ipc.StateDegradada)},
			"● 2 de 2 VPNs conectadas", IconAmber},
		{"vermelho vence", []ipc.VPNView{v("A", ipc.StateReconectando), v("B", ipc.StateCredencialInvalida)},
			"● 0 de 2 VPNs conectadas", IconRed},
		{"pausada não pesa", []ipc.VPNView{v("A", ipc.StateConectada), v("B", ipc.StatePausada), v("C", ipc.StateDesativada)},
			"● 1 de 2 VPNs conectadas", IconGreen},
		{"só pausadas", []ipc.VPNView{v("A", ipc.StatePausada)}, "● 0 de 1 VPN conectada", IconGray},
	}
	for _, c := range cases {
		vm := New("2.0.0")
		connected(vm, c.vpns...)
		m := vm.Model(now)
		if m.Header != c.header || m.Icon != c.icon || !m.Connected || len(m.VPNs) != len(c.vpns) {
			t.Errorf("%s: %q %v (%d itens)", c.name, m.Header, m.Icon, len(m.VPNs))
		}
	}
}

func TestModelVPNStateUpdatesAndOrder(t *testing.T) {
	vm := New("2.0.0")
	connected(vm, ipc.VPNView{Name: "Matriz", State: ipc.StateConectada}, ipc.VPNView{Name: "Filial", State: ipc.StateConectada})
	vm.Apply(client.Event{Kind: client.EvVPNState, VPN: ipc.VPNView{Name: "Filial", State: ipc.StateDesconectada}})
	vm.Apply(client.Event{Kind: client.EvVPNState, VPN: ipc.VPNView{Name: "Nova", State: ipc.StateDesconhecido}})
	m := vm.Model(now)
	var names []string
	for _, it := range m.VPNs {
		names = append(names, it.Name)
	}
	if strings.Join(names, ",") != "Matriz,Filial,Nova" || m.Icon != IconRed || !strings.Contains(m.VPNs[1].Label, "Desconectada") {
		t.Fatalf("%v %v %q", names, m.Icon, m.VPNs[1].Label)
	}
	// Snapshot novo (VPN removida) substitui a lista inteira.
	vm.Apply(client.Event{Kind: client.EvSnapshot, Snapshot: ipc.Snapshot{VPNs: []ipc.VPNView{{Name: "Matriz", State: ipc.StateConectada}}}})
	if m := vm.Model(now); len(m.VPNs) != 1 {
		t.Fatalf("snapshot: %d", len(m.VPNs))
	}
	if got := vm.Names(); len(got) != 1 || got[0] != "Matriz" {
		t.Fatalf("Names: %v", got)
	}
}

// O modelo devolvido não muda quando o VM recebe eventos depois.
func TestModelIsImmutable(t *testing.T) {
	vm := New("2.0.0")
	connected(vm, ipc.VPNView{Name: "Matriz", State: ipc.StateConectada})
	m := vm.Model(now)
	vm.Apply(client.Event{Kind: client.EvVPNState, VPN: ipc.VPNView{Name: "Matriz", State: ipc.StateDesconectada}})
	if !strings.Contains(m.VPNs[0].Label, "Conectada") || m.Icon != IconGreen {
		t.Fatalf("modelo antigo mudou: %+v", m)
	}
}

// Tempos relativos andam com o tique de 1 s, sem evento novo.
func TestModelTick(t *testing.T) {
	vm := New("2.0.0")
	connected(vm, ipc.VPNView{Name: "M", State: ipc.StateReconectando, Attempt: 1, NextAttemptUnix: ahead(3 * time.Second)})
	a := vm.Model(now).VPNs[0].Label
	b := vm.Model(now.Add(time.Second)).VPNs[0].Label
	c := vm.Model(now.Add(5 * time.Second)).VPNs[0].Label
	if a != "M  ● Reconectando (tent. 1, próxima em 3 s)" || b != "M  ● Reconectando (tent. 1, próxima em 2 s)" || c != "M  ● Reconectando (tent. 1)" {
		t.Fatalf("%q / %q / %q", a, b, c)
	}
}

func TestModelToolTip(t *testing.T) {
	vm := New("2.0.0")
	connected(vm, ipc.VPNView{Name: "Matriz", State: ipc.StateConectada}, ipc.VPNView{Name: "P&D", State: ipc.StatePausada, PausedIndefinite: true})
	if got := vm.Model(now).ToolTip; got != "VPN Monitor — 1 de 2 conectadas\nMatriz: Conectada\nP&D: Pausada até retomar" {
		t.Fatalf("tooltip: %q", got)
	}
	var many []ipc.VPNView
	for i := range 20 {
		many = append(many, ipc.VPNView{Name: strings.Repeat("😀", 5) + string(rune('A'+i)), State: ipc.StateConectada})
	}
	connected(vm, many...)
	tip := vm.Model(now).ToolTip
	if n := len(utf16.Encode([]rune(tip))); n > maxToolTip || !strings.HasSuffix(tip, "…") {
		t.Fatalf("tooltip com %d unidades: %q", n, tip)
	}
	connected(vm)
	if got := vm.Model(now).ToolTip; got != "VPN Monitor — nenhuma VPN configurada" {
		t.Fatalf("vazio: %q", got)
	}
}

func TestModelConfigStatus(t *testing.T) {
	vm := New("2.0.0")
	connected(vm, ipc.VPNView{Name: "M", State: ipc.StateConectada})
	vm.Apply(client.Event{Kind: client.EvConfigStatus, ConfigStatus: ipc.ConfigStatus{OK: false, Message: "config.json inválido: vpns[0].check.port: obrigatória em tcp"}})
	if got := vm.Model(now).Notice; got != "⚠ config.json inválido: vpns[0].check.port: obrigatória em tcp" {
		t.Fatalf("aviso: %q", got)
	}
	vm.Apply(client.Event{Kind: client.EvConfigStatus, ConfigStatus: ipc.ConfigStatus{OK: true}})
	if got := vm.Model(now).Notice; got != "" {
		t.Fatalf("corrigido: %q", got)
	}
}

// A bandeja aberta com o config.json já inválido (o serviço só publica
// configStatus quando muda) sabe pelo snapshot; reconectar não esquece o
// aviso se o snapshot não trouxer o estado, e o snapshot com OK o limpa.
func TestModelConfigStatusFromSnapshot(t *testing.T) {
	vm := New("2.0.0")
	vm.Apply(client.Event{Kind: client.EvConn, Conn: client.Conn{State: client.Connected}})
	vm.Apply(client.Event{Kind: client.EvSnapshot, Snapshot: ipc.Snapshot{
		Config: &ipc.ConfigStatus{OK: false, Message: "JSON inválido: unexpected EOF"}}})
	if got := vm.Model(now).Notice; got != "⚠ JSON inválido: unexpected EOF" {
		t.Fatalf("snapshot: %q", got)
	}
	vm.Apply(client.Event{Kind: client.EvConn, Conn: client.Conn{State: client.Unavailable}})
	vm.Apply(client.Event{Kind: client.EvConn, Conn: client.Conn{State: client.Connected}})
	vm.Apply(client.Event{Kind: client.EvSnapshot, Snapshot: ipc.Snapshot{}})
	if got := vm.Model(now).Notice; got != "⚠ JSON inválido: unexpected EOF" {
		t.Fatalf("reconexão sem estado no snapshot: %q", got)
	}
	vm.Apply(client.Event{Kind: client.EvSnapshot, Snapshot: ipc.Snapshot{Config: &ipc.ConfigStatus{OK: true}}})
	if got := vm.Model(now).Notice; got != "" {
		t.Fatalf("corrigido no snapshot: %q", got)
	}
}

// Entre o Connected e o primeiro snapshot ainda não se sabe nada das VPNs:
// "conectando", não "Nenhuma VPN configurada".
func TestModelBeforeSnapshot(t *testing.T) {
	vm := New("2.0.0")
	vm.Apply(client.Event{Kind: client.EvConn, Conn: client.Conn{State: client.Connected, ServerVersion: "2.0.0"}})
	m := vm.Model(now)
	if m.Connected || m.Icon != IconGray || m.Header != "Conectando ao serviço VPN Monitor…" || len(m.VPNs) != 0 {
		t.Fatalf("antes do snapshot: %+v", m)
	}
	vm.Apply(client.Event{Kind: client.EvSnapshot, Snapshot: ipc.Snapshot{}})
	if m := vm.Model(now); !m.Connected || m.Header != "Nenhuma VPN configurada" {
		t.Fatalf("depois do snapshot: %+v", m)
	}
}

func notice(vpn, kind string) client.Event {
	return client.Event{Kind: client.EvNotice, Notice: ipc.NoticeEvent{VPN: vpn, Kind: kind, Text: "VPN " + vpn + " " + kind}}
}

func TestBalloons(t *testing.T) {
	vm := New("2.0.0")
	connected(vm, ipc.VPNView{Name: "Matriz", State: ipc.StateConectada})
	if _, ok := vm.TakeBalloon(); ok {
		t.Fatal("sem avisos, sem balão")
	}
	// Um aviso: o texto do serviço, com o ícone do tipo.
	single := []struct {
		kind string
		want BalloonKind
	}{{"down", BalloonWarning}, {"up", BalloonInfo}, {"credential", BalloonError}, {"config", BalloonError}, {"novo", BalloonInfo}}
	for _, c := range single {
		vm.Apply(notice("Matriz", c.kind))
		b, ok := vm.TakeBalloon()
		if !ok || b.Kind != c.want || b.Title != "VPN Monitor" || b.Text != "VPN Matriz "+c.kind {
			t.Errorf("%s: %+v", c.kind, b)
		}
	}
	vm.Apply(client.Event{Kind: client.EvNotice, Notice: ipc.NoticeEvent{VPN: "M", Kind: "down", Text: strings.Repeat("y", 400)}})
	if b, _ := vm.TakeBalloon(); len([]rune(b.Text)) > maxBalloon {
		t.Fatalf("texto longo: %d", len([]rune(b.Text)))
	}
	if _, ok := vm.TakeBalloon(); ok {
		t.Fatal("TakeBalloon esvazia a fila")
	}
	// notifications=false no snapshot: só log, sem balão.
	vm.Apply(client.Event{Kind: client.EvSnapshot, Snapshot: ipc.Snapshot{Notifications: false}})
	vm.Apply(notice("Matriz", "down"))
	if _, ok := vm.TakeBalloon(); ok {
		t.Fatal("avisos desligados")
	}
}

// Vários avisos de uma vez (ex.: a rede caiu e levou três VPNs) viram um
// balão só, com o ícone do mais grave.
func TestBalloonsAggregate(t *testing.T) {
	cases := []struct {
		name  string
		in    []client.Event
		title string
		text  string
		kind  BalloonKind
	}{
		{"três caíram", []client.Event{notice("A", "down"), notice("B", "down"), notice("C", "down")},
			"VPN Monitor — 3 avisos", "3 VPNs caíram: A, B, C", BalloonWarning},
		{"mesma VPN duas vezes", []client.Event{notice("A", "down"), notice("A", "down")},
			"VPN Monitor — 2 avisos", "VPN A caiu", BalloonWarning},
		{"voltaram", []client.Event{notice("A", "up"), notice("B", "up")},
			"VPN Monitor — 2 avisos", "2 VPNs voltaram: A, B", BalloonInfo},
		{"mistura", []client.Event{notice("A", "down"), notice("B", "up"), notice("C", "credential"), notice("A", "up"), notice("D", "novo")},
			"VPN Monitor — 5 avisos", "Caíram: A\nCredencial rejeitada: C\nVoltaram: B, A\nOutros avisos: D", BalloonError},
	}
	for _, c := range cases {
		vm := New("2.0.0")
		connected(vm)
		for _, ev := range c.in {
			vm.Apply(ev)
		}
		b, ok := vm.TakeBalloon()
		if !ok || b.Title != c.title || b.Text != c.text || b.Kind != c.kind {
			t.Errorf("%s: %+v", c.name, b)
		}
	}
}

func TestAddEntries(t *testing.T) {
	vm := New("2.0.0")
	connected(vm)
	if m := vm.Model(now); len(m.AddEntries) != 0 || m.AddNote != "Carregando entradas RAS…" {
		t.Fatalf("antes de carregar: %+v", m)
	}
	vm.SetRasEntries([]ipc.RasEntry{{Name: "VPN Matriz", Monitored: true}, {Name: "P&D", Monitored: false}, {Name: "Filial"}})
	m := vm.Model(now)
	if len(m.AddEntries) != 2 || m.AddEntries[0] != (AddEntry{Entry: "P&D", Label: "P&&D"}) || m.AddEntries[1].Entry != "Filial" ||
		!strings.Contains(m.AddNote, "só de enlace") {
		t.Fatalf("entradas: %+v", m)
	}
	vm.SetRasEntries([]ipc.RasEntry{{Name: "VPN Matriz", Monitored: true}})
	if m := vm.Model(now); len(m.AddEntries) != 0 || !strings.HasPrefix(m.AddNote, "Nenhuma entrada RAS nova") {
		t.Fatalf("todas monitoradas: %+v", m)
	}
	// Desconectou: lista esquecida.
	vm.Apply(client.Event{Kind: client.EvConn, Conn: client.Conn{State: client.Unavailable}})
	connected(vm)
	if m := vm.Model(now); m.AddNote != "Carregando entradas RAS…" {
		t.Fatalf("após reconectar: %+v", m)
	}
}

func TestAbout(t *testing.T) {
	vm := New("2.1.0")
	if got := vm.About(client.Stats{}); got != "VPN Monitor\nBandeja: 2.1.0\nServiço: não conectado" {
		t.Fatalf("%q", got)
	}
	connected(vm)
	if got := vm.About(client.Stats{}); !strings.Contains(got, "Serviço: 2.0.0") || strings.Contains(got, "reconhecidas") {
		t.Fatalf("%q", got)
	}
	// Serviço mais novo: o que a decodificação tolerante deixou passar aparece aqui.
	got := vm.About(client.Stats{DroppedEvents: 2, UnknownFields: 5})
	if !strings.Contains(got, "2 evento(s) descartado(s), 5 com campos novos ignorados") {
		t.Fatalf("%q", got)
	}
}
```

- [ ] **Step 2: Rodar o teste e ver falhar**

Run: `go test ./internal/features/tray/viewmodel/`
Expected: FAIL — build failed: `undefined: VM`, `undefined: New`, `undefined: BalloonWarning`

- [ ] **Step 3: Implementar**

`internal/features/tray/viewmodel/vm.go`:

```go
package viewmodel

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/client"
)

// Limites do Windows, em unidades UTF-16 (szTip[128], szInfo[256], com NUL).
const (
	maxToolTip = 127
	maxBalloon = 255
	maxNotice  = 96
)

// BalloonKind escolhe o ícone do balão.
type BalloonKind int

const (
	BalloonInfo BalloonKind = iota
	BalloonWarning
	BalloonError
)

// Balloon é um aviso a mostrar com NotifyIcon.ShowMessage (toast no 10/11).
type Balloon struct {
	Title string
	Text  string
	Kind  BalloonKind
}

// AddEntry é uma entrada RAS ainda não monitorada (submenu "Adicionar VPN").
type AddEntry struct {
	Entry string // nome real da entrada
	Label string // escapado para menu
}

// Model é o modelo de tela. Cada chamada a VM.Model devolve um valor novo,
// que não muda depois (o VM não guarda referências a ele).
type Model struct {
	Icon    Icon
	ToolTip string
	// Header é a primeira linha do menu (informativa); Notice, se não vazia,
	// a segunda (motivo da desconexão ou config inválida).
	Header string
	Notice string
	// Connected habilita os itens que dependem do serviço.
	Connected  bool
	VPNs       []VPNItem
	AddEntries []AddEntry
	AddNote    string
}

// VM acumula o estado recebido do cliente. Não é seguro para uso
// concorrente: a view o usa só na thread da interface.
type VM struct {
	appVersion    string
	conn          client.Conn
	hasSnapshot   bool // depois de Connected, até o snapshot, ainda "conectando"
	vpns          []ipc.VPNView
	notifications bool
	cfg           ipc.ConfigStatus
	ras           []ipc.RasEntry
	rasLoaded     bool
	pending       []ipc.NoticeEvent
}

// New cria o VM no estado "conectando".
func New(appVersion string) *VM {
	return &VM{appVersion: appVersion, conn: client.Conn{State: client.Connecting}, cfg: ipc.ConfigStatus{OK: true}}
}

// Apply incorpora um evento do cliente.
func (vm *VM) Apply(ev client.Event) {
	switch ev.Kind {
	case client.EvConn:
		vm.conn = ev.Conn
		// Ao conectar (de novo) e ao perder a conexão, a lista de VPNs deixa
		// de valer; o snapshot que segue o Connected repõe. O estado da config
		// fica: o snapshot o traz de novo (Snapshot.Config) e, se não trouxer,
		// o último aviso conhecido continua valendo.
		vm.hasSnapshot = false
		vm.vpns, vm.ras, vm.rasLoaded = nil, nil, false
	case client.EvSnapshot:
		vm.hasSnapshot = true
		vm.vpns = slices.Clone(ev.Snapshot.VPNs)
		vm.notifications = ev.Snapshot.Notifications
		if ev.Snapshot.Config != nil {
			vm.cfg = *ev.Snapshot.Config
		}
	case client.EvVPNState:
		i := slices.IndexFunc(vm.vpns, func(v ipc.VPNView) bool { return v.Name == ev.VPN.Name })
		if i < 0 {
			vm.vpns = append(vm.vpns, ev.VPN)
		} else {
			vm.vpns[i] = ev.VPN
		}
	case client.EvNotice:
		if vm.notifications {
			vm.pending = append(vm.pending, ev.Notice)
		}
	case client.EvConfigStatus:
		vm.cfg = ev.ConfigStatus
	}
}

func balloonKind(noticeKind string) BalloonKind {
	switch noticeKind {
	case "down":
		return BalloonWarning
	case "credential", "config":
		return BalloonError
	}
	return BalloonInfo
}

// noticeGroups é a ordem e os textos dos grupos de um balão agregado: frase
// com várias VPNs, frase com uma, e rótulo quando há tipos misturados. O
// último grupo recebe os tipos que esta bandeja não conhece.
var noticeGroups = []struct{ kind, many, one, label string }{
	{"down", "%d VPNs caíram: %s", "VPN %s caiu", "Caíram"},
	{"credential", "%d VPNs com credencial rejeitada: %s", "VPN %s: credencial rejeitada", "Credencial rejeitada"},
	{"config", "%d VPNs com erro de configuração: %s", "VPN %s: erro de configuração", "Erro de configuração"},
	{"up", "%d VPNs voltaram: %s", "VPN %s voltou", "Voltaram"},
	{"", "%d VPNs com avisos: %s", "Aviso da VPN %s", "Outros avisos"},
}

func noticeGroup(kind string) int {
	for i, g := range noticeGroups[:len(noticeGroups)-1] {
		if g.kind == kind {
			return i
		}
	}
	return len(noticeGroups) - 1
}

// TakeBalloon junta os avisos pendentes num balão só e esvazia a fila
// (ok=false se não há). Um aviso sai com o texto do serviço; vários (ex.: a
// rede caiu e levou três VPNs) viram um resumo com o ícone do mais grave, em
// vez de uma rajada de toasts que o Windows enfileiraria.
func (vm *VM) TakeBalloon() (Balloon, bool) {
	p := vm.pending
	vm.pending = nil
	switch len(p) {
	case 0:
		return Balloon{}, false
	case 1:
		return Balloon{Title: "VPN Monitor", Text: truncateUTF16(p[0].Text, maxBalloon), Kind: balloonKind(p[0].Kind)}, true
	}
	b := Balloon{Title: fmt.Sprintf("VPN Monitor — %d avisos", len(p))}
	names := make([][]string, len(noticeGroups))
	for _, n := range p {
		i := noticeGroup(n.Kind)
		if !slices.Contains(names[i], n.VPN) {
			names[i] = append(names[i], n.VPN)
		}
		b.Kind = max(b.Kind, balloonKind(n.Kind))
	}
	var used []int
	for i := range names {
		if len(names[i]) > 0 {
			used = append(used, i)
		}
	}
	if len(used) == 1 {
		g, list := noticeGroups[used[0]], names[used[0]]
		if len(list) == 1 {
			b.Text = fmt.Sprintf(g.one, list[0])
		} else {
			b.Text = fmt.Sprintf(g.many, len(list), strings.Join(list, ", "))
		}
	} else {
		lines := make([]string, len(used))
		for k, i := range used {
			lines[k] = noticeGroups[i].label + ": " + strings.Join(names[i], ", ")
		}
		b.Text = strings.Join(lines, "\n")
	}
	b.Text = truncateUTF16(b.Text, maxBalloon)
	return b, true
}

// SetRasEntries guarda a resposta de listRasEntries.
func (vm *VM) SetRasEntries(entries []ipc.RasEntry) {
	vm.ras, vm.rasLoaded = slices.Clone(entries), true
}

// Names são os nomes das VPNs conhecidas, na ordem do serviço.
func (vm *VM) Names() []string {
	out := make([]string, len(vm.vpns))
	for i, v := range vm.vpns {
		out[i] = v.Name
	}
	return out
}

// About é o texto de "Sobre / versão". stats são as contagens do cliente:
// mensagens de um serviço mais novo que esta bandeja não entendeu inteiras.
func (vm *VM) About(stats client.Stats) string {
	svc := "não conectado"
	if vm.conn.State == client.Connected {
		svc = vm.conn.ServerVersion
	}
	s := fmt.Sprintf("VPN Monitor\nBandeja: %s\nServiço: %s", vm.appVersion, svc)
	if stats.DroppedEvents > 0 || stats.UnknownFields > 0 {
		s += fmt.Sprintf("\n\nMensagens do serviço não reconhecidas: %d evento(s) descartado(s), %d com campos novos ignorados.\n"+
			"Atualize a bandeja para a versão do serviço.", stats.DroppedEvents, stats.UnknownFields)
	}
	return s
}

// Model monta o modelo de tela para o instante now (tique de 1 s).
func (vm *VM) Model(now time.Time) Model {
	if vm.conn.State != client.Connected || !vm.hasSnapshot {
		return vm.disconnected()
	}
	m := Model{Connected: true, Icon: IconGray}
	total, up := 0, 0
	tip := []string{}
	for _, v := range vm.vpns {
		m.VPNs = append(m.VPNs, vpnItem(v, now))
		tip = append(tip, v.Name+": "+shortState(v, now))
		if v.State != ipc.StateDesativada {
			total++
		}
		if v.State == ipc.StateConectada || v.State == ipc.StateDegradada {
			up++
		}
		if !inactive(v.State) && severity(v.State) > m.Icon {
			m.Icon = severity(v.State)
		}
	}
	switch {
	case len(vm.vpns) == 0:
		m.Header = "Nenhuma VPN configurada"
		m.ToolTip = "VPN Monitor — nenhuma VPN configurada"
	case total == 0:
		m.Header = "Todas as VPNs estão desativadas"
		m.ToolTip = "VPN Monitor — todas as VPNs desativadas"
	default:
		if total == 1 {
			m.Header = fmt.Sprintf("● %d de 1 VPN conectada", up)
		} else {
			m.Header = fmt.Sprintf("● %d de %d VPNs conectadas", up, total)
		}
		head := fmt.Sprintf("VPN Monitor — %d de %d conectadas", up, total)
		m.ToolTip = truncateUTF16(strings.Join(append([]string{head}, tip...), "\n"), maxToolTip)
	}
	if !vm.cfg.OK {
		m.Notice = MenuEscape("⚠ " + Truncate(vm.cfg.Message, maxNotice))
	}
	m.AddEntries, m.AddNote = vm.addEntries()
	return m
}

func (vm *VM) disconnected() Model {
	m := Model{Icon: IconGray, Notice: MenuEscape(Truncate(vm.conn.Message, maxNotice))}
	switch vm.conn.State {
	case client.Connecting, client.Connected: // Connected sem snapshot ainda
		m.Header, m.ToolTip = "Conectando ao serviço VPN Monitor…", "VPN Monitor — conectando ao serviço"
	case client.Incompatible:
		m.Header, m.ToolTip = "Atualize o VPN Monitor", "VPN Monitor — atualize o VPN Monitor"
	case client.NotService:
		m.Header, m.ToolTip = "Conexão recusada: o pipe não é do serviço", "VPN Monitor — conexão recusada"
	default: // Unavailable, Stopping
		m.Header, m.ToolTip = "Serviço VPN Monitor parado", "VPN Monitor — serviço parado"
	}
	return m
}

func (vm *VM) addEntries() ([]AddEntry, string) {
	if !vm.rasLoaded {
		return nil, "Carregando entradas RAS…"
	}
	var out []AddEntry
	for _, e := range vm.ras {
		if !e.Monitored {
			out = append(out, AddEntry{Entry: e.Name, Label: MenuEscape(Truncate(e.Name, maxDetail))})
		}
	}
	if len(out) == 0 {
		return nil, "Nenhuma entrada RAS nova no catálogo de todos os usuários"
	}
	return out, "Cria com verificação só de enlace; ajuste em Configurações…"
}
```

- [ ] **Step 4: Rodar os testes e ver passar**

Run: `go test -cover ./internal/features/tray/viewmodel/`
Expected: PASS

- [ ] **Step 5: Formatação e vet (linux e windows)**

Run: `gofmt -l . && go vet ./... && GOOS=windows go vet ./...`
Expected: nenhuma saída de erro (gofmt sem arquivos listados)

- [ ] **Step 6: Commit**

```bash
git add internal/features/tray/viewmodel
git commit -m "feat(tray): modelo de tela (cabeçalho, ícone, tooltip, conexão, balões)"
```

---

### Task 9: View-model: pedidos do menu (pausar, adicionar por entrada RAS, mensagens de erro)

**Files:**
- Create: `internal/features/tray/viewmodel/commands.go`
- Test: `internal/features/tray/viewmodel/commands_test.go`

**Interfaces:**
- Consumes: `config.RawVPN`, `config.RawCheck`, `config.NameKey`, `config.CheckLink`; payloads de `core/ipc`; `client.ErrNotConnected`.
- Produces: `type Command struct{ Type string; Payload any }` (vai direto em `client.Call(ctx, c.Type, c.Payload, out)`); `CheckNow`,
  `Reconnect`, `Resume`, `Remove(vpn string) Command`; `SetEnabled(vpn string, enabled bool) Command`; `ListRasEntries()`, `GetConfig()`,
  `LogTail() Command`; `type PauseChoice int` (`Pause15Min`, `Pause1Hour`, `PauseUntilResume`); `PauseChoices []struct{ Choice PauseChoice;
  Label string }`; `Pause(vpn string, c PauseChoice, now time.Time) Command`; `AddFromEntry(entry string, existing []string) Command`;
  `ErrorText(err error) string`; `RemoveConfirm(vpn string) string`; `(VPNItem).ToggleCommand() Command` (Desativar/Ativar). Nos testes:
  `wire(t, Command) string` (usado pela Task 10).

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/tray/viewmodel/commands_test.go`:

```go
package viewmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/client"
)

// wire é o payload como vai no pipe.
func wire(t *testing.T, c Command) string {
	t.Helper()
	m, err := ipc.NewMessage("1", c.Type, c.Payload)
	if err != nil {
		t.Fatal(err)
	}
	return c.Type + " " + string(m.Payload)
}

func TestSimpleCommands(t *testing.T) {
	cases := map[string]Command{
		`checkNow {"vpn":"Matriz"}`:                   CheckNow("Matriz"),
		`reconnect {"vpn":"Matriz"}`:                  Reconnect("Matriz"),
		`resume {"vpn":"Matriz"}`:                     Resume("Matriz"),
		`setEnabled {"vpn":"Matriz","enabled":false}`: SetEnabled("Matriz", false),
		`removeVpn {"name":"Matriz"}`:                 Remove("Matriz"),
		`listRasEntries`:                              ListRasEntries(),
		`getConfig`:                                   GetConfig(),
		`logTail {"maxBytes":60000}`:                  LogTail(),
	}
	for want, c := range cases {
		if got := strings.TrimSuffix(wire(t, c), " "); got != want {
			t.Errorf("%q, esperava %q", got, want)
		}
	}
}

func TestToggleCommand(t *testing.T) {
	on := vpnItem(ipc.VPNView{Name: "Matriz", State: ipc.StateConectada, Enabled: true}, now)
	off := vpnItem(ipc.VPNView{Name: "Matriz", State: ipc.StateDesativada}, now)
	if got := wire(t, on.ToggleCommand()); got != `setEnabled {"vpn":"Matriz","enabled":false}` || on.ToggleLabel != "Desativar" {
		t.Fatalf("ativa: %s", got)
	}
	if got := wire(t, off.ToggleCommand()); got != `setEnabled {"vpn":"Matriz","enabled":true}` || off.ToggleLabel != "Ativar" {
		t.Fatalf("desativada: %s", got)
	}
}

func TestPause(t *testing.T) {
	if len(PauseChoices) != 3 || PauseChoices[0].Label != "15 min" || PauseChoices[1].Label != "1 h" || PauseChoices[2].Label != "Até retomar" {
		t.Fatalf("opções: %+v", PauseChoices)
	}
	cases := map[PauseChoice]string{
		Pause15Min:       fmt.Sprintf(`pause {"vpn":"M","untilUnix":%d}`, now.Add(15*time.Minute).Unix()),
		Pause1Hour:       fmt.Sprintf(`pause {"vpn":"M","untilUnix":%d}`, now.Add(time.Hour).Unix()),
		PauseUntilResume: `pause {"vpn":"M","untilUnix":null}`,
	}
	for c, want := range cases {
		if got := wire(t, Pause("M", c, now)); got != want {
			t.Errorf("%v: %q, esperava %q", c, got, want)
		}
	}
}

func TestAddFromEntry(t *testing.T) {
	c := AddFromEntry("  VPN Filial ", []string{"Matriz"})
	if got := wire(t, c); got != `addVpn {"config":{"name":"VPN Filial","rasEntry":"  VPN Filial ","check":{"kind":"link"}}}` {
		t.Fatalf("%s", got)
	}
	// O serviço aceita o pedido: validação local igual à dele.
	req := c.Payload.(ipc.AddVPNRequest)
	if probs := config.ValidateVPN(req.Config.Normalize()); len(probs) != 0 {
		t.Fatalf("inválido: %v", probs)
	}
	// Nome já usado (sem diferenciar maiúsculas) ganha sufixo.
	c = AddFromEntry("matriz", []string{"Matriz", "MATRIZ (2)"})
	if n := c.Payload.(ipc.AddVPNRequest).Config.Name; n != "matriz (3)" {
		t.Fatalf("duplicado: %q", n)
	}
	// Nome de entrada maior que 64 runas é cortado, inclusive com sufixo.
	long := strings.Repeat("ç", 100)
	c = AddFromEntry(long, nil)
	if n := c.Payload.(ipc.AddVPNRequest).Config.Name; utf8.RuneCountInString(n) != 64 {
		t.Fatalf("longo: %d runas", utf8.RuneCountInString(n))
	}
	c = AddFromEntry(long, []string{strings.Repeat("ç", 64)})
	n := c.Payload.(ipc.AddVPNRequest).Config.Name
	if utf8.RuneCountInString(n) != 64 || !strings.HasSuffix(n, " (2)") {
		t.Fatalf("longo duplicado: %q", n)
	}
	if probs := config.ValidateVPN(c.Payload.(ipc.AddVPNRequest).Config.Normalize()); len(probs) != 0 {
		t.Fatalf("inválido: %v", probs)
	}
}

func TestErrorText(t *testing.T) {
	cases := map[string]error{
		"Sem conexão com o serviço VPN Monitor.":       client.ErrNotConnected,
		"O serviço VPN Monitor não respondeu a tempo.": fmt.Errorf("x: %w", context.DeadlineExceeded),
		"VPN pausada": &ipc.Error{Code: ipc.CodePaused, Message: "VPN pausada"},
		"internal":    &ipc.Error{Code: ipc.CodeInternal},
		"disco cheio": errors.New("disco cheio"),
		"credencial já rejeitada; tente novamente em 9 min": fmt.Errorf("w: %w", &ipc.Error{Code: ipc.CodeCredentialRejected, Message: "credencial já rejeitada; tente novamente em 9 min"}),
	}
	for want, err := range cases {
		if got := ErrorText(err); got != want {
			t.Errorf("%v: %q", err, got)
		}
	}
	if ErrorText(nil) != "" {
		t.Fatal("nil")
	}
}

func TestRemoveConfirm(t *testing.T) {
	got := RemoveConfirm("Matriz")
	if !strings.Contains(got, `"Matriz"`) || !strings.Contains(got, "credential clear") {
		t.Fatalf("%q", got)
	}
}

// Command é serializável (o payload vai direto ao pipe).
func TestCommandPayloadsMarshal(t *testing.T) {
	for _, c := range []Command{AddFromEntry("X", nil), Pause("M", Pause15Min, now), SetEnabled("M", true)} {
		if _, err := json.Marshal(c.Payload); err != nil {
			t.Fatal(err)
		}
	}
}
```

- [ ] **Step 2: Rodar o teste e ver falhar**

Run: `go test ./internal/features/tray/viewmodel/`
Expected: FAIL — build failed: `undefined: CheckNow`, `undefined: Command`, `undefined: AddFromEntry`

- [ ] **Step 3: Implementar**

`internal/features/tray/viewmodel/commands.go`:

```go
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

// RemoveConfirm é a pergunta de "Remover…".
func RemoveConfirm(vpn string) string {
	return fmt.Sprintf("Remover a VPN %q do monitoramento?\n\nA conexão não é derrubada e a credencial guardada continua no cofre "+
		"(apague com: vpnmon-svc credential clear %q).", vpn, vpn)
}
```

- [ ] **Step 4: Rodar os testes e ver passar**

Run: `go test -cover ./internal/features/tray/viewmodel/`
Expected: PASS

- [ ] **Step 5: Formatação e vet (linux e windows)**

Run: `gofmt -l . && go vet ./... && GOOS=windows go vet ./...`
Expected: nenhuma saída de erro (gofmt sem arquivos listados)

- [ ] **Step 6: Commit**

```bash
git add internal/features/tray/viewmodel
git commit -m "feat(tray): pedidos do menu (pausar, adicionar por entrada RAS, mensagens de erro)"
```

---

### Task 10: View-model: formulário de Configurações (pedido completo e erros por campo)

**Files:**
- Create: `internal/features/tray/viewmodel/settings.go`
- Test: `internal/features/tray/viewmodel/settings_test.go`

**Interfaces:**
- Consumes: `Command`, `ErrorText` (Task 9); `config.VPN`, `config.RawVPN`, `config.Config`, `config.FieldError`, `config.NameKey`;
  `ipc.Error`, `ipc.AddVPNRequest`, `ipc.UpdateVPNRequest`, `ipc.SetGlobalRequest`.
- Produces: constantes `FieldName`, `FieldRasEntry`, `FieldKind`, `FieldHost`, `FieldPort`, `FieldTimeout`, `FieldInterval`, `FieldFailures`,
  `FieldGrace`, `FieldConnectTimeout`, `FieldMaxBackoff`, `FieldEnabled` (os caminhos dos `FieldError` do serviço); `FormFields []string`;
  `FieldLabels map[string]string`; `CheckKinds`, `LogLevels []string`; `type FieldErrors map[string]string`; `type Form struct{ IsNew bool;
  Name, RasEntry, Kind, Host, Port, Timeout, Interval, Failures, Grace, ConnectTimeout, MaxBackoff string; Enabled bool }`;
  `FormFrom(config.VPN) Form`; `NewForm() Form`; `(Form).HostEnabled()`, `(Form).PortEnabled() bool`; `(Form).Command() (Command, FieldErrors)`;
  `ServiceErrors(err error) (FieldErrors, string)`; `type Globals struct{ Notifications bool; LogLevel string }`; `GlobalsFrom(config.Config)
  Globals`; `(Globals).Command() Command`; `VPNNames(config.Config) []string`; `FindVPN(config.Config, name string) (config.VPN, bool)`.
  Para a janela só copiar valores: `TextFields []string`; `(Form).Text(field) string`, `(Form).WithText(field, value) Form`,
  `(Form).KindIndex() int`, `(Form).WithKindIndex(i int) Form`, `(Form).NameReadOnly() bool`; `(Globals).LevelIndex() int`,
  `(Globals).WithLevelIndex(i int) Globals`; `SelectIndex(names []string, name string) int`; `EntryNames([]ipc.RasEntry) []string`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/features/tray/viewmodel/settings_test.go`:

```go
package viewmodel

import (
	"errors"
	"slices"
	"testing"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

func sampleVPN() config.VPN {
	return config.VPN{Name: "Matriz", RasEntry: "VPN Matriz", Enabled: true,
		Check:           config.Check{Kind: config.CheckTCP, Host: "10.0.0.1", Port: 443, TimeoutSeconds: 5},
		IntervalSeconds: 30, FailuresBeforeReconnect: 3, GraceAfterConnectSeconds: 15, ConnectTimeoutSeconds: 60, MaxBackoffSeconds: 300}
}

func TestFormRoundTripUpdate(t *testing.T) {
	f := FormFrom(sampleVPN())
	if f.IsNew || f.Name != "Matriz" || f.Port != "443" || f.Kind != "tcp" || !f.PortEnabled() || !f.HostEnabled() {
		t.Fatalf("form: %+v", f)
	}
	f.Interval = " 60 "
	f.Enabled = false
	c, errs := f.Command()
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	// Objeto completo, inclusive enabled (§5.2: UpdateVPN preserva enabled
	// omitido, mas a janela sempre manda o que o usuário vê).
	want := `updateVpn {"name":"Matriz","config":{"name":"Matriz","rasEntry":"VPN Matriz","enabled":false,` +
		`"check":{"kind":"tcp","host":"10.0.0.1","port":443,"timeoutSeconds":5},"intervalSeconds":60,` +
		`"failuresBeforeReconnect":3,"graceAfterConnectSeconds":15,"connectTimeoutSeconds":60,"maxBackoffSeconds":300}}`
	if got := wire(t, c); got != want {
		t.Fatalf("\n%s\nesperava\n%s", got, want)
	}
}

func TestNewFormDefaultsAreValid(t *testing.T) {
	f := NewForm()
	if !f.IsNew || f.Kind != "ping" || !f.Enabled || f.Interval != "30" || f.MaxBackoff != "300" || f.PortEnabled() {
		t.Fatalf("padrões: %+v", f)
	}
	f.Name, f.RasEntry, f.Host = " Filial ", "VPN Filial", "10.0.0.2"
	c, errs := f.Command()
	if len(errs) != 0 || c.Type != ipc.TypeAddVPN {
		t.Fatalf("%v %v", c, errs)
	}
	raw := c.Payload.(ipc.AddVPNRequest).Config
	if raw.Name != "Filial" {
		t.Fatalf("nome: %q", raw.Name)
	}
	if probs := config.ValidateVPN(raw.Normalize()); len(probs) != 0 {
		t.Fatalf("padrões inválidos para o serviço: %v", probs)
	}
}

// Trocar tcp → ping/link não leva a porta (o serviço recusaria "só vale para
// tcp"); link também não leva host.
func TestFormDropsFieldsOfOtherKinds(t *testing.T) {
	f := FormFrom(sampleVPN())
	f.Kind = "ping"
	c, _ := f.Command()
	if raw := c.Payload.(ipc.UpdateVPNRequest).Config; raw.Check.Port != 0 || raw.Check.Host != "10.0.0.1" {
		t.Fatalf("ping: %+v", raw.Check)
	}
	f.Kind = "link"
	if f.HostEnabled() {
		t.Fatal("link não tem host")
	}
	c, _ = f.Command()
	if raw := c.Payload.(ipc.UpdateVPNRequest).Config; raw.Check.Port != 0 || raw.Check.Host != "" {
		t.Fatalf("link: %+v", raw.Check)
	}
}

func TestFormLocalErrors(t *testing.T) {
	f := NewForm()
	f.Name, f.RasEntry, f.Host = "  ", "E", "h"
	f.Port, f.Kind = "abc", "tcp"
	f.Interval, f.Timeout = "", "1.5"
	f.Grace = "-0"
	c, errs := f.Command()
	want := FieldErrors{
		FieldName:     "obrigatório",
		FieldPort:     "use um número inteiro",
		FieldInterval: "obrigatório",
		FieldTimeout:  "use um número inteiro",
	}
	if c.Type != "" || len(errs) != len(want) {
		t.Fatalf("%v %v", c, errs)
	}
	for k, v := range want {
		if errs[k] != v {
			t.Errorf("%s: %q", k, errs[k])
		}
	}
	f = NewForm()
	f.Kind = "icmp"
	if _, errs := f.Command(); errs[FieldKind] == "" {
		t.Fatal("tipo desconhecido")
	}
}

func TestServiceErrors(t *testing.T) {
	err := &ipc.Error{Code: ipc.CodeInvalidConfig, Message: "config inválida: ...", Fields: []config.FieldError{
		{Field: "check.port", Message: "obrigatória em tcp, entre 1 e 65535"},
		{Field: "vpns[2].intervalSeconds", Message: "deve estar entre 5 e 3600"},
		{Field: "vpns[2].intervalSeconds", Message: "outro"},
		{Field: "logLevel", Message: "use debug, info, warn ou error"},
	}}
	fields, general := ServiceErrors(err)
	if fields[FieldPort] != "obrigatória em tcp, entre 1 e 65535" || fields[FieldInterval] != "deve estar entre 5 e 3600; outro" || len(fields) != 2 {
		t.Fatalf("campos: %v", fields)
	}
	if general != "logLevel: use debug, info, warn ou error" {
		t.Fatalf("geral: %q", general)
	}
	// Só campos do formulário: aviso genérico apontando para eles.
	_, general = ServiceErrors(&ipc.Error{Code: ipc.CodeInvalidConfig, Fields: []config.FieldError{{Field: "name", Message: "já existe"}}})
	if general != "Corrija os campos marcados." {
		t.Fatalf("geral só com campos: %q", general)
	}
	// Recusa sem campos (edição manual pendente) vai inteira para o aviso.
	disk := &ipc.Error{Code: ipc.CodeInvalidConfig, Message: "config.json foi alterado no disco; aguarde a recarga e tente de novo"}
	if fields, general := ServiceErrors(disk); len(fields) != 0 || general != disk.Message {
		t.Fatalf("disco: %v %q", fields, general)
	}
	if fields, general := ServiceErrors(errors.New("x")); len(fields) != 0 || general != "x" {
		t.Fatalf("outro erro: %v %q", fields, general)
	}
	if fields, general := ServiceErrors(nil); fields != nil || general != "" {
		t.Fatal("nil")
	}
}

func TestGlobals(t *testing.T) {
	c := config.Empty()
	c.Notifications, c.LogLevel = false, "debug"
	g := GlobalsFrom(c)
	if g.Notifications || g.LogLevel != "debug" {
		t.Fatalf("%+v", g)
	}
	g.Notifications = true
	if got := wire(t, g.Command()); got != `setGlobal {"notifications":true,"logLevel":"debug"}` {
		t.Fatalf("%s", got)
	}
	if len(LogLevels) != 4 || len(CheckKinds) != 3 {
		t.Fatal("listas")
	}
}

func TestConfigHelpers(t *testing.T) {
	c := config.Empty()
	c.VPNs = []config.VPN{sampleVPN(), {Name: "Filial"}}
	if got := VPNNames(c); len(got) != 2 || got[1] != "Filial" {
		t.Fatalf("%v", got)
	}
	if v, ok := FindVPN(c, "MATRIZ"); !ok || v.RasEntry != "VPN Matriz" {
		t.Fatal("FindVPN sem diferenciar maiúsculas")
	}
	if _, ok := FindVPN(c, "x"); ok {
		t.Fatal("inexistente")
	}
	for _, f := range FormFields {
		if FieldLabels[f] == "" {
			t.Errorf("sem rótulo: %s", f)
		}
	}
}

// A janela só copia valores: ler e escrever o formulário mora aqui.
func TestFormFieldAccess(t *testing.T) {
	f := FormFrom(sampleVPN())
	for _, field := range append(slices.Clone(TextFields), FieldRasEntry) {
		g := f.WithText(field, "novo-"+field)
		if g.Text(field) != "novo-"+field {
			t.Errorf("%s: %q", field, g.Text(field))
		}
		if f.Text(field) == "novo-"+field {
			t.Errorf("%s: WithText alterou o original", field)
		}
	}
	if f.Text(FieldEnabled) != "" || f.WithText(FieldKind, "x") != f {
		t.Fatal("campos que não são texto")
	}
	if f.Text(FieldPort) != "443" || f.Text(FieldRasEntry) != "VPN Matriz" {
		t.Fatalf("valores: %+v", f)
	}
	if f.KindIndex() != 1 || f.WithKindIndex(2).Kind != "link" || f.WithKindIndex(-1).Kind != "tcp" || f.WithKindIndex(9).Kind != "tcp" {
		t.Fatal("tipo por posição")
	}
	if !f.NameReadOnly() || NewForm().NameReadOnly() {
		t.Fatal("nome somente leitura só em VPN existente")
	}
	g := Globals{LogLevel: "warn"}
	if g.LevelIndex() != 2 || g.WithLevelIndex(0).LogLevel != "debug" || g.WithLevelIndex(7).LogLevel != "warn" {
		t.Fatal("nível por posição")
	}
}

func TestSelectIndex(t *testing.T) {
	names := []string{"Matriz", "Filial"}
	cases := map[string]int{"Filial": 1, " filial ": 1, "Removida": 0, "": 0}
	for name, want := range cases {
		if got := SelectIndex(names, name); got != want {
			t.Errorf("%q: %d", name, got)
		}
	}
	if SelectIndex(nil, "x") != -1 {
		t.Fatal("lista vazia")
	}
	if got := EntryNames([]ipc.RasEntry{{Name: "A", Monitored: true}, {Name: "B"}}); len(got) != 2 || got[1] != "B" {
		t.Fatalf("%v", got)
	}
}
```

- [ ] **Step 2: Rodar o teste e ver falhar**

Run: `go test ./internal/features/tray/viewmodel/`
Expected: FAIL — build failed: `undefined: FormFrom`, `undefined: ServiceErrors`, `undefined: Globals`

- [ ] **Step 3: Implementar**

`internal/features/tray/viewmodel/settings.go`:

```go
package viewmodel

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

// Campos do formulário de uma VPN, com os mesmos caminhos dos FieldError
// do serviço (sem o prefixo "vpns[N].").
const (
	FieldName           = "name"
	FieldRasEntry       = "rasEntry"
	FieldKind           = "check.kind"
	FieldHost           = "check.host"
	FieldPort           = "check.port"
	FieldTimeout        = "check.timeoutSeconds"
	FieldInterval       = "intervalSeconds"
	FieldFailures       = "failuresBeforeReconnect"
	FieldGrace          = "graceAfterConnectSeconds"
	FieldConnectTimeout = "connectTimeoutSeconds"
	FieldMaxBackoff     = "maxBackoffSeconds"
	FieldEnabled        = "enabled"
)

// FormFields é a ordem dos campos na janela.
var FormFields = []string{FieldName, FieldRasEntry, FieldKind, FieldHost, FieldPort, FieldTimeout,
	FieldInterval, FieldFailures, FieldGrace, FieldConnectTimeout, FieldMaxBackoff, FieldEnabled}

// FieldLabels são os rótulos da janela.
var FieldLabels = map[string]string{
	FieldName:           "Nome",
	FieldRasEntry:       "Entrada RAS",
	FieldKind:           "Verificação",
	FieldHost:           "Host (IPv4 ou nome)",
	FieldPort:           "Porta TCP",
	FieldTimeout:        "Prazo da verificação (s)",
	FieldInterval:       "Intervalo entre verificações (s)",
	FieldFailures:       "Falhas antes de reconectar",
	FieldGrace:          "Carência após conectar (s)",
	FieldConnectTimeout: "Prazo da discagem (s)",
	FieldMaxBackoff:     "Espera máxima entre tentativas (s)",
	FieldEnabled:        "Ativada",
}

// CheckKinds e LogLevels são as opções das listas da janela.
var (
	CheckKinds = []string{string(config.CheckPing), string(config.CheckTCP), string(config.CheckLink)}
	LogLevels  = []string{"debug", "info", "warn", "error"}
)

// FieldErrors associa um campo do formulário à mensagem a mostrar junto dele.
type FieldErrors map[string]string

// Form é o formulário de uma VPN, com os valores como texto (como nas caixas).
type Form struct {
	IsNew          bool // nome editável; Salvar envia addVpn
	Name           string
	RasEntry       string
	Kind           string
	Host           string
	Port           string
	Timeout        string
	Interval       string
	Failures       string
	Grace          string
	ConnectTimeout string
	MaxBackoff     string
	Enabled        bool
}

func itoa(n int) string { return strconv.Itoa(n) }

// FormFrom preenche o formulário com uma VPN existente (nome somente leitura, §5.2).
func FormFrom(v config.VPN) Form {
	f := Form{Name: v.Name, RasEntry: v.RasEntry, Kind: string(v.Check.Kind), Host: v.Check.Host,
		Timeout: itoa(v.Check.TimeoutSeconds), Interval: itoa(v.IntervalSeconds), Failures: itoa(v.FailuresBeforeReconnect),
		Grace: itoa(v.GraceAfterConnectSeconds), ConnectTimeout: itoa(v.ConnectTimeoutSeconds),
		MaxBackoff: itoa(v.MaxBackoffSeconds), Enabled: v.Enabled}
	if v.Check.Port != 0 {
		f.Port = itoa(v.Check.Port)
	}
	return f
}

// NewForm é o formulário de uma VPN nova, com os padrões da §5.2.
func NewForm() Form {
	f := FormFrom(config.RawVPN{}.Normalize())
	f.IsNew = true
	return f
}

// HostEnabled e PortEnabled dizem se o campo vale para o tipo escolhido.
func (f Form) HostEnabled() bool { return f.Kind != string(config.CheckLink) }
func (f Form) PortEnabled() bool { return f.Kind == string(config.CheckTCP) }

// Command converte o formulário no pedido completo (addVpn ou updateVpn).
// Só confere o que impede montar o pedido (campos vazios, não numéricos,
// tipo desconhecido); limites e regras ficam com o serviço, que responde
// com FieldError por campo.
func (f Form) Command() (Command, FieldErrors) {
	errs := FieldErrors{}
	num := func(field, s string) *int {
		s = strings.TrimSpace(s)
		if s == "" {
			errs[field] = "obrigatório"
			return nil
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			errs[field] = "use um número inteiro"
			return nil
		}
		return &n
	}
	name := f.Name
	if f.IsNew {
		name = strings.TrimSpace(name)
	}
	if name == "" {
		errs[FieldName] = "obrigatório"
	}
	if !slices.Contains(CheckKinds, f.Kind) {
		errs[FieldKind] = "escolha ping, tcp ou link"
	}
	check := &config.RawCheck{Kind: config.CheckKind(f.Kind), TimeoutSeconds: num(FieldTimeout, f.Timeout)}
	if f.HostEnabled() {
		check.Host = strings.TrimSpace(f.Host)
	}
	if f.PortEnabled() {
		if p := num(FieldPort, f.Port); p != nil {
			check.Port = *p
		}
	}
	enabled := f.Enabled
	raw := config.RawVPN{
		Name: name, RasEntry: f.RasEntry, Enabled: &enabled, Check: check,
		IntervalSeconds:          num(FieldInterval, f.Interval),
		FailuresBeforeReconnect:  num(FieldFailures, f.Failures),
		GraceAfterConnectSeconds: num(FieldGrace, f.Grace),
		ConnectTimeoutSeconds:    num(FieldConnectTimeout, f.ConnectTimeout),
		MaxBackoffSeconds:        num(FieldMaxBackoff, f.MaxBackoff),
	}
	if len(errs) > 0 {
		return Command{}, errs
	}
	if f.IsNew {
		return Command{ipc.TypeAddVPN, ipc.AddVPNRequest{Config: raw}}, nil
	}
	return Command{ipc.TypeUpdateVPN, ipc.UpdateVPNRequest{Name: name, Config: raw}}, nil
}

var vpnPrefix = regexp.MustCompile(`^vpns\[\d+\]\.`)

// ServiceErrors separa a recusa do serviço em mensagens por campo do
// formulário e um aviso geral (campos de fora do formulário, ou a recusa
// inteira quando não há campos — ex.: "config.json foi alterado no disco").
func ServiceErrors(err error) (FieldErrors, string) {
	if err == nil {
		return nil, ""
	}
	var e *ipc.Error
	if !errors.As(err, &e) || len(e.Fields) == 0 {
		return FieldErrors{}, ErrorText(err)
	}
	fields := FieldErrors{}
	var general []string
	for _, fe := range e.Fields {
		key := vpnPrefix.ReplaceAllString(fe.Field, "")
		if !slices.Contains(FormFields, key) {
			general = append(general, fe.Field+": "+fe.Message)
			continue
		}
		if old := fields[key]; old != "" {
			fields[key] = old + "; " + fe.Message
		} else {
			fields[key] = fe.Message
		}
	}
	if len(general) == 0 {
		return fields, "Corrija os campos marcados."
	}
	return fields, strings.Join(general, "\n")
}

// Globals são as opções gerais da janela (§7: avisos e nível de log).
type Globals struct {
	Notifications bool
	LogLevel      string
}

// GlobalsFrom lê as opções da config.
func GlobalsFrom(c config.Config) Globals {
	return Globals{Notifications: c.Notifications, LogLevel: c.LogLevel}
}

// Command monta o setGlobal com as duas opções.
func (g Globals) Command() Command {
	n, l := g.Notifications, g.LogLevel
	return Command{ipc.TypeSetGlobal, ipc.SetGlobalRequest{Notifications: &n, LogLevel: &l}}
}

// VPNNames são os nomes da lista da janela, na ordem da config.
func VPNNames(c config.Config) []string {
	out := make([]string, len(c.VPNs))
	for i, v := range c.VPNs {
		out[i] = v.Name
	}
	return out
}

// FindVPN acha a VPN pelo nome, sem diferenciar maiúsculas.
func FindVPN(c config.Config, name string) (config.VPN, bool) {
	for _, v := range c.VPNs {
		if config.NameKey(v.Name) == config.NameKey(name) {
			return v, true
		}
	}
	return config.VPN{}, false
}

// TextFields são os campos editados em caixa de texto (o resto: entrada RAS
// em caixa editável com sugestões, tipo em lista, ativada em marcação).
var TextFields = []string{FieldName, FieldHost, FieldPort, FieldTimeout, FieldInterval, FieldFailures,
	FieldGrace, FieldConnectTimeout, FieldMaxBackoff}

// textField aponta o campo de texto do formulário (nil se não for de texto).
func (f *Form) textField(field string) *string {
	switch field {
	case FieldName:
		return &f.Name
	case FieldRasEntry:
		return &f.RasEntry
	case FieldHost:
		return &f.Host
	case FieldPort:
		return &f.Port
	case FieldTimeout:
		return &f.Timeout
	case FieldInterval:
		return &f.Interval
	case FieldFailures:
		return &f.Failures
	case FieldGrace:
		return &f.Grace
	case FieldConnectTimeout:
		return &f.ConnectTimeout
	case FieldMaxBackoff:
		return &f.MaxBackoff
	}
	return nil
}

// Text é o valor de um campo de texto ("" para os demais).
func (f Form) Text(field string) string {
	if p := f.textField(field); p != nil {
		return *p
	}
	return ""
}

// WithText devolve o formulário com o campo de texto trocado (os demais
// campos são ignorados).
func (f Form) WithText(field, value string) Form {
	if p := f.textField(field); p != nil {
		*p = value
	}
	return f
}

// KindIndex é a posição do tipo de verificação em CheckKinds (-1 se nenhum).
func (f Form) KindIndex() int { return slices.Index(CheckKinds, f.Kind) }

// WithKindIndex escolhe o tipo pela posição na lista (fora dela, mantém).
func (f Form) WithKindIndex(i int) Form {
	if i >= 0 && i < len(CheckKinds) {
		f.Kind = CheckKinds[i]
	}
	return f
}

// NameReadOnly: só uma VPN nova tem nome editável (§5.2: o nome é a identidade).
func (f Form) NameReadOnly() bool { return !f.IsNew }

// LevelIndex é a posição do nível de log em LogLevels (-1 se nenhum).
func (g Globals) LevelIndex() int { return slices.Index(LogLevels, g.LogLevel) }

// WithLevelIndex escolhe o nível pela posição na lista (fora dela, mantém).
func (g Globals) WithLevelIndex(i int) Globals {
	if i >= 0 && i < len(LogLevels) {
		g.LogLevel = LogLevels[i]
	}
	return g
}

// SelectIndex é a linha a selecionar na lista de VPNs depois de recarregar:
// a VPN dada (sem diferenciar maiúsculas, sem espaços nas pontas), senão a
// primeira; -1 com a lista vazia (abre o formulário de VPN nova).
func SelectIndex(names []string, name string) int {
	if len(names) == 0 {
		return -1
	}
	key := config.NameKey(strings.TrimSpace(name))
	if i := slices.IndexFunc(names, func(n string) bool { return config.NameKey(n) == key }); i >= 0 {
		return i
	}
	return 0
}

// EntryNames são as sugestões da caixa "Entrada RAS" (todas as entradas do
// catálogo de todos os usuários, monitoradas ou não).
func EntryNames(entries []ipc.RasEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}
```

A cobertura do pacote deve ficar ≥ 80 % (na validação: 99,4 %); o alvo `make cover-tray`, que a confere, entra na Task 13.

- [ ] **Step 4: Rodar os testes e ver passar**

Run: `go test -cover ./internal/features/tray/viewmodel/`
Expected: PASS

- [ ] **Step 5: Formatação e vet (linux e windows)**

Run: `gofmt -l . && go vet ./... && GOOS=windows go vet ./...`
Expected: nenhuma saída de erro (gofmt sem arquivos listados)

- [ ] **Step 6: Commit**

```bash
git add internal/features/tray/viewmodel
git commit -m "feat(tray): formulário de Configurações no view-model (pedido completo e erros por campo)"
```

---

### Task 11: View walk: ícone, menu, balões, janela de Configurações e janela de log

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/features/tray/view/doc.go`
- Create: `internal/features/tray/view/tray_windows.go`
- Create: `internal/features/tray/view/menu_windows.go`
- Create: `internal/features/tray/view/settings_windows.go`
- Create: `internal/features/tray/view/logwin_windows.go`
- Test: `internal/features/tray/view/guard_test.go`

**Interfaces:**
- Consumes: `client.Event`, `client.EvSnapshot`, `client.Stats` (Task 4); `viewmodel.New`, `VM.Apply/Model/TakeBalloon/SetRasEntries/Names/About`,
  `Model`, `VPNItem`, `Balloon*`, `Icon*` (Task 8); `CheckNow`, `Reconnect`, `Pause`, `PauseChoices`, `Resume`, `SetEnabled`, `Remove`,
  `RemoveConfirm`, `AddFromEntry`, `ListRasEntries`, `GetConfig`, `LogTail`, `ErrorText`, `Command`, `VPNItem.ToggleCommand` (Task 9); `Form`,
  `FormFrom`, `NewForm`, `FormFields`, `TextFields`, `FieldLabels`, `Field*`, `CheckKinds`, `LogLevels`, `ServiceErrors`, `Globals`,
  `GlobalsFrom`, `VPNNames`, `FindVPN`, `Form.Text/WithText/KindIndex/WithKindIndex/NameReadOnly`, `Globals.LevelIndex/WithLevelIndex`,
  `SelectIndex`, `EntryNames` (Task 10);
  `assets.Image`, `assets.Conectada/Conectando/Desconectada/Inativa` (Task 2).
- Produces: `view.Caller` (`Call(ctx, typ string, payload, out any) error`; `*client.Client` satisfaz), `view.Options{ Events <-chan client.Event;
  Caller Caller; Stats func() client.Stats; AppVersion string; Log *slog.Logger }`, `view.Run(o Options) (exitCode int, err error)` —
  chamado da goroutine principal.

- [ ] **Step 1: Acrescentar o walk ao módulo**

```bash
go get github.com/tailscale/walk@v0.0.0-20260702185836-28b80ea70d3b
```

O `go.mod` passa a ter `github.com/tailscale/walk v0.0.0-20260702185836-28b80ea70d3b` no bloco direto (depois do `go mod tidy` do Step 5) e,
como indiretas, `github.com/tailscale/win v0.0.0-20260619195133-2d76c33a64c1`, `github.com/dblohm7/wingoes v0.0.0-20231019175336-f6e33aa7cc34`,
`golang.org/x/exp v0.0.0-20230425010034-47ecfdc1ba53` e `gopkg.in/Knetic/govaluate.v3 v3.0.0`. O walk exige `x/sys` ≥ v0.37; o módulo fica
em v0.47.0.

Notas da API do fork da Tailscale (diferente do `lxn/walk`): `walk.InitApp()` antes de tudo, `app.Run()` como laço, `app.Synchronize(fn)`
para voltar à thread da interface, `walk.NewNotifyIcon()` sem janela dona, `ni.ShowingContextMenu()` (`ProceedEvent`, handler `func() bool`)
dispara **antes** de o menu abrir, e `MainWindow` encerra o aplicativo ao fechar a menos que se chame `SetExitOnClose(false)`.

- [ ] **Step 2: Escrever o teste que falha**

`internal/features/tray/view/guard_test.go`:

```go
package view

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No walk, fechar uma MainWindow encerra o aplicativo, a menos que se chame
// SetExitOnClose(false). Toda janela da bandeja (Configurações, log) precisa
// disso, senão fechar a janela some com o ícone. A view só compila no Windows;
// esta conferência lê o código-fonte e roda no Linux.
func TestEveryWindowKeepsTrayAlive(t *testing.T) {
	files, err := filepath.Glob("*_windows.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("fontes da view: %v %v", files, err)
	}
	windows := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		created := strings.Count(src, "d.MainWindow{")
		kept := strings.Count(src, ".SetExitOnClose(false)")
		if created != kept {
			t.Errorf("%s: %d janela(s) e %d SetExitOnClose(false)", f, created, kept)
		}
		windows += created
	}
	if windows < 2 {
		t.Fatalf("esperava as janelas de Configurações e de log, achei %d", windows)
	}
}
```

- [ ] **Step 3: Rodar o teste e ver falhar**

Run: `go test ./internal/features/tray/view/`
Expected: FAIL — `fontes da view: [] <nil>` (ainda não há `*_windows.go`; com só o teste o pacote compila no Linux)

- [ ] **Step 4: Implementar**

`internal/features/tray/view/doc.go`:

```go
// Package view desenha a bandeja com github.com/tailscale/walk: ícone,
// menu, balões, janela de Configurações e janela de log. Só desenha o
// modelo do viewmodel e repassa cliques ao cliente; não tem lógica de
// apresentação. Existe só no Windows (os arquivos são _windows.go); este
// arquivo mantém o pacote visível para `go vet ./...` no Linux.
package view
```

`internal/features/tray/view/tray_windows.go`:

```go
//go:build windows

package view

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tailscale/walk"

	"github.com/guibsu/vpn-tray-monitor/assets"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/client"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/viewmodel"
)

// Caller envia pedidos ao serviço (client.Client serve).
type Caller interface {
	Call(ctx context.Context, typ string, payload any, out any) error
}

// Options liga a view ao cliente.
type Options struct {
	Events <-chan client.Event
	Caller Caller
	// Stats dá as contagens da decodificação tolerante para "Sobre"
	// ((*client.Client).Stats); nil = nenhuma.
	Stats      func() client.Stats
	AppVersion string
	Log        *slog.Logger
}

// callTimeout limita cada pedido do menu e das janelas.
const callTimeout = 15 * time.Second

// Tray é o estado da interface; só é tocado na thread da interface (os
// eventos chegam por app.Synchronize).
type Tray struct {
	o        Options
	app      *walk.Application
	ni       *walk.NotifyIcon
	vm       *viewmodel.VM
	icons    map[viewmodel.Icon]*walk.Icon
	dpi      int // DPI em que os ícones foram gerados
	last     viewmodel.Model
	settings *settingsWin
	logs     *logWin
}

var iconNames = map[viewmodel.Icon]string{
	viewmodel.IconGray: assets.Inativa, viewmodel.IconGreen: assets.Conectada,
	viewmodel.IconAmber: assets.Conectando, viewmodel.IconRed: assets.Desconectada,
}

// Run cria o ícone e roda o laço de mensagens até "Sair da bandeja".
// Precisa ser chamado da goroutine principal (walk trava a thread no init).
func Run(o Options) (int, error) {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Stats == nil {
		o.Stats = func() client.Stats { return client.Stats{} }
	}
	app, err := walk.InitApp()
	if err != nil {
		return 1, fmt.Errorf("iniciando a interface: %w", err)
	}
	ni, err := walk.NewNotifyIcon()
	if err != nil {
		return 1, fmt.Errorf("criando o ícone da bandeja: %w", err)
	}
	defer ni.Dispose()
	t := &Tray{o: o, app: app, ni: ni, vm: viewmodel.New(o.AppVersion)}
	if err := t.loadIcons(); err != nil {
		return 1, err
	}
	// O menu é montado na hora de abrir: um menu aberto não se redesenha
	// sozinho, e assim os tempos relativos saem sempre atuais.
	ni.ShowingContextMenu().Attach(func() bool {
		t.buildMenu(t.vm.Model(time.Now()))
		return true
	})
	m := t.vm.Model(time.Now())
	t.buildMenu(m)
	t.render(m, true)
	if err := ni.SetVisible(true); err != nil {
		return 1, fmt.Errorf("mostrando o ícone da bandeja: %w", err)
	}

	stop := make(chan struct{})
	go t.pump(stop)
	go t.tick(stop)
	code := app.Run()
	close(stop)
	return code, nil
}

// trayDPI é o DPI atual da área de notificação (96 se desconhecido).
func (t *Tray) trayDPI() int {
	if dpi := t.ni.DPI(); dpi > 0 {
		return dpi
	}
	return 96
}

// loadIcons gera os quatro ícones a partir do PNG do tamanho certo para o
// DPI atual (16 px a 96 DPI), em vez de deixar o Windows esticar um menor.
func (t *Tray) loadIcons() error {
	dpi := t.trayDPI()
	icons := map[viewmodel.Icon]*walk.Icon{}
	for k, name := range iconNames {
		img, err := assets.Image(name, 16*dpi/96)
		if err != nil {
			return err
		}
		ic, err := walk.NewIconFromImageForDPI(img, dpi)
		if err != nil {
			return fmt.Errorf("ícone %s: %w", name, err)
		}
		icons[k] = ic
	}
	old := t.icons
	t.icons, t.dpi = icons, dpi
	for _, ic := range old {
		ic.Dispose()
	}
	return nil
}

// pump leva os eventos do cliente para a thread da interface, em ordem.
func (t *Tray) pump(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case ev, ok := <-t.o.Events:
			if !ok {
				return
			}
			t.app.Synchronize(func() { t.apply(ev) })
		}
	}
}

// tick atualiza tooltip e ícone a cada segundo (tempos relativos, §7).
func (t *Tray) tick(stop <-chan struct{}) {
	tk := time.NewTicker(time.Second)
	defer tk.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tk.C:
			t.app.Synchronize(func() { t.render(t.vm.Model(time.Now()), false) })
		}
	}
}

func (t *Tray) apply(ev client.Event) {
	t.vm.Apply(ev)
	if b, ok := t.vm.TakeBalloon(); ok {
		t.showBalloon(b)
	}
	t.render(t.vm.Model(time.Now()), false)
	if ev.Kind == client.EvSnapshot {
		t.refreshRasEntries()
	}
}

// render aplica ícone e tooltip quando mudam. Também é onde a troca de DPI
// é percebida (a cada tique de 1 s): o NotifyIcon do walk trata o
// WM_DPICHANGED só redesenhando o mesmo ícone, sem gancho público; então os
// ícones são gerados de novo no tamanho do DPI novo.
func (t *Tray) render(m viewmodel.Model, force bool) {
	if dpi := t.trayDPI(); dpi != t.dpi {
		if err := t.loadIcons(); err != nil {
			t.o.Log.Warn("gerando ícones para o DPI novo", "dpi", dpi, "erro", err)
		} else {
			force = true
		}
	}
	if force || m.Icon != t.last.Icon {
		if err := t.ni.SetIcon(t.icons[m.Icon]); err != nil {
			t.o.Log.Warn("trocando o ícone", "erro", err)
		}
	}
	if force || m.ToolTip != t.last.ToolTip {
		if err := t.ni.SetToolTip(m.ToolTip); err != nil {
			t.o.Log.Warn("trocando o tooltip", "erro", err)
		}
	}
	t.last = m
}

func (t *Tray) showBalloon(b viewmodel.Balloon) {
	var err error
	switch b.Kind {
	case viewmodel.BalloonWarning:
		err = t.ni.ShowWarning(b.Title, b.Text)
	case viewmodel.BalloonError:
		err = t.ni.ShowError(b.Title, b.Text)
	default:
		err = t.ni.ShowInfo(b.Title, b.Text)
	}
	if err != nil {
		t.o.Log.Warn("mostrando balão", "erro", err)
	}
}

// call faz o pedido fora da thread da interface e devolve o resultado nela.
func (t *Tray) call(c viewmodel.Command, out any, done func(error)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		err := t.o.Caller.Call(ctx, c.Type, c.Payload, out)
		t.app.Synchronize(func() { done(err) })
	}()
}

// do faz um pedido do menu; erro vira caixa de aviso.
func (t *Tray) do(c viewmodel.Command) {
	t.call(c, nil, func(err error) {
		if err != nil {
			walk.MsgBox(nil, "VPN Monitor", viewmodel.ErrorText(err), walk.MsgBoxIconError|walk.MsgBoxOK)
		}
	})
}

func (t *Tray) refreshRasEntries() {
	var r ipc.RasEntries
	t.call(viewmodel.ListRasEntries(), &r, func(err error) {
		if err != nil {
			t.o.Log.Warn("listando entradas RAS", "erro", err)
			return
		}
		t.vm.SetRasEntries(r.Entries)
	})
}
```

`internal/features/tray/view/menu_windows.go`:

```go
//go:build windows

package view

import (
	"time"

	"github.com/tailscale/walk"

	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/viewmodel"
)

// menuBuilder acumula erros de walk ao montar o menu (só vão ao log).
type menuBuilder struct {
	t   *Tray
	err error
}

func (b *menuBuilder) keep(err error) {
	if b.err == nil {
		b.err = err
	}
}

// item, sep e sub ignoram lista nil (submenu que não pôde ser criado).
func (b *menuBuilder) item(l *walk.ActionList, text string, enabled bool, fn func()) {
	if l == nil {
		return
	}
	a := walk.NewAction()
	b.keep(a.SetText(text))
	b.keep(a.SetEnabled(enabled))
	if fn != nil {
		a.Triggered().Attach(fn)
	}
	b.keep(l.Add(a))
}

func (b *menuBuilder) sep(l *walk.ActionList) {
	if l != nil {
		b.keep(l.Add(walk.NewSeparatorAction()))
	}
}

func (b *menuBuilder) sub(l *walk.ActionList, text string, enabled bool) *walk.ActionList {
	if l == nil {
		return nil
	}
	m, err := walk.NewMenu()
	if err != nil {
		b.keep(err)
		return nil
	}
	a, err := l.AddMenu(m)
	b.keep(err)
	if a != nil {
		b.keep(a.SetText(text))
		b.keep(a.SetEnabled(enabled))
	}
	return m.Actions()
}

// disposeMenus libera os submenus (HMENU) do menu antigo.
func disposeMenus(l *walk.ActionList) {
	for i := 0; i < l.Len(); i++ {
		if m := l.At(i).Menu(); m != nil {
			disposeMenus(m.Actions())
			m.Dispose()
		}
	}
}

// buildMenu refaz o menu inteiro a partir do modelo (§7).
func (t *Tray) buildMenu(m viewmodel.Model) {
	root := t.ni.ContextMenu().Actions()
	disposeMenus(root)
	b := &menuBuilder{t: t}
	b.keep(root.Clear())

	b.item(root, m.Header, false, nil)
	if m.Notice != "" {
		b.item(root, m.Notice, false, nil)
	}
	b.sep(root)
	for _, v := range m.VPNs {
		t.vpnMenu(b, b.sub(root, v.Label, true), v)
	}
	if len(m.VPNs) > 0 {
		b.sep(root)
	}
	add := b.sub(root, "Adicionar VPN", m.Connected)
	b.item(add, m.AddNote, false, nil)
	if len(m.AddEntries) > 0 {
		b.sep(add)
	}
	for _, e := range m.AddEntries {
		entry := e.Entry
		b.item(add, e.Label, true, func() { t.do(viewmodel.AddFromEntry(entry, t.vm.Names())) })
	}
	b.item(root, "Configurações…", m.Connected, t.openSettings)
	b.item(root, "Abrir log", m.Connected, t.openLog)
	b.sep(root)
	b.item(root, "Sobre / versão", true, func() {
		walk.MsgBox(nil, "Sobre o VPN Monitor", t.vm.About(t.o.Stats()), walk.MsgBoxIconInformation|walk.MsgBoxOK)
	})
	b.item(root, "Sair da bandeja", true, func() { t.app.Exit(0) })
	if b.err != nil {
		t.o.Log.Warn("montando o menu", "erro", b.err)
	}
}

func (t *Tray) vpnMenu(b *menuBuilder, l *walk.ActionList, v viewmodel.VPNItem) {
	name := v.Name
	for _, d := range v.Details {
		b.item(l, d, false, nil)
	}
	b.sep(l)
	b.item(l, "Verificar agora", v.CanCheck, func() { t.do(viewmodel.CheckNow(name)) })
	b.item(l, "Reconectar agora", v.CanReconnect, func() { t.do(viewmodel.Reconnect(name)) })
	pause := b.sub(l, "Pausar", v.CanPause)
	for _, c := range viewmodel.PauseChoices {
		choice := c.Choice
		b.item(pause, c.Label, true, func() { t.do(viewmodel.Pause(name, choice, time.Now())) })
	}
	b.item(l, "Retomar", v.CanResume, func() { t.do(viewmodel.Resume(name)) })
	b.sep(l)
	b.item(l, v.ToggleLabel, true, func() { t.do(v.ToggleCommand()) })
	b.item(l, "Remover…", true, func() {
		if walk.MsgBox(nil, "Remover VPN", viewmodel.RemoveConfirm(name), walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) == walk.DlgCmdYes {
			t.do(viewmodel.Remove(name))
		}
	})
}
```

`internal/features/tray/view/logwin_windows.go`:

```go
//go:build windows

package view

import (
	"strings"

	"github.com/tailscale/walk"
	d "github.com/tailscale/walk/declarative"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/viewmodel"
)

// logWin mostra o fim do log do serviço, pedido pelo pipe (o usuário não
// tem acesso à pasta de dados, §7).
type logWin struct {
	t    *Tray
	mw   *walk.MainWindow
	text *walk.TextEdit
}

func (t *Tray) openLog() {
	if t.logs != nil {
		_ = t.logs.mw.Activate()
		t.logs.load()
		return
	}
	w := &logWin{t: t}
	err := d.MainWindow{
		AssignTo: &w.mw,
		Title:    "VPN Monitor — log do serviço",
		MinSize:  d.Size{Width: 720, Height: 420},
		Layout:   d.VBox{},
		Children: []d.Widget{
			d.TextEdit{AssignTo: &w.text, ReadOnly: true, VScroll: true, Font: d.Font{Family: "Consolas", PointSize: 9}},
			d.Composite{Layout: d.HBox{MarginsZero: true}, Children: []d.Widget{
				d.HSpacer{},
				d.PushButton{Text: "Atualizar", OnClicked: w.load},
				d.PushButton{Text: "Fechar", OnClicked: func() { _ = w.mw.Close() }},
			}},
		},
	}.Create()
	if err != nil {
		walk.MsgBox(nil, "VPN Monitor", "Não foi possível abrir a janela de log: "+err.Error(), walk.MsgBoxIconError|walk.MsgBoxOK)
		return
	}
	// Sem isto, fechar a janela encerraria a bandeja inteira.
	w.mw.SetExitOnClose(false)
	w.mw.Closing().Attach(func(*bool, walk.CloseReason) { t.logs = nil })
	t.logs = w
	w.mw.Show()
	w.load()
}

func (w *logWin) load() {
	var tail ipc.LogTail
	_ = w.text.SetText("Carregando…")
	w.t.call(viewmodel.LogTail(), &tail, func(err error) {
		if w.mw.IsDisposed() {
			return
		}
		if err != nil {
			_ = w.text.SetText(viewmodel.ErrorText(err))
			return
		}
		// O controle de edição do Windows quer \r\n.
		_ = w.text.SetText(strings.ReplaceAll(strings.ReplaceAll(tail.Text, "\r\n", "\n"), "\n", "\r\n"))
		n := len([]rune(w.text.Text()))
		w.text.SetTextSelection(n, n)
		w.text.ScrollToCaret()
	})
}
```

`internal/features/tray/view/settings_windows.go`:

```go
//go:build windows

package view

import (
	"github.com/tailscale/walk"
	d "github.com/tailscale/walk/declarative"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/viewmodel"
)

// settingsWin é a janela "Configurações…" (§7): lista de VPNs à esquerda,
// formulário da selecionada à direita, opções gerais embaixo. Só copia
// valores entre os controles e o viewmodel.Form/Globals; regras e textos
// moram no view-model.
type settingsWin struct {
	t           *Tray
	mw          *walk.MainWindow
	list        *walk.ListBox
	edits       map[string]*walk.LineEdit // viewmodel.TextFields
	entry       *walk.ComboBox            // entrada RAS (editável, com sugestões)
	kind        *walk.ComboBox
	enabled     *walk.CheckBox
	errs        map[string]*walk.Label
	general     *walk.Label
	save        *walk.PushButton
	saveGlobals *walk.PushButton
	notif       *walk.CheckBox
	level       *walk.ComboBox

	cfg     config.Config
	form    viewmodel.Form
	globals viewmodel.Globals
	names   []string
}

var errorColor = walk.RGB(0xc0, 0x10, 0x10)

func (t *Tray) openSettings() {
	if t.settings != nil {
		_ = t.settings.mw.Activate()
		return
	}
	w, err := newSettingsWin(t)
	if err != nil {
		walk.MsgBox(nil, "VPN Monitor", "Não foi possível abrir as configurações: "+err.Error(), walk.MsgBoxIconError|walk.MsgBoxOK)
		return
	}
	t.settings = w
	w.mw.Show()
	w.reload("")
}

func newSettingsWin(t *Tray) (*settingsWin, error) {
	w := &settingsWin{t: t, edits: map[string]*walk.LineEdit{}, errs: map[string]*walk.Label{}}
	var grid []d.Widget
	editPtrs := map[string]**walk.LineEdit{}
	errPtrs := map[string]**walk.Label{}
	for _, f := range viewmodel.FormFields {
		grid = append(grid, d.Label{Text: viewmodel.FieldLabels[f]})
		switch f {
		case viewmodel.FieldRasEntry:
			grid = append(grid, d.ComboBox{AssignTo: &w.entry, Editable: true})
		case viewmodel.FieldKind:
			grid = append(grid, d.ComboBox{AssignTo: &w.kind, Model: viewmodel.CheckKinds, OnCurrentIndexChanged: w.kindChanged})
		case viewmodel.FieldEnabled:
			grid = append(grid, d.CheckBox{AssignTo: &w.enabled})
		default: // viewmodel.TextFields
			p := new(*walk.LineEdit)
			editPtrs[f] = p
			grid = append(grid, d.LineEdit{AssignTo: p})
		}
		p := new(*walk.Label)
		errPtrs[f] = p
		grid = append(grid, d.Label{AssignTo: p, TextColor: errorColor})
	}
	err := d.MainWindow{
		AssignTo: &w.mw,
		Title:    "VPN Monitor — Configurações",
		MinSize:  d.Size{Width: 760, Height: 520},
		Layout:   d.VBox{},
		Children: []d.Widget{
			d.Composite{Layout: d.HBox{MarginsZero: true}, Children: []d.Widget{
				d.Composite{Layout: d.VBox{MarginsZero: true}, MaxSize: d.Size{Width: 200}, Children: []d.Widget{
					d.ListBox{AssignTo: &w.list, OnCurrentIndexChanged: w.selected},
					d.PushButton{Text: "Nova VPN", OnClicked: w.newVPN},
				}},
				d.Composite{Layout: d.Grid{Columns: 3, MarginsZero: true}, Children: grid},
			}},
			d.Label{AssignTo: &w.general, TextColor: errorColor},
			d.Composite{Layout: d.HBox{MarginsZero: true}, Children: []d.Widget{
				d.HSpacer{},
				// Desabilitados até o getConfig concluir: salvar antes gravaria
				// um formulário vazio por cima da VPN.
				d.PushButton{AssignTo: &w.save, Text: "Salvar VPN", Enabled: false, OnClicked: w.saveVPN},
			}},
			d.GroupBox{Title: "Opções gerais", Layout: d.Grid{Columns: 3}, Children: []d.Widget{
				d.CheckBox{AssignTo: &w.notif, Text: "Mostrar avisos (balões)", ColumnSpan: 3},
				d.Label{Text: "Nível de log"},
				d.ComboBox{AssignTo: &w.level, Model: viewmodel.LogLevels},
				d.PushButton{AssignTo: &w.saveGlobals, Text: "Salvar opções gerais", Enabled: false, OnClicked: w.saveGlobalOptions},
			}},
		},
	}.Create()
	if err != nil {
		return nil, err
	}
	for f, p := range editPtrs {
		w.edits[f] = *p
	}
	for f, p := range errPtrs {
		w.errs[f] = *p
	}
	// Sem isto, fechar a janela encerraria a bandeja inteira.
	w.mw.SetExitOnClose(false)
	w.mw.Closing().Attach(func(*bool, walk.CloseReason) { t.settings = nil })
	return w, nil
}

// setSaving liga ou desliga os dois botões de salvar.
func (w *settingsWin) setSaving(enabled bool) {
	w.save.SetEnabled(enabled)
	w.saveGlobals.SetEnabled(enabled)
}

// reload pede a config ao serviço e seleciona a VPN dada ("" = a primeira).
// Os botões de salvar só voltam depois da resposta.
func (w *settingsWin) reload(selectName string) {
	w.setSaving(false)
	var cfg config.Config
	w.t.call(viewmodel.GetConfig(), &cfg, func(err error) {
		if w.mw.IsDisposed() {
			return
		}
		if err != nil {
			_ = w.general.SetText(viewmodel.ErrorText(err))
			return
		}
		w.cfg = cfg
		w.names = viewmodel.VPNNames(cfg)
		w.globals = viewmodel.GlobalsFrom(cfg)
		w.notif.SetChecked(w.globals.Notifications)
		_ = w.level.SetCurrentIndex(w.globals.LevelIndex())
		_ = w.list.SetModel(w.names)
		if i := viewmodel.SelectIndex(w.names, selectName); i < 0 {
			w.newVPN()
		} else {
			_ = w.list.SetCurrentIndex(i)
			w.selected()
		}
		w.setSaving(true)
	})
	var r ipc.RasEntries
	w.t.call(viewmodel.ListRasEntries(), &r, func(err error) {
		if err != nil || w.mw.IsDisposed() {
			return
		}
		text := w.entry.Text()
		_ = w.entry.SetModel(viewmodel.EntryNames(r.Entries))
		_ = w.entry.SetText(text)
	})
}

func (w *settingsWin) selected() {
	i := w.list.CurrentIndex()
	if i < 0 || i >= len(w.names) {
		return
	}
	if v, ok := viewmodel.FindVPN(w.cfg, w.names[i]); ok {
		w.show(viewmodel.FormFrom(v))
	}
}

func (w *settingsWin) newVPN() {
	_ = w.list.SetCurrentIndex(-1)
	w.show(viewmodel.NewForm())
}

// show copia o formulário para os controles e limpa os erros.
func (w *settingsWin) show(f viewmodel.Form) {
	w.form = f
	for _, field := range viewmodel.TextFields {
		_ = w.edits[field].SetText(f.Text(field))
	}
	_ = w.entry.SetText(f.Text(viewmodel.FieldRasEntry))
	_ = w.kind.SetCurrentIndex(f.KindIndex())
	w.enabled.SetChecked(f.Enabled)
	_ = w.edits[viewmodel.FieldName].SetReadOnly(f.NameReadOnly())
	w.kindChanged()
	w.showErrors(nil, "")
}

// read copia os controles de volta para o formulário.
func (w *settingsWin) read() viewmodel.Form {
	f := w.form
	for _, field := range viewmodel.TextFields {
		f = f.WithText(field, w.edits[field].Text())
	}
	f = f.WithText(viewmodel.FieldRasEntry, w.entry.Text())
	f = f.WithKindIndex(w.kind.CurrentIndex())
	f.Enabled = w.enabled.Checked()
	return f
}

// kindChanged habilita host e porta conforme o tipo de verificação.
func (w *settingsWin) kindChanged() {
	if len(w.edits) == 0 { // evento disparado durante a criação da janela
		return
	}
	f := w.read()
	w.edits[viewmodel.FieldHost].SetEnabled(f.HostEnabled())
	w.edits[viewmodel.FieldPort].SetEnabled(f.PortEnabled())
}

func (w *settingsWin) showErrors(fields viewmodel.FieldErrors, general string) {
	for f, l := range w.errs {
		_ = l.SetText(fields[f])
	}
	_ = w.general.SetText(general)
}

func (w *settingsWin) saveVPN() {
	f := w.read()
	c, local := f.Command()
	if len(local) > 0 {
		w.showErrors(local, "Corrija os campos marcados.")
		return
	}
	w.setSaving(false)
	w.t.call(c, nil, func(err error) {
		if w.mw.IsDisposed() {
			return
		}
		if err != nil {
			w.setSaving(true)
			w.showErrors(viewmodel.ServiceErrors(err))
			return
		}
		w.reload(f.Name)
	})
}

func (w *settingsWin) saveGlobalOptions() {
	g := w.globals.WithLevelIndex(w.level.CurrentIndex())
	g.Notifications = w.notif.Checked()
	w.setSaving(false)
	w.t.call(g.Command(), nil, func(err error) {
		if w.mw.IsDisposed() {
			return
		}
		if err != nil {
			w.setSaving(true)
			_, msg := viewmodel.ServiceErrors(err)
			_ = w.general.SetText(msg)
			return
		}
		_ = w.general.SetText("")
		w.reload(w.form.Name)
	})
}
```

Por que o menu é refeito no `ShowingContextMenu`: um menu de contexto aberto é modal (`TrackPopupMenuEx`) e o Windows não redesenha
itens alterados com ele aberto; mexer nele durante o laço modal (os `Synchronize` rodam lá dentro) seria pior. Assim os detalhes ("há 3 h",
"próxima em 40 s") saem atuais a cada abertura, e o tique de 1 s mantém ícone e tooltip. Os submenus antigos são liberados
(`disposeMenus`) antes de cada recriação.

Troca de DPI (monitor diferente, escala alterada): o `NotifyIcon` do walk trata o `WM_DPICHANGED` só redesenhando o mesmo ícone e não
expõe evento; por isso `render` confere `ni.DPI()` a cada tique de 1 s e, se mudou, gera os quatro ícones de novo a partir do PNG do
tamanho certo (`assets.Image(nome, 16*dpi/96)`), descartando os antigos.

Na janela de Configurações os dois botões de salvar nascem desabilitados e só voltam quando o `getConfig` responde (salvar antes gravaria
um formulário vazio por cima da VPN); a janela só copia valores entre controles e `viewmodel.Form`/`Globals`.

Conferência manual de API, se o `go vet` reclamar: `walk.MsgBox` está marcado como obsoleto (o lint do Marco C pode pedir `TaskDialog`),
mas compila e é o que o plano usa.

- [ ] **Step 5: Rodar os testes e ver passar**

Run: `go mod tidy && go test ./internal/features/tray/view/ && GOOS=windows go vet ./internal/features/tray/view/`
Expected: PASS

- [ ] **Step 6: Formatação e vet (linux e windows)**

Run: `gofmt -l . && go vet ./... && GOOS=windows go vet ./...`
Expected: nenhuma saída de erro (gofmt sem arquivos listados); e `GOOS=windows go list -deps ./internal/features/tray/viewmodel | grep -c walk` imprime `0` (o view-model não conhece walk)

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/features/tray/view
git commit -m "feat(tray): view walk (ícone, menu, balões, Configurações, log)"
```

---

### Task 12: Montagem do `vpnmon-tray` (mutex, cliente, view)

**Files:**
- Create: `cmd/vpnmon-tray/main.go`
- Create: `cmd/vpnmon-tray/main_windows.go`
- Create: `cmd/vpnmon-tray/main_other.go`
- Test: `cmd/vpnmon-tray/main_test.go`

**Interfaces:**
- Consumes: `instance.Acquire`, `instance.TrayMutex`, `instance.ErrAlreadyRunning` (Task 3); `client.New`, `client.Options`, `(*Client).Run`,
  `(*Client).Events`, `(*Client).Stats` (Tasks 4–5); `ipc.Dial` (Marco A; confere o PID e devolve `ipc.ErrNotService`); `view.Run`, `view.Options` (Task 11).
- Produces: `cmd/vpnmon-tray` com as variáveis `version`, `commit`, `date` (preenchidas por `-ldflags -X main.…`, como no `vpnmon-svc`) e
  `appVersion() string` (versão mostrada em "Sobre" e enviada no hello; fora de uma tag o `git describe` já começa pelo commit, que então
  não se repete entre parênteses).

- [ ] **Step 1: Escrever o teste que falha**

`cmd/vpnmon-tray/main_test.go`:

```go
package main

import "testing"

func TestAppVersion(t *testing.T) {
	defer func(v, c, d string) { version, commit, date = v, c, d }(version, commit, date)
	cases := []struct{ v, c, d, want string }{
		{"dev", "", "", "dev"},
		{"v2.1.0", "abc1234", "", "v2.1.0 (abc1234)"},
		{"v2.1.0", "abc1234", "2026-10-08T12:00:00Z", "v2.1.0 (abc1234, 2026-10-08T12:00:00Z)"},
		// Fora de tag o git describe já é o commit: não repete.
		{"abc1234-dirty", "abc1234", "2026-10-08T12:00:00Z", "abc1234-dirty (2026-10-08T12:00:00Z)"},
		{"abc1234", "abc1234", "", "abc1234"},
		{"v2.1.0-3-gabc1234", "abc1234", "", "v2.1.0-3-gabc1234 (abc1234)"},
	}
	for _, c := range cases {
		version, commit, date = c.v, c.c, c.d
		if got := appVersion(); got != c.want {
			t.Errorf("%q, esperava %q", got, c.want)
		}
	}
}
```

- [ ] **Step 2: Rodar o teste e ver falhar**

Run: `go test ./cmd/vpnmon-tray/`
Expected: FAIL — build failed: `undefined: version`, `undefined: appVersion`

- [ ] **Step 3: Implementar**

`cmd/vpnmon-tray/main.go`:

```go
// Comando vpnmon-tray: a bandeja do VPN Monitor, uma por sessão de usuário
// (§7). Iniciada pelo HKLM\...\Run que o MSI grava (Marco C); a bandeja não
// se registra sozinha.
package main

import (
	"os"
	"strings"
)

// Preenchidos por -ldflags no build de release.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

// appVersion é a versão mostrada em "Sobre / versão" e enviada no hello.
// Fora de uma tag, o git describe já começa pelo commit ("5210ef3-dirty"):
// aí o commit não se repete entre parênteses.
func appVersion() string {
	var extra []string
	if commit != "" && !strings.HasPrefix(version, commit) {
		extra = append(extra, commit)
	}
	if date != "" {
		extra = append(extra, date)
	}
	if len(extra) == 0 {
		return version
	}
	return version + " (" + strings.Join(extra, ", ") + ")"
}

func main() { os.Exit(run()) }
```

`cmd/vpnmon-tray/main_other.go`:

```go
//go:build !windows

package main

import (
	"fmt"
	"os"
)

// run fora do Windows só explica: a bandeja usa a API de ícones do Windows.
func run() int {
	fmt.Fprintln(os.Stderr, "vpnmon-tray só roda no Windows")
	return 1
}
```

`cmd/vpnmon-tray/main_windows.go`:

```go
//go:build windows

package main

import (
	"context"
	"errors"
	"log/slog"

	"golang.org/x/sys/windows"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/instance"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/client"
	"github.com/guibsu/vpn-tray-monitor/internal/features/tray/view"
)

// run monta a bandeja: trava de sessão → cliente do pipe → view walk.
func run() int {
	release, err := instance.Acquire(instance.TrayMutex)
	if errors.Is(err, instance.ErrAlreadyRunning) {
		return 0 // já há uma bandeja nesta sessão (login repetido, clique duplo)
	}
	if err != nil {
		fatal("Não foi possível iniciar a bandeja: " + err.Error())
		return 1
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// ipc.Dial confere o PID do servidor do pipe contra o do serviço (§6.1).
	c := client.New(client.Options{Dial: ipc.Dial, AppVersion: appVersion()})
	go c.Run(ctx)

	code, err := view.Run(view.Options{Events: c.Events(), Caller: c, Stats: c.Stats, AppVersion: appVersion(), Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		fatal(err.Error())
	}
	return code
}

// fatal avisa numa caixa de mensagem: windowsgui não tem console.
func fatal(msg string) {
	text, _ := windows.UTF16PtrFromString(msg)
	title, _ := windows.UTF16PtrFromString("VPN Monitor")
	windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR)
}
```

O `main_windows.go` não tem teste no Linux (é só montagem); a garantia aqui é o build cruzado. Segunda bandeja na mesma sessão
(login repetido) sai com código 0, sem caixa de mensagem.

- [ ] **Step 4: Rodar os testes e ver passar**

Run: `go test ./cmd/vpnmon-tray/ && GOOS=windows CGO_ENABLED=0 go build -o /dev/null ./cmd/vpnmon-tray`
Expected: PASS

- [ ] **Step 5: Formatação e vet (linux e windows)**

Run: `gofmt -l . && go vet ./... && GOOS=windows go vet ./...`
Expected: nenhuma saída de erro (gofmt sem arquivos listados)

- [ ] **Step 6: Commit**

```bash
git add cmd/vpnmon-tray
git commit -m "feat(tray): montagem do vpnmon-tray (mutex por sessão, cliente e view)"
```

---

### Task 13: Recursos do exe (go-winres), Makefile, CI e README

**Files:**
- Create: `cmd/vpnmon-tray/winres/winres.json`
- Test: `cmd/vpnmon-tray/winres_test.go`
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`
- Modify: `.gitignore`
- Modify: `README.md`

**Interfaces:**
- Consumes: `assets/conectada.ico` (ícone do exe), `cmd/vpnmon-tray` (Task 12).
- Produces: `cmd/vpnmon-tray/winres/winres.json` (fonte do `.syso`); alvos `make winres`, `make cover-tray` e `make build` (agora com
  `build/vpnmon-tray.exe`, `-H windowsgui`); CI com piso de cobertura do view-model e build dos dois exes no Windows.

- [ ] **Step 1: Escrever o teste que falha**

`cmd/vpnmon-tray/winres_test.go`:

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// O walk exige o comctl32 v6: sem o manifest, a bandeja não abre. O
// winres.json versionado é a fonte do .syso gerado no build.
func TestWinresManifest(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("winres", "winres.json"))
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Icon     map[string]map[string]string `json:"RT_GROUP_ICON"`
		Manifest map[string]map[string]struct {
			ExecutionLevel string `json:"execution-level"`
			DPIAwareness   string `json:"dpi-awareness"`
			CommonControls bool   `json:"use-common-controls-v6"`
		} `json:"RT_MANIFEST"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		t.Fatal(err)
	}
	m := w.Manifest["#1"]["0409"]
	if !m.CommonControls || m.ExecutionLevel != "as invoker" || m.DPIAwareness != "per monitor v2" {
		t.Fatalf("manifest: %+v", m)
	}
	icon := w.Icon["APP"]["0000"]
	if _, err := os.Stat(filepath.Join("winres", icon)); err != nil {
		t.Fatalf("ícone do exe: %v", err)
	}
}
```

- [ ] **Step 2: Rodar o teste e ver falhar**

Run: `go test ./cmd/vpnmon-tray/ -run TestWinresManifest`
Expected: FAIL — `open winres/winres.json: no such file or directory`

- [ ] **Step 3: Implementar**

`cmd/vpnmon-tray/winres/winres.json`:

```json
{
  "RT_GROUP_ICON": {
    "APP": {
      "0000": "../../../assets/conectada.ico"
    }
  },
  "RT_MANIFEST": {
    "#1": {
      "0409": {
        "identity": {
          "name": "VPNMonitor.Tray",
          "version": ""
        },
        "description": "VPN Monitor - bandeja",
        "minimum-os": "win10",
        "execution-level": "as invoker",
        "ui-access": false,
        "auto-elevate": false,
        "dpi-awareness": "per monitor v2",
        "disable-theming": false,
        "disable-window-filtering": false,
        "high-resolution-scrolling-aware": false,
        "ultra-high-resolution-scrolling-aware": false,
        "long-path-aware": false,
        "printer-driver-isolation": false,
        "gdi-scaling": false,
        "segment-heap": false,
        "use-common-controls-v6": true
      }
    }
  },
  "RT_VERSION": {
    "#1": {
      "0000": {
        "fixed": {
          "file_version": "0.0.0.0",
          "product_version": "0.0.0.0"
        },
        "info": {
          "0409": {
            "Comments": "",
            "CompanyName": "",
            "FileDescription": "VPN Monitor - bandeja",
            "FileVersion": "",
            "InternalName": "vpnmon-tray",
            "LegalCopyright": "",
            "LegalTrademarks": "",
            "OriginalFilename": "vpnmon-tray.exe",
            "PrivateBuild": "",
            "ProductName": "VPN Monitor",
            "ProductVersion": "",
            "SpecialBuild": ""
          }
        }
      }
    }
  }
}
```

`Makefile` — depois da linha `WIN     := GOOS=windows GOARCH=amd64`:

```make
# Versão numérica dos recursos do exe (X.Y.Z.0); fora de uma tag, 0.0.0.0.
# A conversão da §10.2 (rc → Z×100+N) entra no release do Marco C.
WINVER  := $(or $(shell echo $(VERSION) | sed -nE 's/^v?([0-9]+)\.([0-9]+)\.([0-9]+).*/\1.\2.\3.0/p'),0.0.0.0)
WINRES  := go run github.com/tc-hib/go-winres@v0.3.3
```

`.PHONY` passa a ser `all lint test cover cover-tray winres build clean`. Depois do alvo `cover`:

```make
# O view-model da bandeja tem piso de 80 % (§10.1).
cover-tray:
	go test -coverprofile=coverage-tray.out ./internal/features/tray/viewmodel/
	@go tool cover -func=coverage-tray.out | awk '/^total:/ { sub("%", "", $$3); \
		if ($$3 + 0 < 80) { print "cobertura do viewmodel: " $$3 "% (mínimo 80%)"; exit 1 } \
		print "cobertura do viewmodel: " $$3 "%" }'

# Manifest (comctl32 v6, que o walk exige; DPI por monitor), ícone e versão
# do vpnmon-tray.exe: gera cmd/vpnmon-tray/rsrc_windows_amd64.syso (não
# versionado) a partir de cmd/vpnmon-tray/winres/winres.json.
winres:
	$(WINRES) make --in cmd/vpnmon-tray/winres/winres.json --out cmd/vpnmon-tray/rsrc --arch amd64 \
		--product-version $(WINVER) --file-version $(WINVER)
```

e os alvos `build` e `clean` ficam:

```make
build: winres
	@mkdir -p $(BUILD)
	$(WIN) CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BUILD)/vpnmon-svc.exe ./cmd/vpnmon-svc
	$(WIN) CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS) -H windowsgui" -o $(BUILD)/vpnmon-tray.exe ./cmd/vpnmon-tray
	@ls -lh $(BUILD)/*.exe

clean:
	rm -rf $(BUILD) coverage.out coverage-tray.out cmd/vpnmon-tray/*.syso
```

`.gitignore` — no bloco "Artefatos de build", acrescentar `*.syso` (depois de `*.exe`) e `coverage-tray.out` (depois de `coverage.out`).

`.github/workflows/ci.yml` — comentário do topo:

```yaml
# CI mínimo dos Marcos A e B: lint, testes no Linux com -race (e piso de
# cobertura do view-model da bandeja) e testes no Windows (inclui os testes de
# integração *_windows_test.go), com o build dos dois exes. O CI completo
# (golangci-lint, govulncheck, cobertura, build do MSI, e2e) vem no Marco C.
```

no job `test-linux`, depois de `- run: go test -race -shuffle=on ./...`:

```yaml
      - name: cobertura do view-model da bandeja (≥ 80 %)
        run: make cover-tray
```

e no job `test-windows` o passo de build vira:

```yaml
      # Produção é sem cgo. A bandeja leva o manifest (comctl32 v6) e é
      # windowsgui.
      - name: build sem cgo
        shell: bash
        env:
          CGO_ENABLED: "0"
        run: |
          go build -trimpath -o vpnmon-svc.exe ./cmd/vpnmon-svc
          go run github.com/tc-hib/go-winres@v0.3.3 make --in cmd/vpnmon-tray/winres/winres.json \
            --out cmd/vpnmon-tray/rsrc --arch amd64
          go build -trimpath -ldflags "-H windowsgui" -o vpnmon-tray.exe ./cmd/vpnmon-tray
```

(O `go test -race ./...` do Windows compila o `vpnmon-tray` sem `.syso`; o manifest só importa para o exe que roda.)

`README.md` — o aviso do topo passa a dizer:

```markdown
> Marco A (núcleo e serviço): serviço instalável por `vpnmon-svc install` e
> operado pela CLI. Marco B: a bandeja `vpnmon-tray.exe`. O MSI/CI completo
> (Marco C) vem depois.
```

o bloco de "Desenvolvimento" vira:

````markdown
```sh
make lint        # gofmt, go mod tidy, go vet (linux e windows)
make test        # go test -race -shuffle=on ./...
make cover-tray  # cobertura do view-model da bandeja (mínimo 80 %)
make build       # build/vpnmon-svc.exe e build/vpnmon-tray.exe (GOOS=windows, CGO_ENABLED=0)
```

O `make build` roda antes o `make winres`, que gera
`cmd/vpnmon-tray/rsrc_windows_amd64.syso` (manifest com comctl32 v6, que o
walk exige, ícone e versão) com o `go-winres` v0.3.3 a partir de
`cmd/vpnmon-tray/winres/winres.json`. Sem o `.syso` a bandeja compila, mas
não abre. O `go run …@v0.3.3` baixa o go-winres pela rede na primeira vez
(depois fica no cache de módulos do Go). Os ícones em `assets/` saem de
`go run ./tools/geniconos`.
````

e, no fim do arquivo, uma seção nova:

```markdown
## Bandeja

`vpnmon-tray.exe` roda uma vez por sessão de usuário (mutex
`Local\VPNMonitorTray`) e conversa com o serviço pelo pipe; sem o serviço, o
ícone fica cinza ("serviço parado") e ela reconecta sozinha. O MSI (Marco C) a
registra no `HKLM\...\Run`; até lá, abra-a à mão. Pelo menu dá para
verificar, reconectar, pausar, desativar, remover e adicionar VPNs (a partir
das entradas RAS de todos os usuários) e, em "Configurações…", editar alvos e
intervalos. Credenciais continuam só pela CLI de administrador
(`vpnmon-svc credential set "<vpn>" --user <usuário>`).
```

Conferir o manifest embutido (opcional, mas foi feito na validação):

```bash
go run github.com/tc-hib/go-winres@v0.3.3 extract --dir /tmp/vpnmon-tray-res --xml-manifest build/vpnmon-tray.exe
grep -c 'Microsoft.Windows.Common-Controls' /tmp/vpnmon-tray-res/app.manifest   # 1
grep -c permonitorv2 /tmp/vpnmon-tray-res/app.manifest                           # 1
```

`make build` deve listar `build/vpnmon-svc.exe` e `build/vpnmon-tray.exe`; `git status` não pode mostrar o `.syso` (ignorado).

- [ ] **Step 4: Rodar os testes e ver passar**

Run: `go test ./cmd/vpnmon-tray/ && make cover-tray && make build && make lint`
Expected: PASS

- [ ] **Step 5: Formatação e vet (linux e windows)**

Run: `gofmt -l . && go vet ./... && GOOS=windows go vet ./...`
Expected: nenhuma saída de erro (gofmt sem arquivos listados)

- [ ] **Step 6: Commit**

```bash
git add cmd/vpnmon-tray/winres cmd/vpnmon-tray/winres_test.go Makefile .github/workflows/ci.yml .gitignore README.md
git commit -m "build: recursos do vpnmon-tray (go-winres), Makefile, CI e README"
```

---

## Roteiro manual no Windows (antes do merge)

A view não roda no Linux nem no CI. Numa máquina Windows 10/11 com o serviço do Marco A instalado (`vpnmon-svc install`, `sc start VPNMonitor`)
e uma entrada RAS de todos os usuários:

1. `make build`, copiar `build/vpnmon-tray.exe` e abrir duas vezes: só um ícone aparece (mutex `Local\VPNMonitorTray`).
2. Ícone e tooltip conforme as VPNs; menu com cabeçalho "N de M VPNs conectadas" e o submenu de cada VPN; reabrir o menu após alguns
   segundos mostra os tempos atualizados.
3. `Adicionar VPN ▸` lista as entradas RAS não monitoradas; adicionar uma cria a VPN com verificação `link`.
4. `Configurações…`: trocar para `ping`, informar host e Salvar; valor fora do limite (ex.: intervalo 2) mostra o erro junto ao campo;
   fechar a janela **não** fecha a bandeja. Opções gerais: desligar avisos e conferir que um "caiu" não gera balão.
5. Derrubar a VPN (`rasdial "<entrada>" /disconnect`): balão "VPN X caiu" e depois "voltou"; ícone âmbar/vermelho e de novo verde.
6. Pausar 15 min / Retomar / Desativar / Ativar / Remover… (confirmação) pelo menu.
7. `Abrir log` mostra o fim do log do serviço; fechar a janela não fecha a bandeja.
8. `sc stop VPNMonitor`: ícone cinza "serviço parado" em até ~10 s; `sc start VPNMonitor`: volta sozinho.
9. Credencial errada (`vpnmon-svc credential set "<vpn>" --user <usuário>` com senha inválida): detalhe com "Credencial rejeitada" e o
   comando completo numa linha própria; reiniciar o serviço dentro de 15 min mostra "Rejeitada antes do reinício do serviço; nova
   tentativa às HH:MM".
10. Com a bandeja fechada, estragar o `config.json` (como administrador) e esperar a recarga; abrir a bandeja: o aviso "⚠ …" aparece no
    menu. Derrubar duas VPNs de uma vez: um balão só ("2 VPNs caíram: …").
11. Arrastar a bandeja para um monitor com outra escala (ou mudar a escala): o ícone continua nítido em até 1 s.
12. "Sobre / versão" mostra a versão da bandeja e a do serviço; "Sair da bandeja" fecha só a bandeja.

## Autorrevisão

- **Cobertura do spec (Marco B, §13 item 2; §§3, 6, 7):** cliente com reconexão (Tasks 4–5), view-model puro (Tasks 6–10), view walk com
  ícone, menu, balões, Configurações e log (Task 11), `vpnmon-tray` com mutex por sessão (Tasks 3 e 12), manifest/recursos no build e no CI
  (Task 13). Pendências do Marco A: credencial restaurada (Tasks 1 e 7) e dica de `credential set` (Task 7). O registro no `HKLM\…\Run` é do
  MSI (Marco C), como manda o spec.
- **Placeholders:** nenhum; todo passo de código traz o arquivo inteiro ou o trecho exato a trocar.
- **Consistência de tipos:** `client.Event`/`Conn`/`ConnState` (Task 4) são os consumidos pelo `viewmodel.VM.Apply` (Task 8) e pela view
  (Task 11); `viewmodel.Command{Type, Payload}` (Task 9) é o que `Tray.call` passa a `Caller.Call`; os nomes de campo do formulário (Task 10)
  são os `FieldError.Field` do serviço (`config.ValidateVPN`).
- **Review Focus:** os cinco itens têm teste na tarefa dona (Tasks 1, 4, 5, 7, 8 e 11).
