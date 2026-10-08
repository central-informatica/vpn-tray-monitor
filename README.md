# VPN Monitor

Mantém VPNs nativas do Windows (RAS) sempre conectadas, com ou sem usuário
logado: um serviço (`vpnmon-svc.exe`) supervisiona cada VPN e reconecta após
quedas, túneis zumbis, suspensão e trocas de rede.

> **Em desenvolvimento (v2).** O desenho completo está em
> [`docs/superpowers/specs/2026-10-07-vpn-monitor-v2-design.md`](docs/superpowers/specs/2026-10-07-vpn-monitor-v2-design.md).
> Marco A (núcleo e serviço): serviço instalável por `vpnmon-svc install` e
> operado pela CLI. Marco B: a bandeja `vpnmon-tray.exe`. O MSI/CI completo
> (Marco C) vem depois.

## Desenvolvimento

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

## Edição manual do config.json

Prefira a CLI (`vpnmon-svc vpn add|remove`). Para editar à mão
`%ProgramData%\VPNMonitor\config.json`, use um editor que salva no próprio
arquivo (o Bloco de Notas faz isso), aberto como administrador. Editores que
salvam por arquivo temporário + renomeação (VS Code e outros) criam um arquivo
novo com dono = a sua conta: a pasta de dados deixa de conferir (§5.1) e, na
próxima partida do serviço, vai inteira para o lado
(`VPNMonitor.naoconfiavel-*`, dados preservados) e o serviço sobe vazio.

A CLI elevada não tem esse problema: ela faz o próprio processo criar arquivos
com dono Administradores antes de gravar.

## Bandeja

`vpnmon-tray.exe` roda uma vez por sessão de usuário (mutex
`Local\VPNMonitorTray`) e conversa com o serviço pelo pipe; sem o serviço, o
ícone fica cinza ("serviço parado") e ela reconecta sozinha. O MSI (Marco C) a
registra no `HKLM\...\Run`; até lá, abra-a à mão. Pelo menu dá para
verificar, reconectar, pausar, desativar, remover e adicionar VPNs (a partir
das entradas RAS de todos os usuários) e, em "Configurações…", editar alvos e
intervalos. Credenciais continuam só pela CLI de administrador
(`vpnmon-svc credential set "<vpn>" --user <usuário>`).
