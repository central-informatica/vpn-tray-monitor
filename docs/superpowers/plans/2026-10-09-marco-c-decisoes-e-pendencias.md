# Marco C — decisões tomadas na execução e pendências

Registro gerado do ledger da execução por subagentes (plano: `2026-10-09-marco-c-distribuicao.md`).

## Validação

- CI verde no último push da leva final (run 37866047523 em b194967), incluindo lint, security (govulncheck), CodeQL, test-linux com piso de cobertura, test-windows, build reproduzível, msi (WiX + `wix msi validate`) e e2e do MSI no Windows.
- O `release.yml` não foi exercitado: só uma tag real prova (ver pendências).
- O `deploy-exemplo.ps1` passou por lint (PSScriptAnalyzer), mas nunca foi executado.

## Decisões (Rulings)

- Fabricante "Central Informática" provisório: o usuário confirmou manter por enquanto; fica num único `define` do WiX mais o `CompanyName` dos `winres.json`.
- Perfil de cobertura sem instruções passava com 100%: `covergate` falha com `Total==0`, valida `mode` e `-min`, ignora `*_windows.go` por filtro explícito. Motivo: um gate que não falha com lista vazia não protege nada.
- `EnsurePolicy` (serviço): reaplica recuperação e preshutdown a cada partida e só grava quando diverge, com direitos mínimos no SCM e `errors.Join` para tentar todas as gravações. Motivo: o MSI não tem custom action própria, então a política precisa se autocorrigir.
- MSI sem custom action: sem validação ICE o risco era silencioso, então `build-msi.ps1` roda `wix msi validate` após o build; `ErrorControl` normal também no install da CLI; Restart Manager desligado (`MSIRESTARTMANAGERCONTROL=Disable`), o que devolve 3010 com a bandeja aberta.
- `VPNMON_DATADIR` fica pública e sem `Secure`: o WiX exige propriedade pública para `RegistrySearch` (WIX0012) e `SetProperty` seria custom action. Só administrador consegue passá-la e ele já pode apagar qualquer pasta. Um teste reprova se virar `Secure`.
- Log da bandeja em `%LOCALAPPDATA%\VPNMonitor`, aberto em append com `FILE_SHARE_READ|WRITE|DELETE` no Windows. Motivo: duas sessões do mesmo usuário impediam a rotação (rename) e o log crescia sem limite; vale também para o log do serviço. Pasta derivada de `config.DataDirName`; aviso repetido da bandeja com limite.
- `status --json` para scripts; o texto usa a `Message` real da config enviada pelo serviço (antes duplicava/mentia) e há testes de contrato do `--json`.
- Passo 6 do e2e (parada com discagem em curso) reescrito para eliminar falsos positivos: exige tentativa N, `now >= nextAttempt+2 s`, linha "discando" no log após T e T+3 s, ausência de conexão ativa depois da parada, e backoff estrito `d1<d2<d3` com `d3-d1 >= 6` sem folga.
- Release por tag com assinatura plugável: job `validate` com `shell: bash` (pipefail, senão o `tee` mascara tag inválida), jobs de metadados e SBOM só leitura, `persist-credentials:false`, atestação cobrindo SBOM e somas, hash do MSI e do zip conferido no publish, thumbprint esperado conferido com `Get-AuthenticodeSignature` (não com `signtool verify /sha1`). O release nunca usa `-dev`.
- Instalador: `PermissionEx` aplica só a DACL na ProgramData, sem trocar o dono (ver correção do O:BA abaixo).
- Docs de implantação: `deploy-exemplo.ps1` usa `ProgramW6432` (Intune roda em 32 bits), sem senha em GPO (alerta de SYSVOL no README), exit 3010 tratado, valida `CheckPort` (1–65535) e `Interval` (5–3600), valores do seed entre aspas.
- Atalho anunciado pode disparar reparo; `AllowSameVersionUpgrades=no` (rebuild da mesma versão instala lado a lado, usar versões distintas); PURGE não remove `VPNMonitor.naoconfiavel-*`. Tudo no README.

## Correção do O:BA na revisão final

A revisão final (opus) achou 1 Important: o `O:BA` no `PermissionEx` do MSI anulava a quarentena da §5.1, pois uma pasta pré-criada por usuário virava propriedade de BA (Administradores) depois da instalação, apagando o sinal de que ela não era confiável.

Correção: o MSI aplica só `D:` (DACL). Na instalação limpa o dono da pasta é SYSTEM (dono padrão do token do msiexec) e o serviço não o reescreve. Quando existe pasta pré-criada, o serviço a coloca em quarentena (`VPNMonitor.naoconfiavel-*`) e cria a nova, cujo dono é BA. O e2e passou a esperar o dono por caso (SY na instalação limpa, BA após quarentena) e o passo 9 prova a quarentena. Spec §9/§10.1 atualizados.

## Achados menores adiados

**CI e supply chain**
- CodeQL em PR de fork falha o upload; push em `feat/**` mais PR duplica execuções; `govulncheck` não cobre `tools/winres`.
- `persist-credentials:false` ainda falta em alguns checkouts; `-dev` do msiversion aceita qualquer texto (restringir a `[0-9A-Za-z._-]+`); `go run` do msiversion achata códigos 1/2.
- `GOOS=windows` sem `GOARCH=amd64` no `lint-go`; exclusões amplas no golangci (`_test.go` no errcheck, `^QF`).
- `govulncheck` só roda via `go install` com versão fixa no workflow (sem alvo no Makefile).

**Build e recursos**
- `SEMVER` vazio não detectado no winres (usar `$(error)`); repro na mesma árvore; `date -d` é GNU; `LegalCopyright` vazio.
- `covergate`: `-h` devolve 2, resumo sem escape de Markdown, falha ao gravar resumo derruba o gate.

**Instalador e política**
- `--allow-downgrade` exige SDK .NET 8 (citar no `.DESCRIPTION`).
- Dupla consulta de `FAILURE_ACTIONS` no `EnsurePolicy`; leitura falha = regrava; código de retorno do `te.run()` no teste de dataDir.

**e2e**
- Desconexão após a parada não distingue `RasHangUp` do serviço de limpeza do RasMan.
- O MSI fica instalado se o roteiro falhar (runner efêmero).
- Atalho, origem do Event Log e HKLM não são conferidos após desinstalar.

**Log e bandeja**
- `w.size` desatualizado após a outra sessão rotacionar (gira cedo, sem perda); limitador só cobre os 2 Warn do tique; nível do log da bandeja fixo; fallback para descarte silencioso.
- `rasBusy` barra o pedido do snapshot (lista RAS velha por até 60 s em janela rara; herdado do Marco B).

**Revisão por tema da CLI (status)**
- CLI `check` imprime `RTT.Milliseconds` (0 ms) enquanto o `status` usa ceil (1 ms); comentário no `Apply` sobre perda intencional de aviso pendente ao cair a conexão (Marco B, parte ainda aberta).

## Pendências que dependem de pessoas

1. **Assinatura pelo HSM:** fica com o time. O `release.yml` e o `sign.ps1` são plugáveis (`SIGNING_ENABLED`, `SIGN_EXPECTED_SHA1`); falta ligar o HSM real.
2. **Fabricante provisório:** "Central Informática" no `define` do WiX e nos `winres.json`; trocar pelo nome oficial quando definido.
3. **Environment `release` e proteção de tags `v*`:** criar o environment `release` restrito a tags `v*` e a regra de proteção de tags `v*` no GitHub.
4. **Primeira tag de teste:** empurrar uma tag de teste para provar o `release.yml` de ponta a ponta (hoje só validado por actionlint e revisão).
5. **Exit 3010 com a bandeja aberta:** comportamento esperado com Restart Manager desligado, mas a confirmar no roteiro manual (`docs/TESTE-MANUAL.md`, item "A confirmar").
6. **`deploy-exemplo.ps1`:** nunca executado no Windows PowerShell 5.1; validar na primeira implantação real (GPO/Intune).
7. **Corrida do PURGE=1:** com o serviço gravando, a `RemoveFolderEx` pode deixar a pasta; documentada no README (parar o serviço antes do PURGE).
8. **Aviso do `ubuntu-latest`:** migra para o Ubuntu 26 em 2026-10-19; observar o CI depois dessa data (ferramentas e versões do Go/WiX/pwsh).

## Armadilhas do Windows achadas pelo CI

- **govulncheck com GOOS:** `GOOS=windows go run ...govulncheck` compila a própria ferramenta para Windows e falha no Linux (`exec format error`). Usar `go install` (binário do host) e rodar com `GOOS=windows` só para a análise.
- **WIX0367:** componente com vários arquivos sem versão e Guid automático é recusado. Um arquivo por componente, como KeyPath; teste `TestOneFilePerComponent`. Só reproduz no bind (Windows).
- **Pipe busy:** `TestWindowsPipeDACL` falhava de forma intermitente com "All pipe instances are busy", porque `ListenPipe` só aceita cliente depois que a goroutine de Accept roda. O teste repete a consulta em `ERROR_PIPE_BUSY` (10 ms, até 5 s); o código de produção já repete via `DialPipeContext`.
- **`qpreshutdown` inexistente:** `sc.exe` não tem esse comando (erro 1639); o preshutdown é conferido só pelo registro.
- **`nextAttemptUnix` zerado antes do dial:** a view zera o campo antes do `StartDial`, então o e2e não pode usá-lo para detectar discagem em curso; confirmar pela linha "discando" no log e pelo número da tentativa.
- **Corrida RemoveFolderEx × serviço:** com `PURGE=1` logo após a partida, o serviço grava `state.json` entre a listagem da `RemoveFolderEx` e o `StopServices`, deixando a pasta. O e2e para o serviço antes; README documenta.
- **Running informado antes do EnsureDir:** o serviço reporta Running antes de criar a pasta de dados e de concluir a quarentena; contar logo após Running dava resultado errado. O passo 9 espera a quarentena e a pasta nova.
