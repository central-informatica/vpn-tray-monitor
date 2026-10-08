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
