# VPN Monitor v2 — Desenho

- **Data:** 2026-10-07
- **Status:** aprovado em conversa; aguardando revisão do texto
- **Substitui:** a versão inicial (commit `997007a`), um app de bandeja único

## 1. Objetivo

Manter uma ou mais VPNs nativas do Windows (RAS/rasdial) sempre de pé numa máquina,
**com ou sem usuário logado**, recuperando-se sozinho de quedas, túneis zumbis,
suspensão e trocas de rede, sem travar, sem bloquear contas no AD e sem exigir
intervenção. Distribuído por MSI (GPO/Intune), com CI/CD que gera releases no
GitHub a cada tag.

### Critérios de sucesso

1. Com o serviço instalado, cada VPN configurada volta sozinha após queda do
   enlace, túnel sem tráfego, suspensão/retomada e troca de rede.
2. Credencial rejeitada nunca gera tentativas repetidas.
3. O serviço não trava: toda operação externa tem prazo; parada em ≤10 s.
4. A bandeja mostra o estado real em tempo real, emite balões reais e permite
   gerenciar as VPNs (inclusive editar alvos e intervalos) sem editar JSON.
5. Todos os problemas listados na seção 13 estão corrigidos e cobertos por teste.
6. CI verde em Linux e Windows, com e2e do MSI; tag `v*` publica a release.

### Fora do escopo

- Conectores não nativos (OpenVPN, WireGuard, FortiClient, comandos livres).
- Alertas remotos (webhook, Telegram, e-mail). Só balão local, log e Event Log.
- Arquiteturas diferentes de x64 (arm64, x86).
- Migração da config v1 (a versão inicial não foi implantada).
- A assinatura de código em si: o pipeline tem o gancho, mas o time de infra
  pluga o HSM depois.
- Diálogo de senha na interface: credenciais só pela CLI de administrador.

## 2. Decisões tomadas

| Tema | Decisão |
|---|---|
| Execução | Serviço do Windows (LocalSystem) + bandeja por sessão como cliente |
| Distribuição | MSI (WiX v5), implantação silenciosa por GPO/Intune |
| Credenciais | Cofre próprio (DPAPI máquina) → credencial salva no Windows → sem credencial |
| Permissões na bandeja | Qualquer usuário interativo local tem controle total |
| Avisos | Balão local + log; Event Log só para eventos do serviço |
| Tipos de VPN | Só nativa (RAS). Verificação por `ping`, `tcp` ou `link` (só enlace) |
| Quantidade | Várias VPNs simultâneas, cada uma com seu supervisor |
| Processos | Dois exes: `vpnmon-svc.exe` (serviço + CLI) e `vpnmon-tray.exe` (GUI) |
| Canal | Named pipe `\\.\pipe\vpnmon`, JSON por linha, ACL restrita |
| UI | `github.com/tailscale/walk` (ícone de bandeja, balões, janela de configurações) |
| Credencial rejeitada | Para de tentar até a credencial mudar ou haver pedido manual |
| Túnel zumbi | Desliga (`RasHangUp`) e redisca após N falhas de alcance |
| Parada/upgrade/remoção | Não derruba as VPNs |
| CI/CD | GitHub Actions; release no GitHub Releases por tag `v*` |
| Assinatura | Passo plugável, desligado por padrão (`SIGNING_ENABLED`) |

## 3. Arquitetura

```
┌────────────── Sessão do usuário (1 por usuário logado) ─────────────┐
│ vpnmon-tray.exe  (windowsgui)                                       │
│   view (walk) ← viewmodel (puro) ← cliente do pipe                  │
└────────────────────────────────┬────────────────────────────────────┘
                                 │ \\.\pipe\vpnmon
┌────────────────────────────────┴──────── Sessão 0 / LocalSystem ────┐
│ vpnmon-svc.exe  (console; serviço + CLI admin)                      │
│   servidor do pipe → orquestrador → N supervisores (1 por VPN)      │
│   config + estado + cofre DPAPI(máquina) + log + Event Log          │
└─────────────────────────────────────────────────────────────────────┘
```

### 3.1 Organização do código

```
cmd/vpnmon-svc/        montagem do serviço e da CLI
cmd/vpnmon-tray/       montagem da bandeja
internal/core/
  config/              tipos, validação, padrões, leitura estrita, gravação atômica, seed
  logging/             slog + rotação por tamanho + Event Log + redação
  ipc/                 protocolo (tipos, versão, codec), servidor, cliente
  platform/            interfaces de SO + impl. Windows + fakes
    ras/               discagem, enumeração, status, hangup, notificação, pbk
    icmp/              eco ICMP
    dpapi/             proteger/desproteger (escopo máquina)
    svc/               integração com o SCM, eventos de energia/sessão
    netwatch/          avisos de mudança de rede
    acl/               aplicar e verificar ACLs em arquivos e pastas
internal/features/
  monitor/
    domain/            estados, entradas, política de transição (funções puras)
    service/           supervisor (ator), orquestrador, fila de discagem
    adapters/          checkers (ping, tcp, link), discador RAS
  credentials/         cofre + resolução da credencial na discagem
  tray/
    viewmodel/         estado do serviço → modelo de tela (puro, testável)
    view/              walk: ícone, menu, balões, janela de configurações
    client/            cliente do pipe com reconexão
internal/shared/       backoff com jitter, clock, secret, atomicfile
installer/             WiX v5 (.wxs), recursos, licença
scripts/               sign.ps1, deploy-exemplo.ps1, e2e/*.ps1
.github/               workflows, dependabot
```

### 3.2 Regras de dependência

- `features/*` dependem de `core` e `shared`. Nenhuma feature importa outra;
  serviço e bandeja se falam pelos tipos de `core/ipc`.
- Todo acesso ao SO passa por interfaces em `core/platform`. Implementações
  Windows ficam em arquivos `_windows.go`; fakes ficam em `platform/fake`.
  A lógica inteira roda e é testada no Linux.
- `tray/viewmodel` não conhece walk. Recebe eventos e devolve um modelo de
  tela imutável. `tray/view` só desenha esse modelo e repassa cliques.

## 4. Motor de monitoramento (`features/monitor`)

### 4.1 Supervisor como ator

Cada VPN tem uma goroutine dona exclusiva do seu estado. As entradas chegam por
um canal: `checkNow`, `reconnect`, `pause(duração|indefinida)`, `resume`,
`updateConfig`, `stop`, mais os despertares (timer, desconexão RAS, mudança de
rede, retomada de energia). As saídas são eventos publicados no barramento do
orquestrador. Não há estado compartilhado com a UI.

A decisão de cada ciclo é uma função pura em `domain`:
`Decide(estadoAtual, observação, config, agora) → (novoEstado, ação, espera, eventos)`.
O supervisor só executa a ação (verificar, discar, desligar) e alimenta a
próxima observação.

### 4.2 Estados

| Estado | Significado |
|---|---|
| `Desconhecido` | antes da primeira verificação |
| `Conectada` | enlace de pé e alvo alcançável |
| `Degradada` | enlace de pé, alvo sem resposta, abaixo de N falhas |
| `Reconectando` | discagem em andamento ou aguardando próxima tentativa |
| `Desconectada` | enlace caído, aguardando backoff |
| `CredencialInvalida` | credencial rejeitada; não tenta de novo sozinho |
| `ErroConfig` | entrada RAS inexistente ou exige interação; não tenta sozinho |
| `Pausada` | pausa manual (até um instante, ou indefinida) |
| `SemRede` | máquina sem nenhuma interface com rota padrão |
| `Desativada` | `enabled: false` na config |

### 4.3 Ciclo

1. **Enlace:** `ras.Status(entry)`. Se não conectado, vai para `Reconectando`
   imediatamente e disca.
2. **Alcance** (com enlace de pé), conforme `check.kind`:
   - `ping`: eco ICMP via `IcmpSendEcho2`; só o status 0 conta como sucesso.
   - `tcp`: conexão com prazo; sucesso ao concluir o handshake.
   - `link`: não há verificação de alcance; o enlace basta.
3. Falha de alcance incrementa o contador (`Degradada`). Ao atingir
   `failuresBeforeReconnect`, o túnel é zumbi: `RasHangUp` seguido de discagem.
4. Após discagem bem-sucedida, aguarda `graceAfterConnectSeconds` antes de
   voltar a contar falhas de alcance.
5. Sem nenhuma interface com rota padrão: `SemRede`, sem discar e sem gastar
   backoff; um aviso de mudança de rede reavalia.

### 4.4 Discagem

- `RasDialW` em modo assíncrono, com callback mínimo; o supervisor acompanha com
  `RasGetConnectStatus` a cada 250 ms.
- Timeout (`connectTimeoutSeconds`, padrão 60) ou parada do serviço → `RasHangUp`
  e espera o handle ser liberado.
- **Fila global com uma discagem por vez** entre todas as VPNs.
- Estruturas RAS (`RASDIALPARAMSW`, `RASCONNSTATUSW`, `RASCONNW` etc.) montadas
  byte a byte com empacotamento de 4 bytes (`pshpack4` do `ras.h`); tamanhos e
  deslocamentos fixados por teste.
- Ao iniciar, o serviço adota conexões RAS já ativas das entradas configuradas,
  sem rediscar.

### 4.5 Classificação de erros RAS

| Classe | Exemplos | Comportamento |
|---|---|---|
| Transitório | 600–699 não listados abaixo, 800, 809, 868, timeouts | backoff exponencial com jitter (±20%), do intervalo até `maxBackoffSeconds` |
| Credencial | 691, 718, 917, 919 | `CredencialInvalida`; para até a credencial mudar ou pedido manual |
| Configuração | 623, 703, 720, 735 | `ErroConfig`; para até a config mudar ou pedido manual |

A tabela de códigos fica num único arquivo com teste. Códigos desconhecidos
contam como transitórios.

### 4.6 Despertares

- Timer do próximo ciclo.
- `RasConnectionNotification` (evento de desconexão de qualquer entrada).
- `NotifyAddrChange` (mudança de endereços).
- `SERVICE_CONTROL_POWEREVENT` com `PBT_APMRESUMEAUTOMATIC`: zera o backoff e
  verifica após 5 s.

Despertares se agregam: vários pendentes geram um só ciclo.

### 4.7 Pausa e comandos manuais

- A pausa é uma entrada do ator, então sempre vence um ciclo em andamento.
  A pausa temporária expira sozinha; o estado de pausa persiste em `state.json`.
- `reconnect` durante a pausa responde erro "pausada", sem mudar o estado.
- `reconnect` com discagem em andamento responde "já reconectando".
- `reconnect` em `CredencialInvalida` ou `ErroConfig` faz uma tentativa.

### 4.8 Avisos

O supervisor publica `vpnState` em toda mudança de estado e `notice` só nos
fatos que interessam ao usuário:

- "VPN X caiu" — uma vez por queda (do primeiro estado ruim após `Conectada`).
- "VPN X voltou (fora do ar por 4 min)".
- "VPN X: credencial rejeitada — rode `vpnmon-svc credential set "X"`".
- "VPN X: erro de configuração: <motivo>".

### 4.9 Orquestrador e robustez

- Mantém um supervisor por VPN ativa. Ao recarregar a config, cria, remove ou
  reinicia só as VPNs cujo trecho de config mudou.
- Cada supervisor roda sob `recover`. Um panic vai para log e Event Log; o
  supervisor é recriado após 5 s, com backoff se repetir.
- Instância única do serviço garantida pelo SCM. A bandeja usa um mutex nomeado
  por sessão (`Local\VPNMonitorTray`).

## 5. Configuração, credenciais e persistência

### 5.1 Arquivos

Pasta `C:\ProgramData\VPNMonitor\`, ACL: SYSTEM e Administradores com controle
total, herança desligada, nenhum outro acesso. Criada pelo MSI e verificada e
corrigida pelo serviço a cada início.

```
config.json
state.json
credentials\<id>.bin
logs\vpnmon.log, vpnmon.1.log … vpnmon.5.log
```

### 5.2 `config.json` (versão 2)

```json
{
  "version": 2,
  "notifications": true,
  "logLevel": "info",
  "log": { "maxSizeMB": 1, "maxFiles": 5 },
  "vpns": [
    {
      "name": "Matriz",
      "rasEntry": "VPN Matriz",
      "enabled": true,
      "check": { "kind": "ping", "host": "10.254.1.172", "timeoutSeconds": 5 },
      "intervalSeconds": 30,
      "failuresBeforeReconnect": 3,
      "graceAfterConnectSeconds": 15,
      "connectTimeoutSeconds": 60,
      "maxBackoffSeconds": 300
    }
  ]
}
```

| Campo | Padrão | Limites |
|---|---|---|
| `check.kind` | `ping` | `ping`, `tcp`, `link` |
| `check.host` | — | obrigatório em `ping`/`tcp`; IPv4 literal ou nome |
| `check.port` | — | obrigatório em `tcp`; 1–65535 |
| `check.timeoutSeconds` | 5 | 1–30 |
| `intervalSeconds` | 30 | 5–3600 |
| `failuresBeforeReconnect` | 3 | 1–20 |
| `graceAfterConnectSeconds` | 15 | 0–300 |
| `connectTimeoutSeconds` | 60 | 10–300 |
| `maxBackoffSeconds` | 300 | ≥ `intervalSeconds`, ≤ 3600 |

- `name` é a identidade: 1–64 caracteres, único sem diferenciar maiúsculas.
  O nome do arquivo de credencial é derivado dele de forma segura (hash curto +
  versão saneada), sem permitir caminho.
- Leitura estrita: campos desconhecidos são erro; BOM é aceito.
- Gravação sempre atômica (temporário na mesma pasta, `fsync`, rename).
- Alterações pela bandeja chegam como comandos pelo pipe; o serviço valida e
  grava. Uma config inválida nunca é gravada.
- Edição manual: o serviço observa o arquivo e recarrega. Se inválido, mantém
  a config anterior, registra no log e no Event Log e publica `configStatus`
  com o motivo.

### 5.3 Seed da instalação

O MSI grava as propriedades `VPN_ENTRY`, `VPN_NAME`, `CHECK_KIND`, `CHECK_HOST`,
`CHECK_PORT` e `INTERVAL` em `HKLM\SOFTWARE\VPNMonitor\Seed`. No primeiro início
sem `config.json`, o serviço gera a config a partir delas. Sem seed e sem config,
gera uma config vazia válida (nenhuma VPN) e aguarda.

### 5.4 Credenciais

- Cofre: `credentials\<id>.bin`, conteúdo `{usuário, senha}` protegido com
  `CryptProtectData` (`CRYPTPROTECT_LOCAL_MACHINE`) e entropia fixa do app.
  No escopo de máquina qualquer processo local poderia decifrar o arquivo se
  pudesse lê-lo; **a proteção real é a ACL** (só SYSTEM e Administradores).
- Resolução na discagem: cofre → `RasGetEntryDialParams` (credencial salva para
  todos os usuários) → discar sem credencial (VPN por certificado).
- Gestão só pela CLI elevada:
  `credential set <vpn> --user X [--password-stdin]`, `credential clear <vpn>`,
  `credential list` (só mostra quais VPNs têm credencial).
- A troca de credencial avisa o supervisor da VPN, que sai de
  `CredencialInvalida`.
- Senhas trafegam no tipo `shared.Secret` (`String()` e JSON devolvem `***`);
  o buffer é zerado após a discagem.

### 5.5 `state.json`

Guarda pausas (`pausedUntil` ou indefinida) por VPN. Corrompido → renomeado para
`state.json.corrompido-<data>` e o serviço segue com estado vazio.

### 5.6 Logs

- `slog` em texto, `vpn=<nome>` em toda linha da VPN, rotação por tamanho.
- Event Log (origem `VPNMonitor`): início e parada do serviço, erros fatais,
  config inválida, panics recuperados.

## 6. Canal bandeja–serviço (`core/ipc`)

### 6.1 Transporte e segurança

- `\\.\pipe\vpnmon` via `github.com/Microsoft/go-winio`, com
  `PIPE_REJECT_REMOTE_CLIENTS`.
- SDDL: SYSTEM e Administradores com acesso total, Usuários Interativos (`IU`)
  com leitura e escrita, Rede (`NU`) negada.
- Mensagem máxima de 64 KB; prazo de leitura e escrita; no máximo 32 conexões.
- Decodificação estrita (campos desconhecidos são erro).
- Cada conexão roda sob `recover`. Cliente que não consome eventos (fila de
  saída cheia) é desconectado.

### 6.2 Protocolo

Uma mensagem JSON por linha: `{"v":1,"id":"…","type":"…","payload":{…}}`.

- **Handshake:** `hello{protocol, appVersion}` nos dois sentidos. Protocolo
  incompatível → erro `incompatible` e a bandeja mostra "atualize o VPN Monitor".
- **Pedidos** (resposta com o mesmo `id`, `ok` ou `error{code, message}`):
  `status`, `checkNow{vpn}`, `reconnect{vpn}`, `pause{vpn, untilUnix|null}`,
  `resume{vpn}`, `setEnabled{vpn, enabled}`, `addVpn{config}`,
  `updateVpn{name, config}`, `removeVpn{name}`, `listRasEntries`,
  `getConfig`, `setGlobal{notifications, logLevel}`.
- **Eventos** (após `subscribe`): `snapshot` completo, depois `vpnState`,
  `notice`, `configStatus`, `serviceStopping`.
- O `snapshot` e o `vpnState` trazem por VPN: estado, desde quando, última
  verificação, latência, falhas consecutivas, próxima tentativa, contagem de
  reconexões nas últimas 24 h, último erro (classe, código, mensagem).

## 7. Bandeja (`features/tray`)

- Uma por sessão, iniciada pelo HKLM `Run` em todo login.
- Cliente com reconexão automática (backoff até 10 s). Serviço ausente → ícone
  cinza "serviço parado".
- Ícone: pior estado entre as VPNs ativas (verde, âmbar, vermelho, cinza).
  Tooltip resume as VPNs.
- Menu:

```
● 2 de 3 VPNs conectadas
───────────────
▸ Matriz      ● Conectada        → Conectada há 3h · ping 12ms · 2 reconexões hoje
                                    Verificar agora · Reconectar agora
                                    Pausar ▸ 15 min / 1 h / até retomar · Retomar
                                    Desativar · Remover…
▸ Filial      ● Reconectando (tent. 3, próxima em 40s)
▸ Backup      ○ Pausada até 15:30
───────────────
Adicionar VPN ▸ (entradas RAS ainda não monitoradas)
Configurações…
Abrir log
Sobre / versão
Sair da bandeja
```

- "Adicionar VPN" cria a VPN com verificação `link`; o alvo de ping é definido
  em "Configurações…".
- **Configurações…:** janela walk com a lista de VPNs e, para a selecionada,
  entrada RAS, tipo de verificação, host, porta, timeouts e intervalos, ativada.
  Salvar envia `updateVpn`/`addVpn`; erros de validação do serviço aparecem
  junto ao campo. Também tem as opções globais (avisos, nível de log).
- "Abrir log" pede o trecho final do log pelo pipe e mostra numa janela
  somente leitura (o usuário não tem acesso à pasta).
- Balões: `notice` vira balão via `NotifyIcon.ShowMessage` (toast no
  Windows 10/11), respeitando `notifications`.
- "Sair da bandeja" fecha só a bandeja.
- Tempos relativos atualizados por um tique local de 1 s; o restante vem por evento.

O view-model recebe `snapshot`/`vpnState`/`notice`/estado da conexão e produz
um modelo de tela (itens de menu, rótulos, habilitados, ícone, tooltip, balões
a mostrar). Toda a lógica de apresentação, inclusive textos e truncamento por
runas, mora nele.

## 8. Serviço e CLI (`vpnmon-svc.exe`)

- Nome `VPNMonitor`, início automático, dependência `RasMan`, conta LocalSystem.
- Sinais aceitos: `stop`, `preshutdown`, `powerevent`, `sessionchange`.
- Parada: encerra supervisores (cancela discagem com `RasHangUp`), envia
  `serviceStopping`, fecha o pipe, grava estado. Prazo total de 10 s.
  **Não derruba VPNs conectadas.**
- CLI (exige elevação, exceto `version`):

```
vpnmon-svc run                      modo console, para depurar
vpnmon-svc status                   estado de cada VPN (via pipe)
vpnmon-svc check <vpn>              verificação única, sem discar
vpnmon-svc install | uninstall      registro manual do serviço (sem MSI)
vpnmon-svc credential set|clear|list
vpnmon-svc config validate [arquivo]
vpnmon-svc version
```

## 9. Instalador (WiX v5)

- Por máquina, x64, Windows 10 1809+/11, Server 2019+.
- `C:\Program Files\VPN Monitor\` com os dois exes, licença e README.
- Serviço via `ServiceInstall`/`ServiceControl`; recuperação via
  `util:ServiceConfig` (reiniciar após 5 s, 30 s, 60 s; zerar em 1 dia);
  Event Log via `util:EventSource`; ACL da ProgramData via `util:PermissionEx`.
- Bandeja no HKLM `Run`; na instalação interativa, opção "abrir agora".
- Propriedades do seed (seção 5.3). Nenhuma custom action, nenhuma senha no MSI.
- `MajorUpgrade` com UpgradeCode fixo; downgrade bloqueado; ProgramData
  preservada no upgrade.
- Desinstalação mantém a ProgramData; `PURGE=1` remove.
- `scripts/deploy-exemplo.ps1`: instalação silenciosa + `credential set`.

## 10. CI/CD (GitHub Actions)

### 10.1 `ci.yml` (push e PR)

| Job | Runner | Conteúdo |
|---|---|---|
| lint | ubuntu | `gofmt`, `go mod tidy` sem diff, `go vet` (linux e `GOOS=windows`), `golangci-lint` com `.golangci.yml` |
| security | ubuntu | `govulncheck`; CodeQL Go (também semanal) |
| test-linux | ubuntu | `go test -race -shuffle=on`; cobertura ≥ 80% em `core/*`, `features/*/domain`, `features/*/service`, `features/tray/viewmodel`; resumo no job |
| test-windows | windows | `go test -race ./...` incluindo adaptadores reais |
| build | windows | exes com `go-winres` (versão, manifest, ícone) + MSI sem assinatura; artefatos |
| e2e | windows | roteiro da seção 10.3 |

Concorrência por ramo com cancelamento; `permissions` mínimas; actions fixadas
por SHA.

### 10.2 `release.yml` (tag `v*`)

1. Valida semver e que o commit está no `main`; roda o CI como pré-requisito.
2. Build reproduzível (`-trimpath`, `-buildid=`, `SOURCE_DATE_EPOCH`), versão,
   commit e data via `-ldflags`.
3. Passo `sign` (exes e depois MSI) em environment `release`, executado só com
   `vars.SIGNING_ENABLED == 'true'`; chama `scripts/sign.ps1`, entregue como
   esqueleto documentado com exemplo de `signtool /dlib` para HSM.
4. `SHA256SUMS`, SBOM CycloneDX (syft), `actions/attest-build-provenance`.
5. Notas por `git-cliff` (commits convencionais, `cliff.toml`).
6. GitHub Release com MSI, zip portátil, somas, SBOM e notas. Tags `-rc.N` saem
   como pré-release. Sem assinatura, as notas dizem "binários não assinados".

### 10.3 Roteiro e2e (Windows)

1. Instala o MSI com `/qn VPN_ENTRY=… CHECK_HOST=…`.
2. Confere serviço `Running`, ACL da ProgramData e config gerada pelo seed.
3. Cria entrada com `Add-VpnConnection -AllUserConnection` apontando para
   servidor inalcançável; adiciona pela CLI/pipe.
4. Confere `Reconectando` → erro transitório → backoff crescente.
5. Grava credencial pelo `credential set --password-stdin` e confere
   `credential list`.
6. Para o serviço; confere parada ≤ 10 s.
7. Instala uma versão maior por cima; confere config preservada.
8. Desinstala; confere ProgramData preservada. Reinstala e desinstala com
   `PURGE=1`; confere remoção.

### 10.4 Higiene

- Dependabot (`gomod`, `github-actions`), semanal.
- `Makefile`: `lint`, `test`, `build` (cross-compile), `cover`; espelha o CI.
- README com proteção de branch recomendada e passo a passo de release.

## 11. Testes

1. **Unidade (Linux, `-race`):**
   - Tabela de transições de `domain` cobrindo cada estado × entrada.
   - Supervisor com relógio e plataforma falsos: N falhas → hangup + discagem;
     691 para; backoff com jitter nos limites; despertares; pausa vence ciclo;
     `reconnect` em pausa recusado; panic recuperado; parada durante discagem
     → hangup; um aviso por queda; `SemRede` não gasta backoff; adoção de
     conexão existente.
   - Orquestrador: recarga parcial; config inválida mantém a anterior.
   - Config: validação, padrões, BOM, campos desconhecidos, gravação atômica
     com falha simulada, seed.
   - IPC: ida e volta, limites, cliente lento, versão incompatível, JSON
     malformado; fuzz do decodificador.
   - View-model: snapshot/eventos → menu, ícone, tooltip, balões; perda de
     conexão → "serviço parado"; truncamento por runas.
   - Layout das estruturas RAS e tabela de erros.
2. **Integração (Windows, CI):** ICMP, DPAPI, ACL do pipe (cliente sem
   permissão é recusado), SCM, ACL de pasta, pbk real.
3. **E2E (Windows, CI):** seção 10.3.
4. **Opcional com VPN real:** `VPNMON_REAL_ENTRY=<entrada>` habilita teste de
   discagem e hangup reais.
5. **Manual antes de release:** `docs/TESTE-MANUAL.md` (derrubar túnel, tirar
   cabo, suspender/retomar, senha errada, duas VPNs, RDP com dois usuários,
   upgrade com VPN de pé).

## 12. Tratamento de falhas

- Erros sempre classificados (transitório, credencial, configuração) e com
  contexto (VPN, operação, código, mensagem em português).
- Nada bloqueia sem prazo; toda chamada externa recebe `context`.
- Degradar em vez de cair: config inválida → mantém anterior; supervisor com
  problema → os outros seguem; sem bandeja → avisos só no log.
- Escritas atômicas; ACLs verificadas a cada início; estado corrompido isolado.
- Nenhum segredo em log, erro ou protocolo.

## 13. Problemas da v1 e como a v2 os trata

| # | Problema da v1 | Na v2 |
|---|---|---|
| 1 | "Balão" era só tooltip | Balão real via walk (§7) |
| 2 | Aviso repetido a cada tentativa | Um aviso por queda (§4.8) |
| 3 | Detalhe do menu congelado | Eventos + tique local (§7) |
| 4 | SIGINT/SIGTERM não fechava a bandeja | Ciclo de vida pelo SCM; bandeja separada (§8) |
| 5 | Estado salvo forçava rasdial e perdia credenciais | Só RAS; config única, sem sobreposição silenciosa (§5) |
| 6 | Autostart sem argumentos; erro de início invisível | Serviço + seed; erros no Event Log (§5.3, §5.6) |
| 7 | Menu de autostart desfeito no início | Autostart é do MSI, não da config |
| 8 | Corridas entre pausa, ciclo e reconexão forçada | Supervisor ator (§4.1, §4.7) |
| 9 | Timeout ignorado na discagem | Discagem assíncrona com hangup (§4.4) |
| 10 | Conector `service` quebrado em PT-BR | Conector removido |
| 11 | Checker `interface` comparava só o nome | Checker removido; `link` usa o RAS |
| 12 | Truncamento por bytes | Truncamento por runas no view-model |
| 13 | Layout das structs RAS incerto | Montagem byte a byte com testes de layout |
| — | Duas instâncias possíveis | SCM + mutex por sessão |
| — | Senha visível no `-set-password` | Leitura sem eco ou `--password-stdin` |
| — | `redact` não usado, código morto | Tipo `Secret`; código morto removido |
| — | README citava script inexistente | README reescrito |
| — | Corrida em `perfilAtual` | Sem estado compartilhado na UI |
| — | Backoff com base acima do teto | Backoff limitado e testado |
