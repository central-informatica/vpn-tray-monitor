# VPN Monitor

Mantém VPNs nativas do Windows (RAS) sempre conectadas, com ou sem usuário
logado: um serviço (`vpnmon-svc.exe`) supervisiona cada VPN e reconecta após
quedas, túneis zumbis, suspensão e trocas de rede; uma bandeja por sessão
(`vpnmon-tray.exe`) mostra o estado e permite gerenciar as VPNs.

Desenho completo:
[`docs/superpowers/specs/2026-10-07-vpn-monitor-v2-design.md`](docs/superpowers/specs/2026-10-07-vpn-monitor-v2-design.md).

Requisitos: Windows 10 1809+, Windows 11 ou Windows Server 2019+, 64 bits.

## Instalação

O VPN Monitor é distribuído como MSI por máquina
(`VPNMonitor-<versão>-x64.msi`, em
[Releases](../../releases)). Ele instala:

- `C:\Program Files\VPN Monitor\` com os dois exes, a licença e este README;
- o serviço `VPNMonitor` (automático, LocalSystem, depende do RasMan);
- a pasta de dados `C:\ProgramData\VPNMonitor\` (só SYSTEM e
  Administradores);
- a bandeja no `HKLM\…\Run` (abre no próximo login de cada usuário) e um
  atalho "VPN Monitor" no menu Iniciar — o MSI não abre a bandeja ao
  terminar; abra-a pelo atalho;
- a origem `VPNMonitor` do Event Log (Aplicativo).

**Antes:** a entrada RAS precisa existir para todos os usuários (o serviço
roda como LocalSystem e só enxerga esse catálogo):

```powershell
Add-VpnConnection -Name "VPN Matriz" -ServerAddress vpn.exemplo.com.br -AllUserConnection
```

### Instalação silenciosa e seed da primeira configuração

```powershell
msiexec /i VPNMonitor-2.1.0-x64.msi /qn /l*v C:\Windows\Temp\vpnmon.log `
  VPN_ENTRY="VPN Matriz" VPN_NAME=Matriz CHECK_HOST=10.254.1.172
```

| Propriedade | Uso | Padrão |
|---|---|---|
| `VPN_ENTRY` | entrada RAS da primeira VPN | sem ela, config vazia |
| `VPN_NAME` | nome da VPN no VPN Monitor | igual à entrada |
| `CHECK_KIND` | `ping`, `tcp` ou `link` | `ping` com host, `link` sem |
| `CHECK_HOST` | alvo do ping/tcp | — |
| `CHECK_PORT` | porta do `tcp` | — |
| `INTERVAL` | intervalo entre verificações, em segundos | 30 |
| `PURGE` | `PURGE=1` na desinstalação apaga a pasta de dados | — |

As propriedades vão para `HKLM\SOFTWARE\VPNMonitor\Seed`; o serviço só as
lê no **primeiro** início sem `config.json`. Depois disso, a configuração
muda pela bandeja ou pela CLI. Os valores não podem começar com `#` (o
Windows Installer leria como número). **Nenhuma senha passa pelo MSI.**

A credencial é gravada depois, pela CLI elevada. Sem `--password-stdin` ela
pede a senha sem eco:

```powershell
& "$env:ProgramFiles\VPN Monitor\vpnmon-svc.exe" credential set Matriz --user DOMINIO\usuario
```

Não escreva a senha na linha de comando (`"senha" | …`): ela fica no
histórico do PowerShell (PSReadLine) e no log de scripts.

[`scripts/deploy-exemplo.ps1`](scripts/deploy-exemplo.ps1) junta os dois
passos (instalação silenciosa + `credential set` com a senha num
`SecureString`) e serve de base para Intune e GPO.

### GPO e Intune

- **GPO (atribuição de software, computador):** publique o MSI num
  compartilhamento lido pelas contas de computador. Propriedades do seed
  entram por uma transformação (`.mst`, ex.: gerada no Orca) — a GPO não
  aceita linha de comando.
  **Nunca coloque a senha da VPN em script de inicialização da GPO nem no
  SYSVOL: qualquer usuário do domínio lê.** Prefira rodar o
  `credential set` interativamente, num prompt de administrador, ou por uma
  ferramenta de gestão que entregue o segredo de forma protegida. Se não
  gravar credencial, o serviço usa a credencial salva no Windows para a
  entrada RAS (ou o certificado, em VPN por certificado), o que também
  serve de alternativa.
- **Intune:** como app de linha de negócios (MSI, com os argumentos acima) ou
  como app Win32 empacotando o `deploy-exemplo.ps1` com o MSI. Detecção pelo
  código de produto do MSI ou pelo *valor* `Version` da chave
  `HKLM\SOFTWARE\VPNMonitor`. Esse valor guarda a `ProductVersion` do MSI,
  não a versão do nome do arquivo: a 2.1.0 grava `2.1.99` e a 2.1.0-rc.2,
  `2.1.2` (ver "Release"). Na regra do Intune, compare como versão
  (`maior ou igual a 2.1.99`), não como texto. O Intune executa o script em
  32 bits e como SYSTEM (sem console para pedir senha): o `deploy-exemplo.ps1` resolve o
  caminho de 64 bits, mas a senha precisa de outro meio (ver o aviso acima).

### Atualização

Instalar uma versão mais nova por cima atualiza (o `UpgradeCode` é fixo);
`config.json`, estado, cofre e logs ficam. As VPNs conectadas **não caem**
durante a troca (a confirmar no roteiro manual, §18). Instalar uma versão
mais velha por cima é recusado.

O MSI desliga o Restart Manager (`MSIRESTARTMANAGERCONTROL=Disable`): a
bandeja aberta nas sessões **não é fechada**. Com ela aberta, o exe está em
uso, o msiexec termina com **3010** e a troca do `vpnmon-tray.exe` fica para
a reinicialização; até lá a bandeja antiga segue funcionando com o serviço
novo (a confirmar no roteiro manual, §18). Trate 3010 como sucesso no
GPO/Intune. O mesmo vale para a desinstalação com a bandeja aberta. Esse
comportamento está **a confirmar no roteiro manual** (§18 e §22): o e2e do
CI não tem sessão com bandeja.

Num upgrade sem as propriedades do seed, a chave
`HKLM\SOFTWARE\VPNMonitor\Seed` é regravada **vazia**. É inofensivo: o
serviço só lê o seed quando **não existe** `config.json`, e num upgrade ele
existe, então a configuração atual é preservada. O efeito só aparece se o
`config.json` for apagado depois: o serviço sobe com config vazia, não com a
do seed original (repita as propriedades no upgrade se quiser poder
re-semear).

**Versões:** reconstruir o MSI da *mesma* versão gera outro ProductCode, e o
Windows Installer o instala **lado a lado** em vez de atualizar. Por isso a
versão só sobe por tag (`vX.Y.Z`); não gere dois MSIs diferentes com o mesmo
número.

O atalho do menu Iniciar é "anunciado" (do Windows Installer): ao abri-lo,
o Windows confere os componentes do produto e, se algum sumiu (ex.: alguém
apagou a origem `VPNMonitor` do Event Log), dispara um reparo do MSI, que
pode pedir UAC e a mídia (o MSI de origem, se o Windows não o achar no
cache). O reparo também regrava o seed **vazio**, o que é inofensivo: a
config existente é preservada. A bandeja aberta pelo `Run` não passa por
isso.

### Desinstalação

```powershell
msiexec /x VPNMonitor-2.1.0-x64.msi /qn            # mantém C:\ProgramData\VPNMonitor
msiexec /x VPNMonitor-2.1.0-x64.msi /qn PURGE=1    # apaga também a pasta de dados
```

O `PURGE=1` apaga só `C:\ProgramData\VPNMonitor`. As pastas
`C:\ProgramData\VPNMonitor.naoconfiavel-*` (§5.1) — o diretório de dados de
que o serviço desconfiou e que renomeou, dados preservados — **não** são
removidas pelo `PURGE=1`: apague-as à mão depois de conferir o conteúdo.

Se o serviço estiver gravando no momento da desinstalação (por exemplo, logo
depois de subir), o `PURGE=1` pode deixar a pasta para trás: o
`RemoveFolderEx` do WiX lista os arquivos antes de o serviço parar. Para uma
remoção garantida, pare o serviço antes (`sc.exe stop VPNMonitor`) ou apague a
pasta à mão depois.

Quem instalou à mão com `vpnmon-svc install` deve rodar
`vpnmon-svc uninstall` antes de instalar o MSI.

### Limitações conhecidas e pontos provisórios

- **Junção na pasta de dados:** se um usuário comum pré-criar
  `%ProgramData%\VPNMonitor` como junção antes da primeira instalação, o
  `PermissionEx` do MSI age no destino da junção; sem custom action própria
  não há defesa no MSI (o serviço detecta a pasta adulterada na partida e a
  põe em quarentena). Ver spec §9.
- **Pasta de dados pré-criada:** o MSI aplica à
  `%ProgramData%\VPNMonitor` só a lista de acesso (SYSTEM e Administradores),
  **sem trocar o dono**. Se um usuário comum a criou antes da instalação, ela
  continua dele e o serviço, na partida, a põe de lado
  (`VPNMonitor.naoconfiavel-*`) e cria outra, vazia. Numa instalação limpa o
  dono é SYSTEM, aceito pelo serviço.
- **Fabricante provisório:** "Central Informática" ainda **precisa ser
  confirmado**. Para trocar, edite `installer/Product.wxs` (`<?define
  Manufacturer = … ?>`) e o `CompanyName` de
  `cmd/vpnmon-svc/winres/winres.json` e `cmd/vpnmon-tray/winres/winres.json`.

## Uso (prompt de administrador)

```text
vpnmon-svc status [--json]              estado de cada VPN (--json: para scripts)
vpnmon-svc vpn add --name Matriz --entry "VPN Matriz" --check ping --host 10.254.1.172
vpnmon-svc credential set Matriz --user dominio\usuario
vpnmon-svc check Matriz                 verificação única, sem discar
vpnmon-svc install | uninstall          registro manual do serviço (sem MSI)
vpnmon-svc version
```

O serviço confere a cada partida a política do SCM — reiniciar após 5 s,
30 s e 60 s (zerando em 1 dia, também quando para com erro) e 15 s de
preshutdown — e regrava só o que divergir, porque a tabela de recuperação do
Windows Installer não funciona (a Microsoft documenta isso); assim, MSI e
`install` ficam iguais. Um ajuste diferente feito à mão no `services.msc`
volta ao padrão na partida seguinte.

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
ícone fica cinza ("serviço parado") e ela reconecta sozinha. Pelo menu dá
para verificar, reconectar, pausar, desativar, remover e adicionar VPNs (a
partir das entradas RAS de todos os usuários) e, em "Configurações…", editar
alvos e intervalos. Credenciais continuam só pela CLI de administrador
(`vpnmon-svc credential set "<vpn>" --user <usuário>`).

## Diagnóstico

| O quê | Onde |
|---|---|
| Log do serviço | `%ProgramData%\VPNMonitor\logs\vpnmon.log` (+ `vpnmon.1.log` … `vpnmon.5.log`); pela bandeja, "Abrir log" |
| Eventos do serviço | Visualizador de Eventos → Aplicativo, origem `VPNMonitor` |
| Log da bandeja | `%LOCALAPPDATA%\VPNMonitor\vpnmon-tray.log` (por usuário, 1 MB × 3) |
| Estado atual | `vpnmon-svc status --json` |
| Instalação | log do `msiexec /l*v` |
| Política do serviço | `sc qc VPNMonitor`, `sc qfailure VPNMonitor`, `sc qfailureflag VPNMonitor` |

## Desenvolvimento

```sh
make lint          # gofmt, go mod tidy, go vet e golangci-lint (linux e windows)
make test          # go test -race -shuffle=on ./...
make cover         # piso de 80 % no núcleo (tools/covergate)
make build         # build/vpnmon-svc.exe e build/vpnmon-tray.exe (reproduzível)
make repro         # build duas vezes e compara os hashes
make lint-scripts  # PSScriptAnalyzer nos scripts (exige pwsh)
make lint-workflows  # actionlint
```

Ferramentas: Go da versão do `go.mod`, golangci-lint v2.14.0 e, para os
scripts, pwsh com PSScriptAnalyzer 1.24.0 (o `scripts/lint.ps1` instala). Os
scripts `.ps1` são gravados em UTF-8 **com BOM** (o Windows PowerShell 5.1
lê sem BOM como ANSI e estraga os acentos); o lint confere.

**Risco aceito:** o WiX e o PSScriptAnalyzer ficam fixados só por versão,
não por hash (o `go-winres` e as actions do CI, sim).

O `make build` gera antes os recursos dos exes (`go-winres`: manifest,
ícone e versão; o manifest com comctl32 v6 é obrigatório para a bandeja
abrir). O go-winres fica fixado por versão e hash num módulo só de
ferramenta, `tools/winres/go.mod`, fora do grafo dos binários
(`go tool -modfile=tools/winres/go.mod go-winres …`). A versão dos exes vem
da tag (`tools/msiversion`); fora de tag, 0.0.0.0.

O MSI só é gerado no Windows:
`pwsh scripts/build-msi.ps1 -Semver 2.1.0 -ProductVersion 2.1.99 -BinDir build -OutDir dist`
(instala o WiX 5.0.2 como ferramenta do dotnet se faltar; o
`dotnet tool update --allow-downgrade` do script exige o .NET 8 SDK ou mais
novo). O
`installer/installer_test.go` roda em qualquer lugar e confere o `.wxs`
contra as constantes do código (nome do serviço, SDDL da pasta, seed,
Event Log, nenhuma custom action).

Toda a lógica roda e é testada no Linux com fakes
(`internal/core/platform/fake`); os testes `*_windows_test.go` rodam no job
Windows do CI. O que só o Windows real cobre está em
[`docs/TESTE-MANUAL.md`](docs/TESTE-MANUAL.md).

## CI/CD

`ci.yml` (push, PR, semanal no `main`): `lint`, `security` (govulncheck),
`codeql`, `test-linux` (com cobertura), `test-windows`, `build` (exes,
reproduzível) → `msi` (WiX, sem assinatura; artefato `msi`) → `e2e` (roteiro
do MSI num runner Windows: instala com seed, confere serviço, ACL e política,
discagem com backoff, credencial, parada ≤ 10 s, upgrade 0.0.199 → 0.0.299,
downgrade recusado, desinstalação e `PURGE=1`, pasta de dados pré-criada por
usuário comum indo para a quarentena; logs no artefato `e2e-logs`).

### Release

1. Garanta o `main` verde.
2. Crie e envie a tag no commit do `main`:
   `git tag -a v2.1.0 -m "VPN Monitor 2.1.0" && git push origin v2.1.0`
   (candidatas: `v2.1.0-rc.1`, `-rc.2`…; saem como pré-release).
3. O `release.yml` valida a tag (formato e limites da §10.2) e se o commit
   está no `main`, roda o CI inteiro, assina (se ligado), gera MSI e zip
   portátil, `SHA256SUMS`, SBOM CycloneDX, proveniência e as notas
   (git-cliff, a partir dos commits convencionais) e publica a release.

Versão do MSI: `vX.Y.Z` → `X.Y.(Z×100+99)`; `vX.Y.Z-rc.N` → `X.Y.(Z×100+N)`
(N de 1 a 98; X, Y ≤ 255; Z ≤ 654). Assim uma rc é sempre menor que a final.

Conferir um download:

```sh
sha256sum -c SHA256SUMS --ignore-missing
gh attestation verify VPNMonitor-2.1.0-x64.msi --repo central-informatica/vpn-tray-monitor
```

**Assinatura:** desligada por padrão; as notas dizem "binários não
assinados". Para ligar, defina a variável `SIGNING_ENABLED=true` e as
variáveis `SIGN_DLIB`, `SIGN_DLIB_METADATA` e `SIGN_TIMESTAMP_URL`
(opcional: `SIGN_EXPECTED_SHA1`, o thumbprint SHA-1 que o assinante precisa
ter) conforme o cabeçalho de [`scripts/sign.ps1`](scripts/sign.ps1). A
assinatura de fato (HSM ou Azure Trusted Signing) é plugada no `sign.ps1`
pelo time; com OIDC (Azure Trusted Signing), o job `package` do `release.yml`
precisa de `permissions: id-token: write`, hoje ausente.

### Configuração do repositório para a release

Só o time com acesso de administrador ao GitHub consegue; faça uma vez:

1. **Environment `release`** (Settings → Environments): *required reviewers*
   e *Deployment branches and tags* restrito a tags `v*`. O job `package`
   roda nele.
2. **Proteção de tags `v*`** (Settings → Rules → Rulesets, alvo "tags"): só
   mantenedores criam, alteram ou apagam.
3. **Variáveis** (do environment ou do repositório): `SIGNING_ENABLED` e,
   se for assinar, `SIGN_DLIB`, `SIGN_DLIB_METADATA`, `SIGN_TIMESTAMP_URL` e,
   opcionalmente, `SIGN_EXPECTED_SHA1`. Segredos do provedor ficam no
   environment.
4. O `release.yml` **só se prova com uma tag real**, depois do merge na
   `main` (ele recusa tag fora do `main`); a primeira deve ser uma
   candidata (`v2.1.0-rc.1`).

### Proteção de branch recomendada (`main`)

- Exigir PR, com 1 aprovação e revisão descartada a cada push novo.
- Exigir os checks `lint`, `security`, `codeql`, `test-linux`,
  `test-windows`, `build`, `msi` e `e2e`, com o ramo atualizado.
- Histórico linear; sem push forçado nem exclusão.
- Proteger as tags `v*` (regra de tag: só mantenedores criam).
- Environment `release` (Settings → Environments) com **revisores
  obrigatórios** e **"Deployment branches and tags" restrito a tags `v*`**:
  é nele que ficam as variáveis/segredos da assinatura, e só uma tag de
  release chega a ele.
