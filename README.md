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
