# Marco C — Distribuição: Plano de Implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Distribuir o VPN Monitor como MSI por máquina (WiX v5, implantação silenciosa por GPO/Intune), com CI completo (lint, segurança, testes, build reproduzível, MSI e e2e do MSI num runner Windows) e release no GitHub a cada tag `v*` (assinatura plugável, somas, SBOM, proveniência e notas).

**Architecture:** O MSI só instala arquivos, registra o serviço (`ServiceInstall`/`ServiceControl`), grava registro (seed, `Run` da bandeja, origem do Event Log) e aplica a ACL da pasta de dados (`PermissionEx`); nenhuma custom action própria — a única CA é a `util:RemoveFolderEx` do WiX, só com `PURGE=1`. A política do SCM (recuperação e preshutdown) passa a ser reaplicada pelo próprio serviço a cada partida, porque as tabelas nativas do Windows Installer para isso não funcionam. Um teste Go (`installer/installer_test.go`) amarra o `.wxs` às constantes do código. Exes compilados no Linux (reproduzíveis, `go-winres` fixado num módulo de ferramenta), MSI e e2e no Windows; a regra de versão da §10.2 é uma função Go pura (`tools/msiversion`) usada por Makefile e workflows.

**Tech Stack:** Go 1.26 (`go 1.26.5`), WiX Toolset 5.0.2 (`dotnet tool`, com `WixToolset.Util.wixext` 5.0.2), go-winres v0.3.3 (diretiva `tool` em `tools/winres/go.mod`), golangci-lint v2.14.0, govulncheck v1.8.0, actionlint v1.7.12, PowerShell 7 + PSScriptAnalyzer 1.24.0, git-cliff v2 (via `orhun/git-cliff-action` v4.9.1), syft (via `anchore/sbom-action` v0.24.3), GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-10-07-vpn-monitor-v2-design.md` — leia §9 (instalador), §10 inteiro (CI/CD, regra de versão §10.2, roteiro e2e §10.3), §13 item 3, e §5.1/§5.3/§8 (o que o MSI configura). Decisões e pendências anteriores: `docs/superpowers/plans/2026-10-07-marco-a-decisoes-e-pendencias.md` e `2026-10-08-marco-b-decisoes-e-pendencias.md`. Modelo de formato: `docs/superpowers/plans/2026-10-08-marco-b-bandeja.md`.

## Global Constraints

- Go `1.26.5` no `go.mod`; build de produção `GOOS=windows GOARCH=amd64 CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w -buildid= -X main.version=… -X main.commit=… -X main.date=…"`; a bandeja com `-H windowsgui`. Só x64.
- **Nenhuma dependência nova no `go.mod` principal.** Ferramentas: go-winres só via `tools/winres/go.mod` (`go tool -modfile=tools/winres/go.mod go-winres`); govulncheck e actionlint por `go run …@versão` (verificado pelo sumdb); golangci-lint pela action fixada.
- MSI (§9): WiX **v5** (5.0.2), por máquina (`Scope="perMachine"`), x64, Windows 10 1809+/11/Server 2019+; `C:\Program Files\VPN Monitor\` com os dois exes, licença e README; serviço `VPNMonitor`, automático, dependência `RasMan`, `LocalSystem`; pasta `C:\ProgramData\VPNMonitor\` com `Sddl="O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"` (= `acl.DirSDDL`); `util:EventSource` (só registro); bandeja no `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Run`; atalho no menu Iniciar; MSI não abre a bandeja.
- **Nenhuma custom action própria.** A única CA é a `util:RemoveFolderEx` (condicionada a `PURGE=1`, desinstalação explícita, nunca em upgrade). Nada de `util:ServiceConfig`, `util:CloseApplication`, `CustomAction`, `SetProperty`, `Binary`.
- Seed (§5.3): propriedades `VPN_ENTRY`, `VPN_NAME`, `CHECK_KIND`, `CHECK_HOST`, `CHECK_PORT`, `INTERVAL`, todas `Secure="yes"`, gravadas em `HKLM\SOFTWARE\VPNMonitor\Seed` (= `config.SeedRegistryPath`, nomes = `config.SeedValueNames()`). **Nenhuma senha no MSI.**
- `MajorUpgrade` com `UpgradeCode="3A0A8DBB-61E5-4653-AFE7-63C4D3EF3ACF"` (fixo para sempre); downgrade bloqueado; ProgramData preservada no upgrade e na desinstalação (só `PURGE=1` remove).
- Política do SCM (§9, notas do Marco A): reiniciar após 5 s/30 s/60 s, zerar em 1 dia, **também em falha sem crash** (`FailureActionsOnNonCrashFailures`), preshutdown **15 s** — iguais no `vpnmon-svc install` e no MSI.
- Versão (§10.2): `vX.Y.Z` → `X.Y.(Z×100+99)`; `vX.Y.Z-rc.N` → `X.Y.(Z×100+N)`; limites X ≤ 255, Y ≤ 255, Z ≤ 654, 1 ≤ N ≤ 98; qualquer outro sufixo é recusado. Semver completa nos recursos do exe (texto), no nome dos arquivos e em `version`; versão numérica do exe = `ProductVersion.0`.
- CI (§10.1): actions fixadas por SHA (lista exata na Task 7), `permissions: contents: read` no topo e só o necessário por job, `concurrency` com cancelamento; jobs `lint`, `security`, `codeql`, `test-linux`, `test-windows`, `build`, `msi`, `e2e`.
- Release (§10.2): tag em commit do `main`; CI completo antes; build reproduzível; `sign` só com `vars.SIGNING_ENABLED == 'true'`, exes antes do MSI, via `scripts/sign.ps1`; `SHA256SUMS`, SBOM CycloneDX, `attest-build-provenance`, notas por git-cliff; `-rc.N` = pré-release; sem assinatura → "binários não assinados".
- Scripts PowerShell em UTF-8 **com BOM** (o Windows PowerShell 5.1 lê sem BOM como ANSI e estraga acentos), sem avisos do PSScriptAnalyzer 1.24.0.
- Textos ao usuário em português; commits convencionais em pt-BR; nenhum segredo em log, protocolo, MSI ou workflow.

## Review Focus

Entradas e condições que o spec implica, que nenhum teste "natural" pegaria e que mais provavelmente atingiriam quem instala ou opera — cada uma já tem teste na tarefa dona:

1. **Upgrade (ou desinstalação sem `PURGE`) apagando a ProgramData** — config, cofre e logs somem e a VPN para de discar após atualizar. A CA do `RemoveFolderEx` também roda na remoção da versão antiga durante o `MajorUpgrade`: a condição precisa de `NOT UPGRADINGPRODUCTCODE`. `TestPurgeOnlyOnExplicitUninstall` (Task 6) e passos 7–8 do e2e (Task 10: hash de `config.json` e do cofre iguais após upgrade e após desinstalar).
2. **Instalação pelo MSI sem a política do SCM** — a tabela `MsiServiceConfigFailureActions` "is not working as expected" (Microsoft) e o WiX avisa (WIX1149): um serviço que cai nunca voltaria. A política vem do serviço, a cada partida. `TestServiceMainEnsuresSCMPolicy`, `TestPolicyValues`, `TestWindowsEnsurePolicyRepairs` (Task 5) e `Assert-ServicePolicy` no e2e (Task 10).
3. **ACL do MSI diferente da que o serviço exige** — o serviço poria a pasta recém-instalada em quarentena (`VPNMonitor.naoconfiavel-*`) e subiria vazio, sem o seed. `TestDataFolderACLMatchesService` (Task 6) e `Assert-DataAcl` no e2e (Task 10).
4. **Nome de propriedade do seed divergente** entre MSI e serviço (ex.: `INTERVALO`) — a implantação por GPO/Intune "funciona" mas gera config vazia ou sem o intervalo, em silêncio. `TestSeedMatchesService` (Task 6) e `TestSeedValueNames` (Task 6).
5. **Tag fora do formato ou acima dos limites** (`v1.0.0-beta.1`, `-rc.99`, `Z=655`) — o MSI sairia com uma `ProductVersion` que não atualiza a anterior (ou estoura 65535). `TestFromTagRejects` e `TestFromTagOrdersForMajorUpgrade` (Task 1); o `validate` da release (Task 11) usa a mesma função.

## Mapa de arquivos

```
tools/msiversion/        version.go (FromTag), main.go (CLI chave=valor), testes
tools/covergate/         cover.go (Parse, Markdown), main.go (piso de cobertura), testes
tools/winres/            go.mod, go.sum (módulo só de ferramenta: go-winres v0.3.3)
.golangci.yml            golangci-lint v2 (standard + exclusões justificadas)
cmd/vpnmon-svc/          winres/winres.json + winres_test.go (recursos do exe); cli.go (ensurePolicy, status --json);
                         app.go (EnsurePolicy na partida); pipecmds.go (status --json)
cmd/vpnmon-tray/         main.go/main_windows.go (log em %LOCALAPPDATA%\VPNMonitor); winres.json (CompanyName)
internal/core/platform/svc/  svc.go (política), svc_windows.go (applyPolicy, EnsurePolicy), svc_other.go
internal/core/config/    seed.go (SeedRegistryPath, SeedValueNames), seed_windows.go
installer/               Product.wxs, LICENCA.txt, installer_test.go
scripts/                 build-msi.ps1, sign.ps1, deploy-exemplo.ps1, lint.ps1, PSScriptAnalyzerSettings.psd1,
                         e2e/lib.ps1, e2e/run.ps1
.github/                 workflows/ci.yml (completo), workflows/release.yml, dependabot.yml
cliff.toml, Makefile, .gitignore, README.md, docs/TESTE-MANUAL.md
```

## Decisões tomadas neste plano (não estavam no spec)

- **Política do SCM aplicada pelo próprio serviço, não pelo MSI.** O spec pede `ServiceConfigFailureActions` nativo, mas a Microsoft documenta na `MsiConfigureServices` que a `MsiServiceConfigFailureActions` "is not working as expected" (e sugere custom action com `sc.exe`); o WiX 5 emite WIX1149 para `ServiceConfigFailureActions` **e** para `ServiceConfig` (que gravaria `FailureActionsWhen` e `PreShutdownDelay`); há relato de erro 1939 (acesso negado com ação de reinício) que derrubaria a instalação. A menor solução sem custom action: o serviço chama `svc.EnsurePolicy()` a cada partida (mesmo `applyPolicy` do `vpnmon-svc install`: 5/30/60 s, reset 1 dia, falha sem crash, preshutdown 15 s); falha vira aviso no Event Log. O MSI fica só com `ServiceInstall`/`ServiceControl`, e o teste do instalador proíbe os elementos quebrados. Custo: um ajuste manual de recuperação feito por um admin é desfeito na próxima partida (documentado no README).
- **go-winres num módulo só de ferramenta** (`tools/winres/go.mod` com `tool github.com/tc-hib/go-winres`, Go 1.24+): versão e hashes fixados num lugar só (Makefile e CI chamam `go tool -modfile=tools/winres/go.mod go-winres`), sem levar `golang.org/x/image` v0.12.0 e `urfave/cli` ao `go.mod` principal (nem ao govulncheck dos binários). O Dependabot olha os dois módulos.
- **Exes compilados no Linux** (job `build`, ubuntu, com `make repro` provando a reprodutibilidade) e **MSI no Windows** (job `msi`); o spec punha o build no Windows. O Makefile é o mesmo do desenvolvimento e a release reaproveita o artefato `binarios` do CI chamado por `workflow_call`.
- **`vpnmon-svc status --json`**: o e2e precisa ler estado, tentativa, próxima tentativa e classe do erro sem depender do texto da tabela.
- **Cobertura:** `tools/covergate` aplica o piso de 80 % ao **total** dos pacotes da §10.1 (hoje 92,6 %), com tabela por pacote no resumo do job; `logging` (71,7 %), `platform/icmp` (68 %) e `platform/svc` (74,2 %) ficam abaixo individualmente (pendência registrada). Fakes ficam fora. O piso próprio do view-model (Marco B) continua.
- **golangci-lint v2** com o conjunto `standard` e exclusões justificadas (preset `std-error-handling`, `errcheck` em testes, liberações de handle do Windows, sugestões `QF*`, `walk.MsgBox` deprecado); os 7 achados reais restantes são corrigidos (ver Task 3), incluindo a remoção de `domain.isDown` (código morto).
- **Log da bandeja em arquivo** (pendência do Marco B): `%LOCALAPPDATA%\VPNMonitor\vpnmon-tray.log`, rotativo 1 MB × 3, com a mesma redação de segredos do serviço; sem a pasta, descarta como hoje. "Logs de diagnóstico" viram uma tabela no README (onde está cada log e comando); um `vpnmon-svc diag` que empacote tudo fica como pendência.
- **Identidade do MSI:** `Manufacturer="Central Informática"`, `Language="1046"` (pt-BR), `Codepage="1252"`, licença proprietária de uso interno (`installer/LICENCA.txt`), sem UI própria do WiX (só a básica do msiexec), `ARPNOMODIFY`.
- **Requisito de SO** por `Launch` com o `CurrentBuildNumber` do registro (≥ 17763) e `VersionNT64` — o `VersionNT` do MSI é congelado em 603.
- **`RemoveFolderEx` lê o caminho de `HKLM\SOFTWARE\VPNMonitor\DataDir`** (a CA roda antes de as pastas resolverem), e a condição é `PURGE=1 AND REMOVE~="ALL" AND NOT UPGRADINGPRODUCTCODE`.
- **Atalho anunciado** (`Advertise="yes"`) no menu Iniciar: um atalho comum exigiria chave em HKCU (ICE38/ICE43) num pacote por máquina. Chave do componente do seed = `HKLM\SOFTWARE\VPNMonitor\Version` (os valores do seed podem vir vazios).
- **e2e:** entradas SSTP para 192.0.2.1; os dois MSIs (0.0.199/0.0.299) usam os mesmos exes e o upgrade é conferido pela `DisplayVersion`; o roteiro também confere downgrade recusado, reinstalação sobre a ProgramData existente, Event Log, `Run` e atalho. Logs (msiexec, serviço, Event Log, SCM) sempre no artefato `e2e-logs`.
- **CI semanal** (`schedule`) e reutilizável (`workflow_call` com `version`); **CodeQL roda no Windows** (build manual), para analisar os `*_windows.go`.
- **Assinatura:** `vars.SIGNING_ENABLED` (variável de repositório) lida no job `package` (environment `release`) e repassada ao `publish`; o `sign.ps1` lê `SIGN_DLIB`, `SIGN_DLIB_METADATA` e `SIGN_TIMESTAMP_URL` (vars do environment). SBOM gerado dos exes do zip portátil.
- **Lint de scripts e workflows no CI:** PSScriptAnalyzer (exige BOM) e actionlint.

## Como verificar cada tarefa

Cada tarefa traz o teste ou a checagem, o comando que deve falhar antes e passar depois, e o commit. Ao fim de cada tarefa: `make lint-go` limpo e `go test ./...` verde; a partir da Task 3, `make lint` (precisa do golangci-lint v2.14.0 no PATH ou `GOLANGCI=<caminho>`).

O conteúdo deste plano já foi executado numa cópia do repositório (Linux, Go 1.26.5): testes Go falham antes e passam depois (com `-race`); `gofmt`, `go vet` e golangci-lint (linux e windows) limpos; `make repro` reproduzível; `govulncheck` sem vulnerabilidade alcançável; `actionlint` limpo nos dois workflows; PSScriptAnalyzer limpo em todos os scripts (pwsh 7.6); `git-cliff` gerou as notas com o `cliff.toml`; e o `Product.wxs` passou por **compilação e link do WiX 5.0.2** (no Linux o WiX para no bind, que exige `msi.dll` — ou seja, schema, atributos e referências estão certos). **Só o CI Windows prova:** o bind e a validação ICE do MSI, a instalação real (ACL com dono, Event Log, atalho anunciado, condição de SO, seed com valores vazios, `RemoveFolderEx`, upgrade/downgrade), `EnsurePolicy` no SCM real, o RAS do runner no e2e, o CodeQL no Windows e o `release.yml` inteiro (só passou no actionlint). Por isso a Task 7 (CI com `build` e `msi`) vem antes do e2e: **depois da Task 7, faça push e espere o job `msi` verde antes de seguir para a Task 10.**

---

### Task 1: Regra de versão do MSI (`tools/msiversion`)

**Files:**
- Create: `tools/msiversion/version.go`
- Create: `tools/msiversion/version_test.go`
- Create: `tools/msiversion/main.go`
- Create: `tools/msiversion/main_test.go`

**Interfaces:**
- Consumes: nada.
- Produces: `func FromTag(tag string) (Version, error)` com `type Version struct { Tag, Semver, Product, FileVer string; Prerelease bool }` (pacote `main`); CLI `go run ./tools/msiversion [-dev] [-field tag|semver|product|filever|prerelease] <tag>` — sem `-field`, imprime `tag=…`, `semver=…`, `product=…`, `filever=…`, `prerelease=true|false` (uma por linha, pronto para `$GITHUB_OUTPUT`); com `-dev`, entrada que não é tag vira `semver=<entrada sem "v">` (ou `dev`), `product=0.0.0`, `filever=0.0.0.0`, `prerelease=true`. Sai 1 com tag inválida (sem `-dev`), 2 com uso errado.

- [ ] **Step 1: Escrever os testes que falham**

`tools/msiversion/version_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestFromTag(t *testing.T) {
	cases := []struct {
		tag, semver, product string
		pre                  bool
	}{
		{"v0.0.1", "0.0.1", "0.0.199", false},
		{"v0.0.2", "0.0.2", "0.0.299", false},
		{"v2.1.0-rc.1", "2.1.0-rc.1", "2.1.1", true},
		{"v2.1.0-rc.98", "2.1.0-rc.98", "2.1.98", true},
		{"v2.1.0", "2.1.0", "2.1.99", false},
		{"v2.1.3", "2.1.3", "2.1.399", false},
		{"v255.255.654", "255.255.654", "255.255.65499", false},
		{"v255.255.654-rc.98", "255.255.654-rc.98", "255.255.65498", true},
	}
	for _, c := range cases {
		v, err := FromTag(c.tag)
		if err != nil {
			t.Fatalf("%s: %v", c.tag, err)
		}
		if v.Tag != c.tag || v.Semver != c.semver || v.Product != c.product || v.FileVer != c.product+".0" || v.Prerelease != c.pre {
			t.Errorf("%s: %+v", c.tag, v)
		}
	}
}

// Uma rc é sempre menor que a final da mesma versão, e a final menor que
// a rc da versão seguinte: é o que o MajorUpgrade (3 campos) compara.
func TestFromTagOrdersForMajorUpgrade(t *testing.T) {
	seq := []string{"v1.0.0-rc.1", "v1.0.0-rc.2", "v1.0.0", "v1.0.1-rc.1", "v1.0.1", "v1.1.0-rc.1", "v1.1.0", "v2.0.0"}
	var prev [3]int
	for i, tag := range seq {
		v, err := FromTag(tag)
		if err != nil {
			t.Fatal(err)
		}
		var cur [3]int
		for j, p := range strings.Split(v.Product, ".") {
			for _, r := range p {
				cur[j] = cur[j]*10 + int(r-'0')
			}
		}
		if i > 0 && !(cur[0] > prev[0] || cur[0] == prev[0] && (cur[1] > prev[1] || cur[1] == prev[1] && cur[2] > prev[2])) {
			t.Errorf("%s (%s) não é maior que o anterior %v", tag, v.Product, prev)
		}
		prev = cur
	}
}

func TestFromTagRejects(t *testing.T) {
	for _, tag := range []string{
		"", "2.1.0", "v2.1", "v2.1.0.0", "v02.1.0", "v2.01.0", "v2.1.00",
		"v2.1.0-rc.0", "v2.1.0-rc.99", "v2.1.0-rc.01", "v2.1.0-beta.1", "v2.1.0-rc1", "v2.1.0+build",
		"v256.0.0", "v0.256.0", "v0.0.655", "v99999999999999999999.0.0", " v2.1.0", "v2.1.0\n",
	} {
		if v, err := FromTag(tag); err == nil {
			t.Errorf("%q aceita: %+v", tag, v)
		}
	}
}
```

`tools/msiversion/main_test.go`:

```go
package main

import (
	"bytes"
	"testing"
)

func TestRun(t *testing.T) {
	cases := []struct {
		args       []string
		code       int
		out, inErr string
	}{
		{[]string{"v2.1.0-rc.1"}, 0, "tag=v2.1.0-rc.1\nsemver=2.1.0-rc.1\nproduct=2.1.1\nfilever=2.1.1.0\nprerelease=true\n", ""},
		{[]string{"-field", "product", "v2.1.0"}, 0, "2.1.99\n", ""},
		{[]string{"-field", "semver", "v2.1.0"}, 0, "2.1.0\n", ""},
		{[]string{"v2.1.0-beta.1"}, 1, "", "fora do formato"},
		{[]string{"-dev", "-field", "product", "v2.1.0-3-gabc1234"}, 0, "0.0.0\n", ""},
		{[]string{"-dev", "-field", "semver", "abc1234-dirty"}, 0, "abc1234-dirty\n", ""},
		{[]string{"-dev", "-field", "semver", ""}, 0, "dev\n", ""},
		{[]string{"-dev", "-field", "product", "v1.2.3"}, 0, "1.2.399\n", ""},
		{[]string{"-field", "nada", "v1.2.3"}, 2, "", "campo desconhecido"},
		{[]string{}, 2, "", "uso:"},
		{[]string{"v1.0.0", "v2.0.0"}, 2, "", "uso:"},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		code := run(c.args, &out, &errb)
		if code != c.code || out.String() != c.out || !bytes.Contains(errb.Bytes(), []byte(c.inErr)) {
			t.Errorf("%q: código %d, saída %q, erro %q", c.args, code, out.String(), errb.String())
		}
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./tools/msiversion/`
Expected: FAIL de compilação — `undefined: FromTag` e `undefined: run`.

- [ ] **Step 3: Implementar**

`tools/msiversion/version.go`:

```go
package main

import (
	"fmt"
	"regexp"
	"strconv"
)

// Version é a tag de release convertida para cada uso (§10.2).
type Version struct {
	Tag        string // a tag como veio: v2.1.0-rc.1
	Semver     string // sem o "v": 2.1.0-rc.1 (nome dos arquivos, recursos do exe)
	Product    string // ProductVersion do MSI: X.Y.(Z×100+N) ou X.Y.(Z×100+99)
	FileVer    string // versão numérica dos recursos do exe: Product + ".0"
	Prerelease bool   // tag -rc.N
}

// Limites da §10.2: o MSI guarda maior e menor em 8 bits e o build em 16
// (≤ 65535); Z×100+99 ≤ 65535 dá Z ≤ 654, e N vai de 1 a 98 (99 é a final).
const (
	maxMajor = 255
	maxMinor = 255
	maxPatch = 654
	maxRC    = 98
)

// Só vX.Y.Z e vX.Y.Z-rc.N, sem zeros à esquerda; qualquer outro sufixo
// (-beta, +build) é recusado para a conversão nunca ser ambígua.
var tagRe = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-rc\.([1-9][0-9]*))?$`)

// FromTag converte a tag de release; erro se fora do formato ou dos limites.
func FromTag(tag string) (Version, error) {
	m := tagRe.FindStringSubmatch(tag)
	if m == nil {
		return Version{}, fmt.Errorf("tag %q fora do formato vX.Y.Z ou vX.Y.Z-rc.N", tag)
	}
	num := func(s string, max int, name string) (int, error) {
		n, err := strconv.Atoi(s)
		if err != nil || n > max {
			return 0, fmt.Errorf("tag %q: %s %s acima do limite %d", tag, name, s, max)
		}
		return n, nil
	}
	x, err := num(m[1], maxMajor, "X")
	if err != nil {
		return Version{}, err
	}
	y, err := num(m[2], maxMinor, "Y")
	if err != nil {
		return Version{}, err
	}
	z, err := num(m[3], maxPatch, "Z")
	if err != nil {
		return Version{}, err
	}
	build, pre := z*100+99, false
	if m[4] != "" {
		n, err := num(m[4], maxRC, "N")
		if err != nil {
			return Version{}, err
		}
		build, pre = z*100+n, true
	}
	product := fmt.Sprintf("%d.%d.%d", x, y, build)
	return Version{Tag: tag, Semver: tag[1:], Product: product, FileVer: product + ".0", Prerelease: pre}, nil
}
```

`tools/msiversion/main.go`:

```go
// Comando msiversion converte a tag de release nas versões que o build usa
// (§10.2): a semver completa (nome dos arquivos, recursos do exe, `version`),
// a ProductVersion do MSI e a versão numérica dos recursos do exe.
//
// Uso:
//
//	go run ./tools/msiversion v2.1.0-rc.1            # chave=valor, para $GITHUB_OUTPUT
//	go run ./tools/msiversion -field product v2.1.0  # só um campo (Makefile)
//	go run ./tools/msiversion -dev "$(git describe)" # fora de tag: build de desenvolvimento
//
// Com -dev, uma entrada que não é tag de release vira versão de
// desenvolvimento (product 0.0.0) em vez de erro; uma tag válida segue a
// conversão normal.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// devVersion é a versão de um build fora de tag: a ProductVersion 0.0.0 é
// menor que qualquer release, então um MSI de desenvolvimento nunca impede
// instalar uma release por cima.
func devVersion(describe string) Version {
	s := strings.TrimPrefix(strings.TrimSpace(describe), "v")
	if s == "" {
		s = "dev"
	}
	return Version{Tag: describe, Semver: s, Product: "0.0.0", FileVer: "0.0.0.0", Prerelease: true}
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("msiversion", flag.ContinueOnError)
	fs.SetOutput(stderr)
	field := fs.String("field", "", "imprime só este campo (tag, semver, product, filever, prerelease)")
	dev := fs.Bool("dev", false, "fora de tag, devolve versão de desenvolvimento em vez de erro")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "uso: msiversion [-dev] [-field nome] <tag>")
		return 2
	}
	v, err := FromTag(fs.Arg(0))
	if err != nil {
		if !*dev {
			fmt.Fprintln(stderr, "erro:", err)
			return 1
		}
		v = devVersion(fs.Arg(0))
	}
	fields := []struct{ k, v string }{
		{"tag", v.Tag}, {"semver", v.Semver}, {"product", v.Product},
		{"filever", v.FileVer}, {"prerelease", fmt.Sprint(v.Prerelease)},
	}
	for _, f := range fields {
		switch {
		case *field == "":
			fmt.Fprintf(stdout, "%s=%s\n", f.k, f.v)
		case *field == f.k:
			fmt.Fprintln(stdout, f.v)
			return 0
		}
	}
	if *field != "" {
		fmt.Fprintf(stderr, "campo desconhecido %q\n", *field)
		return 2
	}
	return 0
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `go test -race ./tools/msiversion/ && go vet ./tools/... && go run ./tools/msiversion v2.1.0-rc.1`
Expected: `ok`; a última linha imprime `tag=v2.1.0-rc.1`, `semver=2.1.0-rc.1`, `product=2.1.1`, `filever=2.1.1.0`, `prerelease=true`.

- [ ] **Step 5: Commit**

```bash
git add tools/msiversion
git commit -m "feat(build): regra de versão do MSI da tag de release (§10.2)"
```

---

### Task 2: Piso de cobertura com resumo (`tools/covergate`)

**Files:**
- Create: `tools/covergate/cover.go`
- Create: `tools/covergate/cover_test.go`
- Create: `tools/covergate/main.go`
- Modify: `Makefile` (alvos `cover` e `cover-tray`)

**Interfaces:**
- Consumes: perfis de `go test -coverprofile`.
- Produces: `func Parse(r io.Reader) ([]PkgCover, PkgCover, error)`, `type PkgCover struct { Pkg string; Covered, Total int }` com `Percent() float64`, `func Markdown(pkgs []PkgCover, total PkgCover, module string, min float64) string`; CLI `go run ./tools/covergate -min 80 -profile coverage.out [-summary arquivo]` (sai 1 abaixo do mínimo); `make cover` (piso de 80 % no total de `internal/core/...` sem os fakes, `features/monitor/domain`, `features/monitor/service`, `features/tray/viewmodel`; acrescenta a tabela a `$GITHUB_STEP_SUMMARY` quando definido) e `make cover-tray` (view-model ≥ 80 %).

- [ ] **Step 1: Escrever o teste que falha**

`tools/covergate/cover_test.go`:

```go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `mode: set
github.com/guibsu/vpn-tray-monitor/internal/a/a.go:1.1,2.2 3 1
github.com/guibsu/vpn-tray-monitor/internal/a/a.go:3.1,4.2 1 0
github.com/guibsu/vpn-tray-monitor/internal/b/b.go:1.1,2.2 4 0
github.com/guibsu/vpn-tray-monitor/internal/b/b.go:1.1,2.2 4 2
github.com/guibsu/vpn-tray-monitor/internal/b/c.go:1.1,2.2 2 0
`

func TestParse(t *testing.T) {
	pkgs, total, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	// a: 3 de 4; b: bloco repetido conta uma vez e coberto (4) + 0 de 2 = 4 de 6.
	want := []PkgCover{
		{"github.com/guibsu/vpn-tray-monitor/internal/a", 3, 4},
		{"github.com/guibsu/vpn-tray-monitor/internal/b", 4, 6},
	}
	if len(pkgs) != 2 || pkgs[0] != want[0] || pkgs[1] != want[1] {
		t.Fatalf("%+v", pkgs)
	}
	if total.Covered != 7 || total.Total != 10 || total.Percent() != 70 {
		t.Fatalf("total %+v", total)
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{
		"",
		"github.com/x/a.go:1.1,2.2 1 1\n",
		"mode: set\nlixo\n",
		"mode: set\na.go:1.1,2.2 x 1\n",
		"mode: set\na.go:1.1,2.2 1 -1\n",
	} {
		if _, _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%q aceito", in)
		}
	}
}

func TestPercentWithoutStatements(t *testing.T) {
	if p := (PkgCover{}).Percent(); p != 100 {
		t.Fatal(p)
	}
}

func TestRunGate(t *testing.T) {
	dir := t.TempDir()
	prof := filepath.Join(dir, "c.out")
	if err := os.WriteFile(prof, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := filepath.Join(dir, "summary.md")
	var out, errb bytes.Buffer
	if code := run([]string{"-min", "70", "-profile", prof, "-summary", sum}, &out, &errb); code != 0 {
		t.Fatalf("70 %%: %d %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "| `internal/a` | 75.0 % |") || !strings.Contains(out.String(), "**70.0 %**") {
		t.Fatalf("tabela: %q", out.String())
	}
	if b, err := os.ReadFile(sum); err != nil || !strings.Contains(string(b), "internal/b") {
		t.Fatalf("resumo: %q %v", b, err)
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"-min", "80", "-profile", prof}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "abaixo do mínimo") {
		t.Fatalf("80 %%: %d %q", code, errb.String())
	}
	if code := run([]string{"-profile", filepath.Join(dir, "nada")}, &out, &errb); code != 1 {
		t.Fatalf("perfil ausente: %d", code)
	}
	if code := run([]string{"extra"}, &out, &errb); code != 2 {
		t.Fatalf("argumento extra: %d", code)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./tools/covergate/`
Expected: FAIL de compilação — `undefined: Parse`, `undefined: PkgCover`, `undefined: run`.

- [ ] **Step 3: Implementar**

`tools/covergate/cover.go`:

```go
package main

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

// PkgCover é a cobertura de um pacote (instruções cobertas / total).
type PkgCover struct {
	Pkg            string
	Covered, Total int
}

// Percent devolve a porcentagem (100 sem instruções).
func (p PkgCover) Percent() float64 {
	if p.Total == 0 {
		return 100
	}
	return 100 * float64(p.Covered) / float64(p.Total)
}

// Parse lê um perfil de `go test -coverprofile` e soma por pacote. Um bloco
// repetido (o mesmo trecho medido por mais de um binário de teste) conta uma
// vez, coberto se algum o cobriu.
func Parse(r io.Reader) ([]PkgCover, PkgCover, error) {
	type block struct {
		stmts   int
		covered bool
	}
	blocks := map[string]block{}
	sc := bufio.NewScanner(r)
	first := true
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if first {
			first = false
			if !strings.HasPrefix(line, "mode: ") {
				return nil, PkgCover{}, fmt.Errorf("linha 1: esperava \"mode: …\", veio %q", line)
			}
			continue
		}
		if line == "" {
			continue
		}
		// arquivo.go:1.2,3.4 stmts count
		f := strings.Fields(line)
		if len(f) != 3 || !strings.Contains(f[0], ":") {
			return nil, PkgCover{}, fmt.Errorf("linha %d malformada: %q", n, line)
		}
		stmts, err1 := strconv.Atoi(f[1])
		count, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || stmts < 0 || count < 0 {
			return nil, PkgCover{}, fmt.Errorf("linha %d malformada: %q", n, line)
		}
		b := blocks[f[0]]
		b.stmts = stmts
		b.covered = b.covered || count > 0
		blocks[f[0]] = b
	}
	if err := sc.Err(); err != nil {
		return nil, PkgCover{}, err
	}
	if first {
		return nil, PkgCover{}, fmt.Errorf("perfil vazio")
	}
	byPkg := map[string]*PkgCover{}
	total := PkgCover{Pkg: "total"}
	for key, b := range blocks {
		file := key[:strings.LastIndex(key, ":")]
		pkg := path.Dir(file)
		p := byPkg[pkg]
		if p == nil {
			p = &PkgCover{Pkg: pkg}
			byPkg[pkg] = p
		}
		p.Total += b.stmts
		total.Total += b.stmts
		if b.covered {
			p.Covered += b.stmts
			total.Covered += b.stmts
		}
	}
	out := make([]PkgCover, 0, len(byPkg))
	for _, p := range byPkg {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pkg < out[j].Pkg })
	return out, total, nil
}

// Markdown monta a tabela do resumo do job, com o prefixo do módulo cortado.
func Markdown(pkgs []PkgCover, total PkgCover, module string, min float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Cobertura (mínimo %.0f %% no total)\n\n| Pacote | Cobertura |\n|---|---:|\n", min)
	for _, p := range pkgs {
		name := strings.TrimPrefix(strings.TrimPrefix(p.Pkg, module), "/")
		if name == "" {
			name = "."
		}
		fmt.Fprintf(&b, "| `%s` | %.1f %% |\n", name, p.Percent())
	}
	fmt.Fprintf(&b, "| **total** | **%.1f %%** |\n", total.Percent())
	return b.String()
}
```

`tools/covergate/main.go`:

```go
// Comando covergate aplica o piso de cobertura do CI (§10.1) a um perfil de
// `go test -coverprofile` e imprime a tabela por pacote em Markdown (para o
// resumo do job). Sai com 1 se o total ficar abaixo do mínimo.
//
// Uso: go run ./tools/covergate -min 80 -profile coverage.out [-summary arquivo]
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

const module = "github.com/guibsu/vpn-tray-monitor"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("covergate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	minPct := fs.Float64("min", 80, "cobertura mínima do total, em %")
	profile := fs.String("profile", "coverage.out", "perfil de cobertura")
	summary := fs.String("summary", "", "acrescenta a tabela a este arquivo (ex.: $GITHUB_STEP_SUMMARY)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "uso: covergate [-min 80] [-profile coverage.out] [-summary arquivo]")
		return 2
	}
	f, err := os.Open(*profile)
	if err != nil {
		fmt.Fprintln(stderr, "erro:", err)
		return 1
	}
	defer f.Close()
	pkgs, total, err := Parse(f)
	if err != nil {
		fmt.Fprintf(stderr, "erro: %s: %v\n", *profile, err)
		return 1
	}
	table := Markdown(pkgs, total, module, *minPct)
	fmt.Fprint(stdout, table)
	if *summary != "" {
		s, err := os.OpenFile(*summary, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintln(stderr, "erro:", err)
			return 1
		}
		_, werr := io.WriteString(s, table)
		if cerr := s.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			fmt.Fprintln(stderr, "erro:", werr)
			return 1
		}
	}
	if total.Percent() < *minPct {
		fmt.Fprintf(stderr, "cobertura total %.1f %% abaixo do mínimo de %.0f %%\n", total.Percent(), *minPct)
		return 1
	}
	return 0
}
```

No `Makefile`, troque os alvos `cover` e `cover-tray` inteiros:

```make
cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

# O view-model da bandeja tem piso de 80 % (§10.1).
cover-tray:
	go test -coverprofile=coverage-tray.out ./internal/features/tray/viewmodel/
	@go tool cover -func=coverage-tray.out | awk '/^total:/ { sub("%", "", $$3); \
		if ($$3 + 0 < 80) { print "cobertura do viewmodel: " $$3 "% (mínimo 80%)"; exit 1 } \
		print "cobertura do viewmodel: " $$3 "%" }'
```

por

```make
# Piso de 80 % no total de core/*, features/*/domain, features/*/service e
# tray/viewmodel (§10.1); os fakes ficam de fora e os *_windows.go nem
# compilam no Linux (são cobertos pelo job Windows).
cover:
	go test -coverprofile=coverage.out $$(go list $(COVER_PKGS) | grep -v /platform/fake)
	go run ./tools/covergate -min 80 -profile coverage.out $(if $(GITHUB_STEP_SUMMARY),-summary "$(GITHUB_STEP_SUMMARY)")

# O view-model da bandeja tem piso próprio de 80 % (Marco B).
cover-tray:
	go test -coverprofile=coverage-tray.out ./internal/features/tray/viewmodel/
	go run ./tools/covergate -min 80 -profile coverage-tray.out
```

e acrescente, logo abaixo da linha `WINRES  := …`:

```make
COVER_PKGS := ./internal/core/... ./internal/features/monitor/domain ./internal/features/monitor/service ./internal/features/tray/viewmodel
```

- [ ] **Step 4: Rodar e ver passar**

Run: `go test -race ./tools/covergate/ && make cover && make cover-tray`
Expected: `ok`; `make cover` termina com a linha `| **total** | **92.6 %** |` (±0,5) e sai 0; `make cover-tray` com `98.8 %`.

- [ ] **Step 5: Commit**

```bash
git add tools/covergate Makefile
git commit -m "build: piso de cobertura do núcleo com resumo por pacote (tools/covergate)"
```

---

### Task 3: golangci-lint e correção dos achados

**Files:**
- Create: `.golangci.yml`
- Modify: `internal/core/ipc/client.go:48`
- Modify: `internal/features/tray/client/session.go:69`
- Modify: `internal/core/platform/icmp/icmp_windows.go:39`
- Modify: `internal/features/tray/view/tray_windows.go:90`
- Modify: `internal/core/ipc/server_test.go:188`
- Modify: `internal/features/monitor/domain/state.go:29-37` (remove `isDown`)
- Rename: `internal/core/platform/ras/errors_helper_test.go` → `internal/core/platform/ras/errors_helper_windows_test.go`
- Modify: `Makefile` (alvos `lint`, `lint-go`, `lint-golangci`, `lint-workflows`)

**Interfaces:**
- Consumes: golangci-lint v2.14.0 instalado (`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`, ou o binário da release).
- Produces: `make lint` = `lint-go` (gofmt, `go mod tidy -diff`, `go vet` linux e windows) + `lint-golangci` (golangci-lint linux e `GOOS=windows`); `make lint-workflows` (actionlint v1.7.12). O CI (Task 7) chama `lint-go` e a action do golangci-lint separadamente.

- [ ] **Step 1: Escrever a configuração (o "teste")**

`.golangci.yml`:

```yaml
# golangci-lint v2 (o CI fixa a versão). Roda no Linux e com GOOS=windows:
# make lint e o job lint do CI fazem as duas passadas.
version: "2"
linters:
  default: standard # errcheck, govet, ineffassign, staticcheck, unused
  settings:
    errcheck:
      # Liberações de recurso sem caminho de recuperação: o erro não muda
      # nada para quem chama (o handle já está perdido de qualquer jeito).
      exclude-functions:
        - golang.org/x/sys/windows.CloseHandle
        - golang.org/x/sys/windows.CloseServiceHandle
        - golang.org/x/sys/windows.LocalFree
        - golang.org/x/sys/windows.MessageBox
        - (*golang.org/x/sys/windows/svc/mgr.Mgr).Disconnect
  exclusions:
    presets:
      - std-error-handling # Close, Flush, fmt.Fprint* e afins
    rules:
      # Nos testes, um erro ignorado aparece como falha da asserção seguinte.
      - path: _test\.go
        linters: [errcheck]
      # Sugestões de estilo (De Morgan etc.), não defeitos.
      - linters: [staticcheck]
        text: "^QF"
      # walk.MsgBox é a caixa de mensagem síncrona que a bandeja quer; o
      # TaskDialog sugerido não acrescenta nada aqui.
      - linters: [staticcheck]
        text: "SA1019: github.com/tailscale/walk.MsgBox"
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `golangci-lint run ./... ; GOOS=windows golangci-lint run ./...`
Expected: FAIL com exatamente estes achados (somando as duas passadas):

```text
internal/core/ipc/client.go:48:26: Error return value of `c.conn.SetDeadline` is not checked (errcheck)
internal/features/tray/client/session.go:69:26: Error return value of `s.conn.SetDeadline` is not checked (errcheck)
internal/core/platform/icmp/icmp_windows.go:39:32: Error return value of `procIcmpCloseHandle.Call` is not checked (errcheck)
internal/features/tray/view/tray_windows.go:90:13: Error return value of `ni.Dispose` is not checked (errcheck)
internal/core/ipc/server_test.go:188:2: SA4006: this value of c is never used (staticcheck)
internal/core/platform/ras/errors_helper_test.go:5:6: func asError is unused (unused)
internal/features/monitor/domain/state.go:31:6: func isDown is unused (unused)
```

- [ ] **Step 3: Corrigir**

`internal/core/ipc/client.go` — troque `defer c.conn.SetDeadline(time.Time{})` por:

```go
	defer func() { _ = c.conn.SetDeadline(time.Time{}) }()
```

`internal/features/tray/client/session.go` — troque `defer s.conn.SetDeadline(time.Time{})` por:

```go
	defer func() { _ = s.conn.SetDeadline(time.Time{}) }()
```

`internal/core/platform/icmp/icmp_windows.go` — troque `defer procIcmpCloseHandle.Call(h)` por:

```go
	defer func() { _, _, _ = procIcmpCloseHandle.Call(h) }()
```

`internal/features/tray/view/tray_windows.go` — no `defer` logo após `t := &Tray{…}`, troque `ni.Dispose()` por:

```go
		_ = ni.Dispose()
```

`internal/core/ipc/server_test.go` — troque `c, r = rawConn(t, addr) // não manda nada: prazo do handshake` por:

```go
	_, r = rawConn(t, addr) // não manda nada: prazo do handshake
```

`internal/features/monitor/domain/state.go` — apague a função morta (e o comentário dela) inteira:

```go
// isDown diz se o estado conta como "fora do ar" para o aviso de queda.
// Degradada não conta: uma perda de ping isolada não é queda.
func isDown(s State) bool {
	switch s {
	case Reconectando, Desconectada, CredencialInvalida, ErroConfig, SemRede:
		return true
	}
	return false
}
```

O auxiliar `asError` só é usado pelo teste Windows do RAS:

```bash
git mv internal/core/platform/ras/errors_helper_test.go internal/core/platform/ras/errors_helper_windows_test.go
```

No `Makefile`, troque o alvo `lint` inteiro por:

```make
lint: lint-go lint-golangci

lint-go:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt pendente:"; echo "$$out"; exit 1; fi
	go mod tidy -diff
	go vet ./...
	GOOS=windows go vet ./...

# Linux e Windows: boa parte do código só compila com GOOS=windows.
lint-golangci:
	$(GOLANGCI) run ./...
	GOOS=windows $(GOLANGCI) run ./...

lint-workflows:
	go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
```

acrescente `GOLANGCI ?= golangci-lint` abaixo de `WINRES  := …` e troque a linha `.PHONY` por:

```make
.PHONY: all lint lint-go lint-golangci lint-workflows test cover cover-tray winres build clean
```

- [ ] **Step 4: Rodar e ver passar**

Run: `make lint && make lint-workflows && go test -race ./... && GOOS=windows go test -c -o /dev/null ./internal/core/platform/ras/`
Expected: `0 issues.` nas duas passadas do golangci-lint; actionlint sem saída; testes `ok`; o teste Windows do RAS compila.

- [ ] **Step 5: Commit**

```bash
git add .golangci.yml Makefile internal/
git commit -m "build: golangci-lint (linux e windows) e correção dos achados"
```

---

### Task 4: Recursos do `vpnmon-svc`, go-winres fixado e build reproduzível

**Files:**
- Create: `tools/winres/go.mod`, `tools/winres/go.sum` (gerados)
- Create: `cmd/vpnmon-svc/winres/winres.json`
- Create: `cmd/vpnmon-svc/winres_test.go`
- Modify: `cmd/vpnmon-tray/winres/winres.json` (`CompanyName`)
- Modify: `Makefile` (conteúdo final completo)

**Interfaces:**
- Consumes: `tools/msiversion` (Task 1), alvos `cover`/`lint*` (Tasks 2–3).
- Produces: `make winres` gera `cmd/vpnmon-svc/rsrc_windows_amd64.syso` e `cmd/vpnmon-tray/rsrc_windows_amd64.syso` com `--product-version <semver>` (texto = semver completa) e `--file-version <X.Y.Z'.0>`; `make build [VERSION=vX.Y.Z] [BUILD=pasta]` gera `<pasta>/vpnmon-svc.exe` e `<pasta>/vpnmon-tray.exe`; `make repro` recompila do zero em `build/repro` e compara os hashes ("build reproduzível"); `DATE` vem de `SOURCE_DATE_EPOCH` (padrão: data do último commit). O alvo `lint-scripts` já aponta para `scripts/lint.ps1`, criado na Task 6.

- [ ] **Step 1: Escrever o teste que falha**

`cmd/vpnmon-svc/winres_test.go`:

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// O vpnmon-svc é console (serviço + CLI): manifest asInvoker — a CLI confere
// a elevação sozinha e `version` roda sem ela — e versão nos recursos. O
// winres.json versionado é a fonte do .syso gerado no build.
func TestWinresManifestAndVersion(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("winres", "winres.json"))
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Icon     map[string]map[string]string `json:"RT_GROUP_ICON"`
		Manifest map[string]map[string]struct {
			ExecutionLevel string `json:"execution-level"`
			CommonControls bool   `json:"use-common-controls-v6"`
		} `json:"RT_MANIFEST"`
		Version map[string]map[string]struct {
			Info map[string]map[string]string `json:"info"`
		} `json:"RT_VERSION"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		t.Fatal(err)
	}
	if m := w.Manifest["#1"]["0409"]; m.ExecutionLevel != "as invoker" || m.CommonControls {
		t.Fatalf("manifest: %+v", m)
	}
	info := w.Version["#1"]["0000"].Info["0409"]
	if info["OriginalFilename"] != "vpnmon-svc.exe" || info["ProductName"] != "VPN Monitor" {
		t.Fatalf("versão: %+v", info)
	}
	icon := w.Icon["APP"]["0000"]
	if _, err := os.Stat(filepath.Join("winres", icon)); err != nil {
		t.Fatalf("ícone do exe: %v", err)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./cmd/vpnmon-svc/ -run Winres`
Expected: FAIL — `open winres/winres.json: no such file or directory`.

- [ ] **Step 3: Implementar**

Módulo só de ferramenta (gera `go.sum` com os hashes):

```bash
mkdir -p tools/winres
cat > tools/winres/go.mod <<'EOF'
// Módulo só de ferramenta: fixa o go-winres (versão aqui, hashes em go.sum)
// fora do go.mod principal, para as dependências dele (golang.org/x/image
// antigo, urfave/cli) não entrarem no grafo dos binários. Uso, da raiz:
//
//	go tool -modfile=tools/winres/go.mod go-winres make …
module github.com/guibsu/vpn-tray-monitor/tools/winres

go 1.26.5
EOF
(cd tools/winres && go get -tool github.com/tc-hib/go-winres@v0.3.3 && go mod tidy)
```

O `tools/winres/go.mod` resultante deve ser exatamente:

```text
// Módulo só de ferramenta: fixa o go-winres (versão aqui, hashes em go.sum)
// fora do go.mod principal, para as dependências dele (golang.org/x/image
// antigo, urfave/cli) não entrarem no grafo dos binários. Uso, da raiz:
//
//	go tool -modfile=tools/winres/go.mod go-winres make …
module github.com/guibsu/vpn-tray-monitor/tools/winres

go 1.26.5

tool github.com/tc-hib/go-winres

require (
	github.com/cpuguy83/go-md2man/v2 v2.0.2 // indirect
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/tc-hib/go-winres v0.3.3 // indirect
	github.com/tc-hib/winres v0.2.1 // indirect
	github.com/urfave/cli/v2 v2.25.7 // indirect
	github.com/xrash/smetrics v0.0.0-20201216005158-039620a65673 // indirect
	golang.org/x/image v0.12.0 // indirect
)
```

`cmd/vpnmon-svc/winres/winres.json`:

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
          "name": "VPNMonitor.Service",
          "version": ""
        },
        "description": "VPN Monitor - serviço e CLI",
        "minimum-os": "win10",
        "execution-level": "as invoker",
        "ui-access": false,
        "auto-elevate": false,
        "dpi-awareness": "system",
        "use-common-controls-v6": false
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
            "CompanyName": "Central Informática",
            "FileDescription": "VPN Monitor - serviço e CLI",
            "InternalName": "vpnmon-svc",
            "LegalCopyright": "",
            "OriginalFilename": "vpnmon-svc.exe",
            "ProductName": "VPN Monitor",
            "ProductVersion": "",
            "FileVersion": ""
          }
        }
      }
    }
  }
}
```

`cmd/vpnmon-tray/winres/winres.json` — troque `"CompanyName": "",` por:

```json
            "CompanyName": "Central Informática",
```

`Makefile` — conteúdo final completo:

```make
# Espelha o CI (.github/workflows/ci.yml). Rode `make lint test` antes de abrir PR.
# Ferramentas fora do Go: golangci-lint v2.14.0 (lint) e, para lint-scripts,
# pwsh com PSScriptAnalyzer 1.24.0. O MSI só é gerado no Windows
# (scripts/build-msi.ps1, WiX v5).
BUILD   := build
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
# Build reproduzível (§10.2): a data gravada no exe é a do último commit,
# não a do relógio; com o mesmo commit, dois builds saem byte a byte iguais.
SOURCE_DATE_EPOCH ?= $(shell git log -1 --format=%ct 2>/dev/null || echo 0)
DATE    ?= $(shell date -u -d @$(SOURCE_DATE_EPOCH) +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -buildid= -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
WIN     := GOOS=windows GOARCH=amd64 CGO_ENABLED=0
# Versões dos recursos do exe (§10.2): texto = semver completa; número =
# o mesmo X.Y.(Z×100+N) do MSI. Fora de tag, 0.0.0.0. Avaliadas só quando usadas.
MSIVER   = go run ./tools/msiversion -dev
SEMVER   = $(shell $(MSIVER) -field semver '$(VERSION)')
FILEVER  = $(shell $(MSIVER) -field filever '$(VERSION)')
# go-winres fixado por hash num módulo só de ferramenta (tools/winres/go.mod).
WINRES  := go tool -modfile=tools/winres/go.mod go-winres
GOLANGCI ?= golangci-lint
COVER_PKGS := ./internal/core/... ./internal/features/monitor/domain ./internal/features/monitor/service ./internal/features/tray/viewmodel

.PHONY: all lint lint-go lint-golangci lint-scripts lint-workflows test cover cover-tray winres build repro clean

all: lint test build

lint: lint-go lint-golangci

lint-go:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt pendente:"; echo "$$out"; exit 1; fi
	go mod tidy -diff
	cd tools/winres && go mod tidy -diff
	go vet ./...
	GOOS=windows go vet ./...

# Linux e Windows: boa parte do código só compila com GOOS=windows.
lint-golangci:
	$(GOLANGCI) run ./...
	GOOS=windows $(GOLANGCI) run ./...

# Scripts PowerShell (instalação, e2e, assinatura): erros e avisos do
# PSScriptAnalyzer reprovam.
lint-scripts:
	pwsh -NoProfile -File scripts/lint.ps1

lint-workflows:
	go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12

test:
	go test -race -shuffle=on ./...

# Piso de 80 % no total de core/*, features/*/domain, features/*/service e
# tray/viewmodel (§10.1); os fakes ficam de fora e os *_windows.go nem
# compilam no Linux (são cobertos pelo job Windows).
cover:
	go test -coverprofile=coverage.out $$(go list $(COVER_PKGS) | grep -v /platform/fake)
	go run ./tools/covergate -min 80 -profile coverage.out $(if $(GITHUB_STEP_SUMMARY),-summary "$(GITHUB_STEP_SUMMARY)")

# O view-model da bandeja tem piso próprio de 80 % (Marco B).
cover-tray:
	go test -coverprofile=coverage-tray.out ./internal/features/tray/viewmodel/
	go run ./tools/covergate -min 80 -profile coverage-tray.out

# Manifest, ícone e versão dos dois exes: gera cmd/*/rsrc_windows_amd64.syso
# (não versionados) a partir de cmd/*/winres/winres.json. O vpnmon-tray
# precisa do manifest (comctl32 v6, que o walk exige) para abrir.
winres:
	$(WINRES) make --in cmd/vpnmon-svc/winres/winres.json --out cmd/vpnmon-svc/rsrc --arch amd64 \
		--product-version $(SEMVER) --file-version $(FILEVER)
	$(WINRES) make --in cmd/vpnmon-tray/winres/winres.json --out cmd/vpnmon-tray/rsrc --arch amd64 \
		--product-version $(SEMVER) --file-version $(FILEVER)

build: winres
	@mkdir -p $(BUILD)
	$(WIN) go build -trimpath -ldflags "$(LDFLAGS)" -o $(BUILD)/vpnmon-svc.exe ./cmd/vpnmon-svc
	$(WIN) go build -trimpath -ldflags "$(LDFLAGS) -H windowsgui" -o $(BUILD)/vpnmon-tray.exe ./cmd/vpnmon-tray
	@ls -l $(BUILD)/*.exe

# Prova a reprodutibilidade: recompila tudo do zero (-a) em outra pasta e
# compara os hashes com os de $(BUILD).
repro: build
	$(MAKE) build BUILD=$(BUILD)/repro GOFLAGS=-a
	cd $(BUILD) && sha256sum vpnmon-svc.exe vpnmon-tray.exe > a.sum && \
		cd repro && sha256sum vpnmon-svc.exe vpnmon-tray.exe > ../b.sum && \
		cd .. && diff a.sum b.sum && echo "build reproduzível"

clean:
	rm -rf $(BUILD) coverage.out coverage-tray.out cmd/vpnmon-svc/*.syso cmd/vpnmon-tray/*.syso
```

- [ ] **Step 4: Rodar e ver passar**

Run:

```bash
go test -race ./cmd/... && make lint-go && make repro && \
go tool -modfile=tools/winres/go.mod go-winres extract --dir /tmp/winres-svc build/vpnmon-svc.exe && \
make build VERSION=v2.1.0-rc.1 && go tool -modfile=tools/winres/go.mod go-winres extract --dir /tmp/winres-rc build/vpnmon-svc.exe && \
grep -E '"(file_version|ProductVersion)"' /tmp/winres-rc/winres.json
```

Expected: testes `ok`; `make repro` termina com `build reproduzível`; o último `grep` mostra `"file_version": "2.1.1.0"` e `"ProductVersion": "2.1.0-rc.1"`. `go list ./...` **não** lista `tools/winres` (módulo à parte) e `go.mod` principal não muda.

- [ ] **Step 5: Commit**

```bash
git add tools/winres cmd/vpnmon-svc/winres cmd/vpnmon-svc/winres_test.go cmd/vpnmon-tray/winres/winres.json Makefile
git commit -m "build: recursos do vpnmon-svc, go-winres fixado em módulo de ferramenta e build reproduzível"
```

---

### Task 5: Política do SCM reaplicada pelo serviço (`svc.EnsurePolicy`)

**Files:**
- Modify: `internal/core/platform/svc/svc.go`
- Modify: `internal/core/platform/svc/svc_windows.go`
- Modify: `internal/core/platform/svc/svc_other.go`
- Modify: `internal/core/platform/svc/svc_test.go`
- Modify: `internal/core/platform/svc/svc_windows_test.go`
- Modify: `cmd/vpnmon-svc/cli.go`, `cmd/vpnmon-svc/cli_test.go`
- Modify: `cmd/vpnmon-svc/app.go`, `cmd/vpnmon-svc/app_test.go`

**Interfaces:**
- Consumes: `svc.Install`/`installNamed` (Marco A).
- Produces: `svc.PreshutdownTimeout = 15 * time.Second`, `svc.RecoveryReset = 24 * time.Hour`, `func svc.RecoveryDelays() []time.Duration` (5 s, 30 s, 60 s; cópia nova a cada chamada), `func svc.Dependencies() []string` (`["RasMan"]`), `func svc.EnsurePolicy() error` (Windows: reaplica ao serviço `VPNMonitor`; fora: `platform.ErrNotSupported`); `env.ensurePolicy func() error` no `vpnmon-svc`, chamado uma vez na partida como serviço (falha → `Events.Warning`). A Task 6 usa `RecoveryDelays`/`Dependencies` no teste do instalador.

- [ ] **Step 1: Escrever os testes que falham**

Acrescente ao fim de `internal/core/platform/svc/svc_test.go`:

```go
// A política do SCM é contrato com o spec (§9) e com o roteiro e2e, que a
// confere no registro depois de instalar o MSI.
func TestPolicyValues(t *testing.T) {
	want := []time.Duration{5 * time.Second, 30 * time.Second, 60 * time.Second}
	if got := RecoveryDelays(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RecoveryDelays %v", got)
	}
	if RecoveryReset != 24*time.Hour || PreshutdownTimeout != 15*time.Second {
		t.Fatalf("reset %s, preshutdown %s", RecoveryReset, PreshutdownTimeout)
	}
	if got := Dependencies(); !reflect.DeepEqual(got, []string{"RasMan"}) {
		t.Fatalf("Dependencies %v", got)
	}
	// Quem chama não altera a política de todos.
	RecoveryDelays()[0] = 0
	Dependencies()[0] = "x"
	if RecoveryDelays()[0] != 5*time.Second || Dependencies()[0] != "RasMan" {
		t.Fatal("política mutável por quem chama")
	}
}
```

Acrescente ao fim de `internal/core/platform/svc/svc_windows_test.go` (roda no job `test-windows`):

```go
// preshutdownMs lê o prazo de preshutdown gravado no SCM.
func preshutdownMs(t *testing.T, s *mgr.Service) uint32 {
	t.Helper()
	var info struct{ PreshutdownTimeout uint32 }
	var needed uint32
	if err := windows.QueryServiceConfig2(s.Handle, windows.SERVICE_CONFIG_PRESHUTDOWN_INFO,
		(*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), &needed); err != nil {
		t.Fatal(err)
	}
	return info.PreshutdownTimeout
}

// Só no job Windows (exige elevação): um serviço registrado sem a política
// (como o MSI faz) a ganha inteira com EnsurePolicy, e reaplicar não falha.
func TestWindowsEnsurePolicyRepairs(t *testing.T) {
	if !IsElevated() {
		t.Skip("exige processo elevado (o runner do CI é)")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("VPNMonitorPol%d", os.Getpid())
	m, err := mgr.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Disconnect()
	s, err := m.CreateService(name, exe, mgr.Config{DisplayName: name, StartType: mgr.StartManual})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t.Cleanup(func() { _ = s.Delete() })
	if acts, _ := s.RecoveryActions(); len(acts) != 0 {
		t.Fatalf("serviço novo já com recuperação: %v", acts)
	}
	for i := 0; i < 2; i++ {
		if err := ensurePolicyNamed(name); err != nil {
			t.Fatalf("EnsurePolicy (%d): %v", i+1, err)
		}
	}
	acts, err := s.RecoveryActions()
	if err != nil {
		t.Fatal(err)
	}
	var delays []time.Duration
	for _, a := range acts {
		if a.Type != mgr.ServiceRestart {
			t.Fatalf("ação %v, quer reiniciar", a)
		}
		delays = append(delays, a.Delay)
	}
	if !reflect.DeepEqual(delays, RecoveryDelays()) {
		t.Fatalf("esperas %v", delays)
	}
	if rp, err := s.ResetPeriod(); err != nil || rp != uint32(RecoveryReset.Seconds()) {
		t.Fatalf("ResetPeriod %d (%v)", rp, err)
	}
	if f, err := s.RecoveryActionsOnNonCrashFailures(); err != nil || !f {
		t.Fatalf("flag de falhas sem crash: %v (%v)", f, err)
	}
	if ms := preshutdownMs(t, s); ms != uint32(PreshutdownTimeout/time.Millisecond) {
		t.Fatalf("preshutdown %d ms", ms)
	}
	if err := ensurePolicyNamed("VPNMonitorInexistente"); err == nil {
		t.Fatal("serviço inexistente deveria dar erro")
	}
}
```

e, no mesmo arquivo, troque os imports `"time"` + `"golang.org/x/sys/windows/svc"` por:

```go
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
```

e, em `TestWindowsInstallStartPIDUninstall`, logo depois da checagem `RecoveryActionsOnNonCrashFailures`, acrescente:

```go
	if ms := preshutdownMs(t, s); ms != uint32(PreshutdownTimeout/time.Millisecond) {
		t.Fatalf("preshutdown %d ms", ms)
	}
```

Acrescente ao fim de `cmd/vpnmon-svc/app_test.go`:

```go
// Na partida como serviço, a política do SCM é reaplicada uma vez; se falhar,
// vira aviso no Event Log e o serviço sobe assim mesmo.
func TestServiceMainEnsuresSCMPolicy(t *testing.T) {
	te := newTestEnv(t)
	te.writeConfig(t, "Matriz")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	events := &logging.RecordingSink{}
	p := testPlatform(fake.NewRAS("VPN Matriz"), events, &fake.ACL{})
	p.Listen = func() (net.Listener, error) { return ln, nil }
	te.platform = func() (Platform, error) { return p, nil }
	te.isService = func() (bool, error) { return true, nil }
	var calls atomic.Int32
	te.ensurePolicy = func() error { calls.Add(1); return errors.New("acesso negado") }
	te.runService = func(h svc.Hooks) error {
		reqs := make(chan svc.Request)
		finished := make(chan uint32, 1)
		go func() { finished <- svc.Loop(h, reqs, func(svc.State) {}) }()
		waitFor(t, func() bool { return hasEvent(events.Snapshot(), "info", "iniciado") })
		reqs <- svc.Request{Cmd: svc.CmdStop}
		if code := <-finished; code != 0 {
			t.Errorf("laço saiu com %d", code)
		}
		return nil
	}
	if rc := te.run(); rc != 0 {
		t.Fatalf("saída %d", rc)
	}
	if calls.Load() != 1 {
		t.Fatalf("EnsurePolicy chamado %d vezes", calls.Load())
	}
	if !hasEvent(events.Snapshot(), "warning", "acesso negado") {
		t.Fatalf("Event Log: %+v", events.Snapshot())
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/core/platform/svc/ ./cmd/vpnmon-svc/`
Expected: FAIL de compilação — `undefined: RecoveryDelays`, `undefined: RecoveryReset`, `undefined: PreshutdownTimeout`, `undefined: Dependencies`; e `te.ensurePolicy undefined (type *testEnv has no field or method ensurePolicy)`.

- [ ] **Step 3: Implementar**

`internal/core/platform/svc/svc.go` — logo antes de `// Eventos de energia (pbt.h) que indicam retomada.`:

```go
// Política do serviço no SCM (§8, §9), a mesma no `vpnmon-svc install` e no
// MSI. Como a MsiServiceConfigFailureActions do Windows Installer não funciona
// (documentado pela Microsoft), o próprio serviço reaplica recuperação e
// preshutdown a cada partida (EnsurePolicy); o MSI só registra o serviço.
const (
	// PreshutdownTimeout é quanto o SCM espera o serviço no preshutdown.
	PreshutdownTimeout = 15 * time.Second
	// RecoveryReset zera a contagem de falhas após um dia sem falhar.
	RecoveryReset = 24 * time.Hour
)

// RecoveryDelays são as esperas antes de reiniciar após a 1ª, 2ª e 3ª falha.
func RecoveryDelays() []time.Duration {
	return []time.Duration{5 * time.Second, 30 * time.Second, 60 * time.Second}
}

// Dependencies são os serviços de que o VPNMonitor depende.
func Dependencies() []string { return []string{"RasMan"} }
```

`internal/core/platform/svc/svc_other.go` — acrescente, abaixo de `func Uninstall() …`:

```go
func EnsurePolicy() error         { return platform.ErrNotSupported }
```

`internal/core/platform/svc/svc_windows.go`:

1. Apague a constante antiga:

```go
// preshutdownTimeoutMs é o tempo que o SCM espera o serviço no preshutdown.
const preshutdownTimeoutMs = 15000
```

2. Em `Install`, troque `[]string{"RasMan"}` por `Dependencies()`.

3. Em `installNamed`, troque todo o trecho que vai de `actions := []mgr.RecoveryAction{` até o fim da checagem do `ChangeServiceConfig2` (inclusive), isto é:

```go
	actions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}
	if err := s.SetRecoveryActions(actions, uint32((24 * time.Hour).Seconds())); err != nil {
		return fmt.Errorf("configurando recuperação: %w", err)
	}
	// Run que termina com erro limpo vira SERVICE_STOPPED com código de saída,
	// não crash: sem este flag a recuperação não dispararia.
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return fmt.Errorf("configurando recuperação em falhas sem crash: %w", err)
	}
	info := struct{ PreshutdownTimeout uint32 }{preshutdownTimeoutMs}
	if err := windows.ChangeServiceConfig2(s.Handle, windows.SERVICE_CONFIG_PRESHUTDOWN_INFO,
		(*byte)(unsafe.Pointer(&info))); err != nil {
		return fmt.Errorf("configurando tempo de preshutdown: %w", err)
	}
```

por

```go
	if err := applyPolicy(s); err != nil {
		return err
	}
```

(o registro do Event Log que vem depois fica igual).

4. Logo antes de `// Uninstall para o serviço (sem derrubar VPNs) e remove o registro.`, acrescente:

```go
// applyPolicy grava a política do SCM: recuperação (RecoveryDelays, zerando
// em RecoveryReset), também quando o serviço para com erro sem crash, e o
// prazo de preshutdown. Idempotente.
func applyPolicy(s *mgr.Service) error {
	var actions []mgr.RecoveryAction
	for _, d := range RecoveryDelays() {
		actions = append(actions, mgr.RecoveryAction{Type: mgr.ServiceRestart, Delay: d})
	}
	if err := s.SetRecoveryActions(actions, uint32(RecoveryReset.Seconds())); err != nil {
		return fmt.Errorf("configurando recuperação: %w", err)
	}
	// Run que termina com erro limpo vira SERVICE_STOPPED com código de saída,
	// não crash: sem este flag a recuperação não dispararia.
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return fmt.Errorf("configurando recuperação em falhas sem crash: %w", err)
	}
	info := struct{ PreshutdownTimeout uint32 }{uint32(PreshutdownTimeout / time.Millisecond)}
	if err := windows.ChangeServiceConfig2(s.Handle, windows.SERVICE_CONFIG_PRESHUTDOWN_INFO,
		(*byte)(unsafe.Pointer(&info))); err != nil {
		return fmt.Errorf("configurando tempo de preshutdown: %w", err)
	}
	return nil
}

// EnsurePolicy reaplica a política do SCM ao serviço VPNMonitor. O serviço
// chama na partida: é o que garante a política numa instalação pelo MSI.
func EnsurePolicy() error { return ensurePolicyNamed(ServiceName) }

func ensurePolicyNamed(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()
	return applyPolicy(s)
}
```

`cmd/vpnmon-svc/cli.go` — no `type env struct`, depois do campo `adminOwner func() error`:

```go
	// ensurePolicy reaplica a política do SCM (recuperação e preshutdown) ao
	// serviço; chamado na partida do serviço (svc.EnsurePolicy).
	ensurePolicy func() error
```

e, em `defaultEnv`, troque `isService: svc.IsService, runService: svc.Run, now: time.Now,` por:

```go
		isService: svc.IsService, runService: svc.Run, ensurePolicy: svc.EnsurePolicy, now: time.Now,
```

`cmd/vpnmon-svc/cli_test.go` — em `newTestEnv`, acrescente ao literal `env{…}` (depois de `runService`; rode `gofmt -w` para realinhar):

```go
		ensurePolicy: func() error { return nil },
```

`cmd/vpnmon-svc/app.go` — em `serviceMain`, dentro de `Run`, entre a checagem de erro do `e.platform()` e o `return serve(…)`:

```go
			// O MSI só registra o serviço (a tabela de recuperação do
			// Windows Installer não funciona): a política vem daqui. Falha
			// é aviso — o serviço sobe do mesmo jeito.
			if err := e.ensurePolicy(); err != nil {
				p.Events.Warning("aplicando a política de recuperação e de preshutdown do serviço: " + err.Error())
			}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l . ; go test -race ./internal/core/platform/svc/ ./cmd/vpnmon-svc/ && GOOS=windows go vet ./... && GOOS=windows go test -c -o /dev/null ./internal/core/platform/svc/ && make lint`
Expected: `gofmt` sem saída; `ok` nos dois pacotes; o teste Windows compila; lint limpo. (`TestWindowsEnsurePolicyRepairs` e a checagem de preshutdown rodam no job `test-windows`.)

- [ ] **Step 5: Commit**

```bash
git add internal/core/platform/svc cmd/vpnmon-svc
git commit -m "feat(svc): serviço reaplica recuperação e preshutdown a cada partida (EnsurePolicy)"
```

---

### Task 6: MSI (`installer/Product.wxs`) amarrado ao código

**Files:**
- Modify: `internal/core/config/seed.go`, `internal/core/config/seed_windows.go`, `internal/core/config/seed_test.go`
- Create: `installer/Product.wxs`
- Create: `installer/LICENCA.txt`
- Create: `installer/installer_test.go`
- Create: `scripts/build-msi.ps1`
- Create: `scripts/lint.ps1`
- Create: `scripts/PSScriptAnalyzerSettings.psd1`

**Interfaces:**
- Consumes: `svc.ServiceName`, `svc.DisplayName`, `svc.Description`, `svc.Dependencies()` (Task 5), `acl.DirSDDL`, `logging.EventSourceName`.
- Produces: `config.SeedRegistryPath` (agora em `seed.go`, sem build tag) e `func config.SeedValueNames() []string`; `installer/Product.wxs` com as variáveis de pré-processador `ProductVersion` (X.Y.Z) e `BinDir` (pasta dos exes); `pwsh scripts/build-msi.ps1 -Semver <semver> -ProductVersion <X.Y.Z> -BinDir <pasta> -OutDir <pasta>` gera `<OutDir>/VPNMonitor-<semver>-x64.msi` (instala o WiX 5.0.2 se faltar); `make lint-scripts` (`pwsh scripts/lint.ps1`).

- [ ] **Step 1: Escrever os testes que falham**

Acrescente ao fim de `internal/core/config/seed_test.go`:

```go
// Os nomes são o contrato com o MSI (installer/Product.wxs) e com quem
// implanta por GPO/Intune: mudar um deles quebra instalações existentes.
func TestSeedValueNames(t *testing.T) {
	want := []string{"VPN_ENTRY", "VPN_NAME", "CHECK_KIND", "CHECK_HOST", "CHECK_PORT", "INTERVAL"}
	got := SeedValueNames()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%v", got)
	}
	var s Seed
	for i, v := range s.values() {
		*v.dst = want[i]
	}
	if s != (Seed{VPNEntry: "VPN_ENTRY", VPNName: "VPN_NAME", CheckKind: "CHECK_KIND", CheckHost: "CHECK_HOST", CheckPort: "CHECK_PORT", Interval: "INTERVAL"}) {
		t.Fatalf("campos trocados: %+v", s)
	}
	if SeedRegistryPath != `SOFTWARE\VPNMonitor\Seed` {
		t.Fatal(SeedRegistryPath)
	}
}
```

`installer/installer_test.go`:

```go
// Package installer guarda o MSI (Product.wxs, WiX v5). O WiX só roda no
// Windows (job msi do CI); este teste roda em qualquer lugar e amarra o .wxs
// às constantes que o código também usa, para MSI e serviço não divergirem.
package installer

import (
	"encoding/xml"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/acl"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/svc"
)

const (
	nsWix  = "http://wixtoolset.org/schemas/v4/wxs"
	nsUtil = "http://wixtoolset.org/schemas/v4/wxs/util"
	// UpgradeCode nunca muda: é o que liga todas as versões no MajorUpgrade.
	upgradeCode = "3A0A8DBB-61E5-4653-AFE7-63C4D3EF3ACF"
)

// node é um elemento XML genérico.
type node struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Children []node     `xml:",any"`
}

func (n node) attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name && a.Name.Space == "" {
			return a.Value
		}
	}
	return ""
}

// all devolve, em ordem de documento, os elementos com esse nome local.
func (n node) all(space, local string) []node {
	var out []node
	var walk func(node)
	walk = func(c node) {
		if c.XMLName.Space == space && c.XMLName.Local == local {
			out = append(out, c)
		}
		for _, k := range c.Children {
			walk(k)
		}
	}
	walk(n)
	return out
}

func (n node) one(t *testing.T, space, local string) node {
	t.Helper()
	got := n.all(space, local)
	if len(got) != 1 {
		t.Fatalf("esperava 1 <%s>, achei %d", local, len(got))
	}
	return got[0]
}

func load(t *testing.T) node {
	t.Helper()
	data, err := os.ReadFile("Product.wxs")
	if err != nil {
		t.Fatal(err)
	}
	var root node
	if err := xml.Unmarshal(data, &root); err != nil {
		t.Fatalf("Product.wxs não é XML válido: %v", err)
	}
	if root.XMLName.Space != nsWix || root.XMLName.Local != "Wix" {
		t.Fatalf("raiz %v", root.XMLName)
	}
	return root
}

func TestPackage(t *testing.T) {
	p := load(t).one(t, nsWix, "Package")
	want := map[string]string{
		"Name": "VPN Monitor", "Version": "$(var.ProductVersion)", "UpgradeCode": upgradeCode,
		"Scope": "perMachine", "InstallerVersion": "500",
	}
	for k, v := range want {
		if got := p.attr(k); got != v {
			t.Errorf("Package/@%s = %q, quer %q", k, got, v)
		}
	}
	if mu := p.one(t, nsWix, "MajorUpgrade"); mu.attr("DowngradeErrorMessage") == "" || mu.attr("AllowDowngrades") != "" {
		t.Errorf("MajorUpgrade deve bloquear downgrade: %+v", mu.Attrs)
	}
	if lc := p.one(t, nsWix, "Launch"); !strings.Contains(lc.attr("Condition"), "WINBUILD >= 17763") ||
		!strings.Contains(lc.attr("Condition"), "VersionNT64") {
		t.Errorf("condição de SO: %q", lc.attr("Condition"))
	}
}

func TestServiceMatchesCode(t *testing.T) {
	root := load(t)
	si := root.one(t, nsWix, "ServiceInstall")
	want := map[string]string{
		"Name": svc.ServiceName, "DisplayName": svc.DisplayName, "Description": svc.Description,
		"Start": "auto", "Account": "LocalSystem", "Type": "ownProcess", "Vital": "yes",
	}
	for k, v := range want {
		if got := si.attr(k); got != v {
			t.Errorf("ServiceInstall/@%s = %q, quer %q", k, got, v)
		}
	}
	var deps []string
	for _, d := range si.all(nsWix, "ServiceDependency") {
		deps = append(deps, d.attr("Id"))
	}
	if !slices.Equal(deps, svc.Dependencies()) {
		t.Errorf("dependências %v, quer %v", deps, svc.Dependencies())
	}
	sc := root.one(t, nsWix, "ServiceControl")
	for k, v := range map[string]string{"Name": svc.ServiceName, "Start": "install", "Stop": "both", "Remove": "uninstall", "Wait": "yes"} {
		if got := sc.attr(k); got != v {
			t.Errorf("ServiceControl/@%s = %q, quer %q", k, got, v)
		}
	}
	// Tabelas MsiServiceConfig*: documentadas como quebradas; a política do
	// SCM vem do serviço (svc.EnsurePolicy).
	for _, el := range []string{"ServiceConfig", "ServiceConfigFailureActions"} {
		if n := len(root.all(nsWix, el)) + len(root.all(nsUtil, el)); n != 0 {
			t.Errorf("<%s> não deve ser usado (%d)", el, n)
		}
	}
}

func TestDataFolderACLMatchesService(t *testing.T) {
	root := load(t)
	pe := root.one(t, nsWix, "PermissionEx")
	if got := pe.attr("Sddl"); got != acl.DirSDDL {
		t.Fatalf("Sddl %q, o serviço espera %q", got, acl.DirSDDL)
	}
	var dataDir node
	for _, d := range root.all(nsWix, "Directory") {
		if d.attr("Id") == "DATAFOLDER" {
			dataDir = d
		}
	}
	if dataDir.attr("Name") != "VPNMonitor" {
		t.Fatalf("pasta de dados %q", dataDir.attr("Name"))
	}
}

func TestEventSourceMatchesService(t *testing.T) {
	es := load(t).one(t, nsUtil, "EventSource")
	if es.attr("Name") != logging.EventSourceName || es.attr("Log") != "Application" {
		t.Fatalf("EventSource %+v", es.Attrs)
	}
	// Mesma DLL de mensagens do eventlog.InstallAsEventCreate do install.
	if !strings.HasSuffix(es.attr("EventMessageFile"), `\System32\EventCreate.exe`) {
		t.Fatalf("EventMessageFile %q", es.attr("EventMessageFile"))
	}
}

func TestSeedMatchesService(t *testing.T) {
	root := load(t)
	secure := map[string]bool{}
	for _, p := range root.all(nsWix, "Property") {
		if p.attr("Secure") == "yes" {
			secure[p.attr("Id")] = true
		}
	}
	var key node
	for _, k := range root.all(nsWix, "RegistryKey") {
		if k.attr("Root") == "HKLM" && k.attr("Key") == config.SeedRegistryPath {
			key = k
		}
	}
	var names []string
	for _, v := range key.all(nsWix, "RegistryValue") {
		names = append(names, v.attr("Name"))
		if v.attr("Value") != "["+v.attr("Name")+"]" || v.attr("Type") != "string" {
			t.Errorf("valor %s: Value=%q Type=%q", v.attr("Name"), v.attr("Value"), v.attr("Type"))
		}
		if !secure[v.attr("Name")] {
			t.Errorf("propriedade %s sem Secure=\"yes\"", v.attr("Name"))
		}
	}
	if !slices.Equal(names, config.SeedValueNames()) {
		t.Fatalf("valores do seed %v, o serviço lê %v", names, config.SeedValueNames())
	}
	if !secure["PURGE"] {
		t.Error("PURGE sem Secure=\"yes\"")
	}
	// Nenhuma senha no MSI.
	for _, p := range root.all(nsWix, "Property") {
		if id := strings.ToUpper(p.attr("Id")); strings.Contains(id, "PASS") || strings.Contains(id, "SENHA") {
			t.Errorf("propriedade de senha no MSI: %s", p.attr("Id"))
		}
	}
}

func TestPurgeOnlyOnExplicitUninstall(t *testing.T) {
	root := load(t)
	rf := root.one(t, nsUtil, "RemoveFolderEx")
	cond := rf.attr("Condition")
	for _, part := range []string{"PURGE=1", `REMOVE~="ALL"`, "NOT UPGRADINGPRODUCTCODE"} {
		if !strings.Contains(cond, part) {
			t.Errorf("condição do RemoveFolderEx %q sem %q", cond, part)
		}
	}
	if rf.attr("On") != "uninstall" {
		t.Errorf("RemoveFolderEx/@On %q", rf.attr("On"))
	}
	// A pasta vem do registro (a CA roda antes de as pastas resolverem).
	var search node
	for _, p := range root.all(nsWix, "Property") {
		if p.attr("Id") == rf.attr("Property") {
			search = p.one(t, nsWix, "RegistrySearch")
		}
	}
	if search.attr("Key") != `SOFTWARE\VPNMonitor` || search.attr("Name") != "DataDir" {
		t.Fatalf("RegistrySearch da pasta: %+v", search.Attrs)
	}
}

func TestTrayStartsOnLogon(t *testing.T) {
	root := load(t)
	found := false
	for _, v := range root.all(nsWix, "RegistryValue") {
		if v.attr("Root") == "HKLM" && v.attr("Key") == `SOFTWARE\Microsoft\Windows\CurrentVersion\Run` {
			found = v.attr("Value") == `"[#TrayExe]"`
		}
	}
	if !found {
		t.Fatal("bandeja fora do HKLM Run (ou caminho sem aspas)")
	}
	if sc := root.one(t, nsWix, "Shortcut"); sc.attr("Directory") != "ProgramMenuFolder" {
		t.Fatalf("atalho em %q", sc.attr("Directory"))
	}
}

// §9: nenhuma custom action própria; do util, só a origem do Event Log
// (registro) e o RemoveFolderEx (a única CA, do próprio WiX).
func TestNoCustomActions(t *testing.T) {
	root := load(t)
	for _, el := range []string{"CustomAction", "CustomActionRef", "SetProperty", "SetDirectory", "Binary", "InstallExecuteSequence", "InstallUISequence"} {
		if n := len(root.all(nsWix, el)); n != 0 {
			t.Errorf("<%s> proibido (%d)", el, n)
		}
	}
	allowed := map[string]bool{"EventSource": true, "RemoveFolderEx": true}
	var walk func(node)
	walk = func(n node) {
		if n.XMLName.Space == nsUtil && !allowed[n.XMLName.Local] {
			t.Errorf("util:%s não permitido", n.XMLName.Local)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/core/config/ ./installer/`
Expected: FAIL — no `config`, `undefined: SeedValueNames` e `undefined: SeedRegistryPath` (a constante só existe com `GOOS=windows`); no `installer`, `open Product.wxs: no such file or directory` depois de corrigido o `config`.

- [ ] **Step 3: Implementar**

`internal/core/config/seed.go` — logo antes de `// SeedReader lê o seed; found=false quando a chave não existe.`:

```go
// SeedRegistryPath é a chave (HKLM, visão de 64 bits) que o MSI grava.
const SeedRegistryPath = `SOFTWARE\VPNMonitor\Seed`

// seedValue liga o nome de um valor do registro (= propriedade do MSI) ao
// campo do Seed.
type seedValue struct {
	name string
	dst  *string
}

// values são os valores do seed na ordem da §5.3; leitor do registro e
// teste do instalador usam esta mesma lista.
func (s *Seed) values() []seedValue {
	return []seedValue{
		{"VPN_ENTRY", &s.VPNEntry},
		{"VPN_NAME", &s.VPNName},
		{"CHECK_KIND", &s.CheckKind},
		{"CHECK_HOST", &s.CheckHost},
		{"CHECK_PORT", &s.CheckPort},
		{"INTERVAL", &s.Interval},
	}
}

// SeedValueNames devolve os nomes dos valores do seed (propriedades do MSI).
func SeedValueNames() []string {
	var s Seed
	vals := s.values()
	names := make([]string, len(vals))
	for i, v := range vals {
		names[i] = v.name
	}
	return names
}
```

`internal/core/config/seed_windows.go` — apague a constante (agora em `seed.go`):

```go
// SeedRegistryPath é a chave que o MSI grava.
const SeedRegistryPath = `SOFTWARE\VPNMonitor\Seed`
```

e troque a lista local `fields := []struct{…}{…}` e o `for _, f := range fields {` por:

```go
	for _, f := range seed.values() {
```

`installer/Product.wxs`:

```xml
<?xml version="1.0" encoding="utf-8"?>
<!--
  MSI do VPN Monitor (spec §9), WiX v5. Gerado por scripts/build-msi.ps1:

    wix build installer/Product.wxs -arch x64 -ext WixToolset.Util.wixext
      -d ProductVersion=X.Y.Z -d BinDir=<pasta com os dois exes> -o <saída.msi>

  Regras: nenhuma custom action própria (a única CA é a WixRemoveFoldersEx
  do util:RemoveFolderEx, só com PURGE=1 na desinstalação); nenhuma senha
  em propriedade. Os valores que o código também conhece (nome do serviço,
  SDDL da pasta, chave do seed, origem do Event Log, prazos do SCM) são
  conferidos contra as constantes Go por installer/installer_test.go.
-->
<Wix xmlns="http://wixtoolset.org/schemas/v4/wxs"
     xmlns:util="http://wixtoolset.org/schemas/v4/wxs/util">

  <Package Name="VPN Monitor"
           Manufacturer="Central Informática"
           Version="$(var.ProductVersion)"
           UpgradeCode="3A0A8DBB-61E5-4653-AFE7-63C4D3EF3ACF"
           Scope="perMachine"
           InstallerVersion="500"
           Language="1046"
           Codepage="1252">

    <SummaryInformation Description="VPN Monitor: mantém as VPNs nativas do Windows conectadas" />

    <!-- Upgrade: a versão anterior sai inteira antes (afterInstallValidate),
         a ProgramData fica. Downgrade bloqueado. -->
    <MajorUpgrade DowngradeErrorMessage="Uma versão mais nova do VPN Monitor já está instalada. Desinstale-a antes de instalar esta." />

    <MediaTemplate EmbedCab="yes" />

    <!-- Windows 10 1809 (build 17763), Windows 11 ou Server 2019+, 64 bits.
         O VersionNT do MSI é congelado em 603 desde o 8.1; o número real do
         build vem do registro. -->
    <Property Id="WINBUILD">
      <RegistrySearch Id="WinBuild" Root="HKLM" Key="SOFTWARE\Microsoft\Windows NT\CurrentVersion"
                      Name="CurrentBuildNumber" Type="raw" Bitness="always64" />
    </Property>
    <Launch Condition="Installed OR (VersionNT64 AND WINBUILD &gt;= 17763)"
            Message="O VPN Monitor exige Windows 10 1809, Windows 11 ou Windows Server 2019 ou mais novo, de 64 bits." />

    <!-- Seed da primeira configuração (§5.3). Secure: valem em instalação
         gerenciada (GPO/Intune). Nunca há senha aqui. -->
    <Property Id="VPN_ENTRY" Secure="yes" />
    <Property Id="VPN_NAME" Secure="yes" />
    <Property Id="CHECK_KIND" Secure="yes" />
    <Property Id="CHECK_HOST" Secure="yes" />
    <Property Id="CHECK_PORT" Secure="yes" />
    <Property Id="INTERVAL" Secure="yes" />
    <!-- PURGE=1 na desinstalação apaga a ProgramData (config, estado, cofre, logs). -->
    <Property Id="PURGE" Secure="yes" />

    <!-- Caminho da pasta de dados lembrado no registro: a WixRemoveFoldersEx
         roda antes do CostFinalize, quando as pastas ainda não resolveram. -->
    <Property Id="VPNMON_DATADIR">
      <RegistrySearch Id="DataDirSearch" Root="HKLM" Key="SOFTWARE\VPNMonitor"
                      Name="DataDir" Type="raw" Bitness="always64" />
    </Property>

    <Icon Id="VPNMonitor.ico" SourceFile="$(sys.SOURCEFILEDIR)..\assets\conectada.ico" />
    <Property Id="ARPPRODUCTICON" Value="VPNMonitor.ico" />
    <Property Id="ARPNOMODIFY" Value="1" />

    <StandardDirectory Id="ProgramFiles64Folder">
      <Directory Id="INSTALLFOLDER" Name="VPN Monitor" />
    </StandardDirectory>
    <StandardDirectory Id="CommonAppDataFolder">
      <Directory Id="DATAFOLDER" Name="VPNMonitor" />
    </StandardDirectory>
    <StandardDirectory Id="ProgramMenuFolder" />

    <Feature Id="Main" Title="VPN Monitor" Level="1" AllowAbsent="no">
      <ComponentGroupRef Id="Programa" />
      <ComponentGroupRef Id="Dados" />
    </Feature>

    <ComponentGroup Id="Programa" Directory="INSTALLFOLDER">

      <!-- Serviço (§8): automático, depende do RasMan, LocalSystem. -->
      <Component Id="Servico" Bitness="always64">
        <File Id="SvcExe" Source="$(var.BinDir)\vpnmon-svc.exe" KeyPath="yes" />
        <ServiceInstall Id="Servico"
                        Name="VPNMonitor"
                        DisplayName="VPN Monitor"
                        Description="Mantém as VPNs nativas do Windows conectadas e reconecta após quedas."
                        Type="ownProcess"
                        Start="auto"
                        ErrorControl="normal"
                        Account="LocalSystem"
                        Vital="yes">
          <ServiceDependency Id="RasMan" />
          <!-- Sem ServiceConfigFailureActions/ServiceConfig nativos: a
               Microsoft documenta que a MsiServiceConfigFailureActions "is not
               working as expected" (MsiConfigureServices) e o WiX avisa
               (WIX1149). A recuperação (5 s/30 s/60 s, zera em 1 dia, também
               em falha sem crash) e o preshutdown de 15 s são aplicados pelo
               próprio serviço a cada partida (svc.EnsurePolicy), a mesma
               política do `vpnmon-svc install`. -->
        </ServiceInstall>
        <ServiceControl Id="Servico" Name="VPNMonitor" Start="install" Stop="both" Remove="uninstall" Wait="yes" />
      </Component>

      <!-- Bandeja: abre em todo login (HKLM Run); o MSI não a abre ao fim
           (abriria elevada). Atalho anunciado no menu Iniciar. -->
      <Component Id="Bandeja" Bitness="always64">
        <File Id="TrayExe" Source="$(var.BinDir)\vpnmon-tray.exe" KeyPath="yes">
          <Shortcut Id="AtalhoBandeja" Directory="ProgramMenuFolder" Name="VPN Monitor"
                    Description="Bandeja do VPN Monitor" WorkingDirectory="INSTALLFOLDER"
                    Icon="VPNMonitor.ico" Advertise="yes" />
        </File>
        <RegistryValue Root="HKLM" Key="SOFTWARE\Microsoft\Windows\CurrentVersion\Run"
                       Name="VPNMonitorTray" Type="string" Value="&quot;[#TrayExe]&quot;" />
      </Component>

      <Component Id="Documentos" Bitness="always64">
        <File Id="Licenca" Source="$(sys.SOURCEFILEDIR)LICENCA.txt" KeyPath="yes" />
        <File Id="Leiame" Source="$(sys.SOURCEFILEDIR)..\README.md" />
      </Component>

      <!-- Origem do Event Log (§5.6): só registro, com a mesma DLL de
           mensagens do `vpnmon-svc install` (EventCreate.exe). -->
      <Component Id="EventLog" Bitness="always64">
        <util:EventSource Log="Application" Name="VPNMonitor"
                          EventMessageFile="%SystemRoot%\System32\EventCreate.exe"
                          SupportsErrors="yes" SupportsWarnings="yes" SupportsInformationals="yes"
                          KeyPath="yes" />
      </Component>
    </ComponentGroup>

    <ComponentGroup Id="Dados">
      <!-- Pasta de dados (§5.1): dono Administradores, SYSTEM e
           Administradores com controle total, herança desligada (P). -->
      <Component Id="PastaDados" Directory="DATAFOLDER" Bitness="always64">
        <CreateFolder>
          <PermissionEx Sddl="O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)" />
        </CreateFolder>
        <RegistryValue Root="HKLM" Key="SOFTWARE\VPNMonitor" Name="DataDir" Type="string"
                       Value="[DATAFOLDER]" KeyPath="yes" />
        <util:RemoveFolderEx Id="PurgeDados" On="uninstall" Property="VPNMON_DATADIR"
                             Condition="PURGE=1 AND REMOVE~=&quot;ALL&quot; AND NOT UPGRADINGPRODUCTCODE" />
      </Component>

      <!-- Seed (§5.3): o serviço lê só no primeiro início sem config.json. -->
      <Component Id="Seed" Directory="INSTALLFOLDER" Bitness="always64">
        <!-- Chave do componente: sempre gravada (os valores do seed podem vir
             vazios). -->
        <RegistryValue Root="HKLM" Key="SOFTWARE\VPNMonitor" Name="Version" Type="string"
                       Value="[ProductVersion]" KeyPath="yes" />
        <RegistryKey Root="HKLM" Key="SOFTWARE\VPNMonitor\Seed">
          <RegistryValue Name="VPN_ENTRY" Type="string" Value="[VPN_ENTRY]" />
          <RegistryValue Name="VPN_NAME" Type="string" Value="[VPN_NAME]" />
          <RegistryValue Name="CHECK_KIND" Type="string" Value="[CHECK_KIND]" />
          <RegistryValue Name="CHECK_HOST" Type="string" Value="[CHECK_HOST]" />
          <RegistryValue Name="CHECK_PORT" Type="string" Value="[CHECK_PORT]" />
          <RegistryValue Name="INTERVAL" Type="string" Value="[INTERVAL]" />
        </RegistryKey>
      </Component>
    </ComponentGroup>
  </Package>
</Wix>
```

`installer/LICENCA.txt`:

```text
VPN Monitor
Copyright (c) 2026 Central Informática. Todos os direitos reservados.

Software de uso interno. É proibido copiar, distribuir ou modificar este
programa fora da organização sem autorização por escrito.

O programa é fornecido "como está", sem garantias de qualquer tipo.
```

`scripts/build-msi.ps1`:

```powershell
<#
.SYNOPSIS
  Gera o MSI do VPN Monitor (WiX v5) a partir dos dois exes já compilados.

.DESCRIPTION
  Usado pelo CI (job msi), pela release e à mão no Windows. Instala o WiX
  5.0.2 como ferramenta global do dotnet se ainda não houver (o WiX v5 é
  .NET 6; DOTNET_ROLL_FORWARD=Major o roda nos runtimes mais novos).

.EXAMPLE
  pwsh scripts/build-msi.ps1 -Semver 2.1.0 -ProductVersion 2.1.99 -BinDir build -OutDir dist
#>
[CmdletBinding()]
param(
    # Versão completa, só para o nome do arquivo (ex.: 2.1.0-rc.1).
    [Parameter(Mandatory)][string]$Semver,
    # ProductVersion do MSI, X.Y.Z (tools/msiversion).
    [Parameter(Mandatory)][ValidatePattern('^\d{1,3}\.\d{1,3}\.\d{1,5}$')][string]$ProductVersion,
    # Pasta com vpnmon-svc.exe e vpnmon-tray.exe.
    [Parameter(Mandatory)][string]$BinDir,
    [Parameter(Mandatory)][string]$OutDir
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$WixVersion = '5.0.2'
$env:DOTNET_ROLL_FORWARD = 'Major'

function Install-WixIfMissing {
    $wix = Get-Command wix -ErrorAction SilentlyContinue
    $current = if ($wix) { (& wix --version 2>$null | Select-Object -Last 1) } else { '' }
    if ($current -notlike "$WixVersion*") {
        Write-Host "instalando WiX $WixVersion"
        & dotnet tool install --global wix --version $WixVersion
        if ($LASTEXITCODE -ne 0) { throw "dotnet tool install wix falhou ($LASTEXITCODE)" }
        $env:PATH = "$env:USERPROFILE\.dotnet\tools;$env:PATH"
    }
    & wix extension add -g "WixToolset.Util.wixext/$WixVersion"
    if ($LASTEXITCODE -ne 0) { throw "wix extension add falhou ($LASTEXITCODE)" }
}

$repo = Split-Path -Parent $PSScriptRoot
$bin = (Resolve-Path $BinDir).Path
foreach ($exe in 'vpnmon-svc.exe', 'vpnmon-tray.exe') {
    if (-not (Test-Path (Join-Path $bin $exe))) { throw "faltando $exe em $bin" }
}
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$out = Join-Path (Resolve-Path $OutDir).Path "VPNMonitor-$Semver-x64.msi"

Install-WixIfMissing
& wix build (Join-Path $repo 'installer\Product.wxs') `
    -arch x64 `
    -ext "WixToolset.Util.wixext" `
    -d "ProductVersion=$ProductVersion" `
    -d "BinDir=$bin" `
    -o $out
if ($LASTEXITCODE -ne 0) { throw "wix build falhou ($LASTEXITCODE)" }
Write-Host "MSI: $out"
```

`scripts/lint.ps1`:

```powershell
<#
.SYNOPSIS
  Analisa os scripts PowerShell do repositório com o PSScriptAnalyzer
  (erros e avisos reprovam). Roda no Linux (pwsh) e no Windows.
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Version = '1.24.0'
if (-not (Get-Module -ListAvailable PSScriptAnalyzer | Where-Object { $_.Version -eq [version]$Version })) {
    Install-Module PSScriptAnalyzer -RequiredVersion $Version -Scope CurrentUser -Force -Repository PSGallery
}
Import-Module PSScriptAnalyzer -RequiredVersion $Version

$problems = Invoke-ScriptAnalyzer -Path $PSScriptRoot -Recurse -Severity Error, Warning `
    -Settings (Join-Path $PSScriptRoot 'PSScriptAnalyzerSettings.psd1')
$problems | Format-Table -AutoSize ScriptName, Line, Severity, RuleName, Message | Out-String -Width 200 | Write-Host
if ($problems) {
    Write-Error "$(@($problems).Count) problema(s) do PSScriptAnalyzer"
}
Write-Host 'scripts PowerShell sem problemas'
```

`scripts/PSScriptAnalyzerSettings.psd1`:

```powershell
@{
    # Os scripts são de linha de comando (CI, implantação): Write-Host é a
    # saída pretendida, e os nomes de função seguem o verbo aprovado.
    ExcludeRules = @('PSAvoidUsingWriteHost')
}
```

Os três arquivos de `scripts/` vão em UTF-8 **com BOM** (regra do PSScriptAnalyzer `PSUseBOMForUnicodeEncodedFile`; sem BOM, o Windows PowerShell 5.1 lê como ANSI). Depois de criá-los:

```bash
for f in scripts/*.ps1 scripts/*.psd1; do sed -i '1s/^\xEF\xBB\xBF//; 1s/^/\xEF\xBB\xBF/' "$f"; done
```

- [ ] **Step 4: Rodar e ver passar**

Run: `go test -race ./internal/core/config/ ./installer/ && GOOS=windows go vet ./... && make lint && xmllint --noout installer/Product.wxs && make lint-scripts`
Expected: `ok` nos dois pacotes; lint limpo; `xmllint` sem saída; `scripts PowerShell sem problemas` (exige `pwsh`; o script instala o PSScriptAnalyzer 1.24.0).

Prova de que o teste do instalador pega divergência: troque temporariamente `Name="INTERVAL"` por `Name="INTERVALO"` no `RegistryValue` e `O:BAD:P` por `D:P` no `Sddl`; `go test ./installer/` tem de falhar em `TestSeedMatchesService` e `TestDataFolderACLMatchesService`. Desfaça.

Opcional, no Linux com `dotnet` (valida schema e referências do WiX; para no bind, que exige `msi.dll`):

```bash
T=$(mktemp -d) && dotnet tool install wix --version 5.0.2 --tool-path "$T/wix" >/dev/null && \
DOTNET_ROLL_FORWARD=Major "$T/wix/wix" extension add -g WixToolset.Util.wixext/5.0.2 && make build && \
python3 - "$PWD" "$T" <<'PY'
import re, sys
r, t = sys.argv[1], sys.argv[2]
s = open(r + '/installer/Product.wxs').read()
s = s.replace('$(var.BinDir)\\', '$(var.BinDir)/').replace('$(sys.SOURCEFILEDIR)..\\assets\\', r + '/assets/')
s = s.replace('$(sys.SOURCEFILEDIR)..\\README.md', r + '/README.md').replace('$(sys.SOURCEFILEDIR)LICENCA.txt', r + '/installer/LICENCA.txt')
# No Linux o WiX recusa qualquer Directory/@Name; os componentes vão direto às pastas-padrão.
s = s.replace('<Directory Id="INSTALLFOLDER" Name="VPN Monitor" />', '').replace('<Directory Id="DATAFOLDER" Name="VPNMonitor" />', '')
s = s.replace('"INSTALLFOLDER"', '"ProgramFiles64Folder"').replace('"DATAFOLDER"', '"CommonAppDataFolder"').replace('[DATAFOLDER]', '[CommonAppDataFolder]')
open(t + '/P.wxs', 'w').write(s)
PY
(cd "$T" && DOTNET_ROLL_FORWARD=Major ./wix/wix build P.wxs -arch x64 -ext WixToolset.Util.wixext \
  -d ProductVersion=0.0.199 -d BinDir="$OLDPWD/build" -o t.msi 2>&1 | grep -E 'error|warning' | grep -v WIX0000 | head -3)
```

Expected: só `error WIX0001: System.DllNotFoundException: Unable to load shared library 'msi.dll'` (chegou ao bind); nenhum `WIX0004`/`WIX0010`/`WIX1149`.

- [ ] **Step 5: Commit**

```bash
git add internal/core/config installer scripts
git commit -m "feat(installer): MSI WiX v5 por máquina, sem custom action própria, conferido contra o código"
```

---

### Task 7: CI completo até o MSI (`ci.yml`)

**Files:**
- Modify: `.github/workflows/ci.yml` (conteúdo completo, sem o job `e2e`, que vem na Task 10)

**Interfaces:**
- Consumes: `make lint-go`, `make lint-workflows`, `make lint-scripts`, `make cover`, `make cover-tray`, `make repro` (Tasks 2–4, 6), `tools/msiversion` (Task 1), `scripts/build-msi.ps1` (Task 6).
- Produces: jobs `lint`, `security`, `codeql`, `test-linux`, `test-windows`, `build` (outputs `semver`, `product`; artefato `binarios` com `build/vpnmon-svc.exe` e `build/vpnmon-tray.exe`), `msi` (artefatos `msi` = `dist/VPNMonitor-<semver>-x64.msi` e `msi-e2e` = `e2e/VPNMonitor-0.0.1-x64.msi` [0.0.199] e `e2e/VPNMonitor-0.0.2-x64.msi` [0.0.299]); `workflow_call` com input `version` (a release passa a tag); `schedule` semanal.

Actions fixadas (SHA conferido no GitHub em 2026-10-08):

| Action | Versão | SHA |
|---|---|---|
| actions/checkout | v7.0.1 | `3d3c42e5aac5ba805825da76410c181273ba90b1` |
| actions/setup-go | v7.0.0 | `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` |
| actions/upload-artifact | v7.0.2 | `cf430e030ddbb5b0abf93d22962f4752f3646cd9` |
| actions/download-artifact | v8.0.2 | `9000827ccba6bdab643e8b6fd33ac0654aef8333` |
| golangci/golangci-lint-action | v9.3.0 | `ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a` |
| github/codeql-action (init/analyze) | v4.38.3 | `24c54180a607b1449ed407dd24f251e4e9147c8d` |
| actions/attest-build-provenance | v4.2.2 | `4d101475d8b20a2381f78447822ac1eab6504dd8` |
| anchore/sbom-action | v0.24.3 | `66cbf4bc1f1c0d2edc94016e65bc221b6bb0ad6c` |
| orhun/git-cliff-action | v4.9.1 | `a9a95522b26fe6403f7bb24031f21fb573d0f5ff` |

- [ ] **Step 1: Escrever o workflow**

`.github/workflows/ci.yml` — conteúdo completo:

```yaml
# CI completo (spec §10.1). Ordem pensada para falhar cedo: lint e testes em
# paralelo; build dos exes (Linux, reproduzível) → MSI (Windows, WiX v5) → e2e
# do MSI (Windows). Também roda toda semana no main (pega vulnerabilidade nova
# e mudança na imagem do runner) e é chamado pelo release.yml.
name: ci

on:
  push:
    branches: [main, "feat/**"]
  pull_request:
  schedule:
    - cron: "17 6 * * 1" # segunda, 06:17 UTC
  workflow_dispatch:
  workflow_call:
    inputs:
      version:
        description: "Versão gravada nos exes (a tag, na release). Vazio: git describe."
        type: string
        required: false
        default: ""

concurrency:
  group: ci-${{ github.ref }}-${{ github.event_name }}
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
      - name: gofmt, go mod tidy, go vet (linux e windows)
        run: make lint-go
      - name: golangci-lint (linux)
        uses: golangci/golangci-lint-action@ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a # v9.3.0
        with:
          version: v2.14.0
      - name: golangci-lint (windows)
        uses: golangci/golangci-lint-action@ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a # v9.3.0
        env:
          GOOS: windows
        with:
          version: v2.14.0
      - name: workflows (actionlint)
        run: make lint-workflows
      - name: scripts PowerShell (PSScriptAnalyzer)
        run: make lint-scripts

  security:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - name: govulncheck (linux e windows)
        run: |
          go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
          GOOS=windows go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...

  codeql:
    # No Windows: assim o CodeQL analisa também os arquivos *_windows.go.
    runs-on: windows-latest
    permissions:
      contents: read
      actions: read
      security-events: write
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - uses: github/codeql-action/init@24c54180a607b1449ed407dd24f251e4e9147c8d # v4.38.3
        with:
          languages: go
          build-mode: manual
      - name: build para o CodeQL
        shell: bash
        env:
          CGO_ENABLED: "0"
        run: go build ./...
      - uses: github/codeql-action/analyze@24c54180a607b1449ed407dd24f251e4e9147c8d # v4.38.3
        with:
          category: go

  test-linux:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - run: go test -race -shuffle=on ./...
      - name: cobertura (≥ 80 % no núcleo; resumo no job)
        run: make cover
      - name: cobertura do view-model da bandeja (≥ 80 %)
        run: make cover-tray

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

  build:
    # Cross-compile no Linux: o Makefile é o mesmo do desenvolvimento e a
    # reprodutibilidade (§10.2) é conferida aqui.
    runs-on: ubuntu-latest
    outputs:
      semver: ${{ steps.version.outputs.semver }}
      product: ${{ steps.version.outputs.product }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0 # git describe precisa das tags
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - name: versão
        id: version
        env:
          INPUT_VERSION: ${{ inputs.version }}
        run: |
          v="${INPUT_VERSION:-$(git describe --tags --always)}"
          echo "VERSION=$v" >> "$GITHUB_ENV"
          go run ./tools/msiversion -dev "$v" | tee -a "$GITHUB_OUTPUT"
      - name: exes (go-winres + build reproduzível, conferido duas vezes)
        run: make repro VERSION="$VERSION"
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: binarios
          path: |
            build/vpnmon-svc.exe
            build/vpnmon-tray.exe
          if-no-files-found: error

  msi:
    needs: build
    runs-on: windows-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: binarios
          path: build
      - name: MSI da versão (sem assinatura)
        shell: pwsh
        env:
          SEMVER: ${{ needs.build.outputs.semver }}
          PRODUCT: ${{ needs.build.outputs.product }}
        run: ./scripts/build-msi.ps1 -Semver $env:SEMVER -ProductVersion $env:PRODUCT -BinDir build -OutDir dist
      - name: MSIs do e2e (0.0.199 e 0.0.299, §10.2)
        shell: pwsh
        run: |
          ./scripts/build-msi.ps1 -Semver 0.0.1 -ProductVersion 0.0.199 -BinDir build -OutDir e2e
          ./scripts/build-msi.ps1 -Semver 0.0.2 -ProductVersion 0.0.299 -BinDir build -OutDir e2e
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: msi
          path: dist/*.msi
          if-no-files-found: error
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: msi-e2e
          path: e2e/*.msi
          if-no-files-found: error
          retention-days: 3
```

- [ ] **Step 2: Conferir localmente**

Run: `make lint-workflows`
Expected: sem saída (actionlint + shellcheck limpos). Antes desta tarefa, um `actionlint` sobre um workflow com `needs: build` num job inexistente, ou com `${{ inputs.version }}` sem `workflow_call`, falharia — é o teste que esta etapa faz.

- [ ] **Step 3: Commit e push; esperar o MSI**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: lint completo, govulncheck, CodeQL, cobertura, build reproduzível e MSI"
git push -u origin feat/v2-distribuicao
gh run watch --exit-status "$(gh run list --branch feat/v2-distribuicao --workflow ci --limit 1 --json databaseId --jq '.[0].databaseId')"
```

Expected: todos os jobs verdes; o artefato `msi` contém `VPNMonitor-<semver>-x64.msi`. **Não siga para a Task 10 com o job `msi` vermelho**: um erro de bind/ICE do WiX aparece aqui (log do passo "MSI da versão"). Se a conta do `gh` não enxergar o repositório (`Could not resolve to a Repository`), troque para a conta da empresa (`gh auth switch --user EduardoAlves8006`).

---

### Task 8: `vpnmon-svc status --json`

**Files:**
- Modify: `cmd/vpnmon-svc/pipecmds.go`
- Modify: `cmd/vpnmon-svc/cli.go`
- Modify: `cmd/vpnmon-svc/pipecmds_test.go`

**Interfaces:**
- Consumes: `ipc.Snapshot`, `parseFlags`, `withClient` (Marco A).
- Produces: `func cmdStatus(args []string, e env) error`; `vpnmon-svc status --json` imprime o `ipc.Snapshot` em JSON indentado (campos do protocolo: `vpns[].name`, `state`, `attempt`, `nextAttemptUnix`, `lastError.class` …); argumento posicional ou flag desconhecida → código 2. Usado pelo e2e (Task 10).

- [ ] **Step 1: Escrever o teste que falha**

Acrescente ao fim de `cmd/vpnmon-svc/pipecmds_test.go` (e `"encoding/json"` aos imports):

```go
// status --json devolve o snapshot do protocolo, legível por scripts (e2e).
func TestStatusJSON(t *testing.T) {
	te := newTestEnv(t)
	startService(t, te)
	waitFor(t, func() bool { return stateVia(te) == ipc.StateConectada })
	if code := te.run("status", "--json"); code != 0 {
		t.Fatalf("status --json: %d %q", code, te.errb)
	}
	var snap ipc.Snapshot
	if err := json.Unmarshal(te.out.Bytes(), &snap); err != nil {
		t.Fatalf("saída não é JSON: %v\n%s", err, te.out)
	}
	if len(snap.VPNs) != 1 || snap.VPNs[0].Name != "Matriz" || snap.VPNs[0].State != ipc.StateConectada {
		t.Fatalf("snapshot: %+v", snap)
	}
	if code := te.run("status", "--json", "x"); code != 2 {
		t.Fatalf("argumento extra: %d", code)
	}
	if code := te.run("status", "--yaml"); code != 2 {
		t.Fatalf("flag desconhecida: %d", code)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./cmd/vpnmon-svc/ -run TestStatusJSON`
Expected: FAIL — `status --json: 2 "status não aceita argumentos…"`.

- [ ] **Step 3: Implementar**

`cmd/vpnmon-svc/pipecmds.go` — acrescente `"encoding/json"` aos imports e troque `func cmdStatus(e env) error { … }` inteira por:

```go
// cmdStatus mostra o estado das VPNs; com --json, o snapshot do protocolo
// como veio do serviço (para scripts: roteiro e2e, monitoração).
func cmdStatus(args []string, e env) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "imprime o snapshot em JSON")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usageError{"use: status [--json]"}
	}
	return withClient(e, func(c *ipc.Client) error {
		var snap ipc.Snapshot
		if err := c.Call(ipc.TypeStatus, nil, &snap); err != nil {
			return err
		}
		if *asJSON {
			enc := json.NewEncoder(e.stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(snap)
		}
		fmt.Fprint(e.stdout, formatStatus(snap, e.now()))
		return nil
	})
}
```

`cmd/vpnmon-svc/cli.go` — na tabela `commands`, troque `"status":  noArgs("status", cmdStatus),` por `"status":  cmdStatus,`; no texto `usage`, troque a linha do status por:

```text
  status [--json]                      estado de cada VPN (via pipe)
```

- [ ] **Step 4: Rodar e ver passar**

Run: `go test -race ./cmd/vpnmon-svc/ && make lint`
Expected: `ok` (inclui `TestCommandTableIsSingleSource`, que ainda exige código 2 para `status x`); lint limpo.

- [ ] **Step 5: Commit**

```bash
git add cmd/vpnmon-svc
git commit -m "feat(cli): status --json para scripts"
```

---

### Task 9: Log da bandeja em arquivo

**Files:**
- Modify: `cmd/vpnmon-tray/main.go`
- Modify: `cmd/vpnmon-tray/main_windows.go`
- Modify: `cmd/vpnmon-tray/main_test.go`

**Interfaces:**
- Consumes: `logging.OpenRotating`, `logging.New` (Marco A); `view.Options.Log` (Marco B).
- Produces: `func openTrayLog(dir string) (*slog.Logger, func(), error)` (arquivo `vpnmon-tray.log`, 1 MB × 3, nível info, redação de segredos); no Windows, `trayLog()` usa `%LOCALAPPDATA%\VPNMonitor` e cai para descarte se não der; a bandeja registra "bandeja iniciada" (com a versão), "bandeja encerrada" e o erro fatal da view.

- [ ] **Step 1: Escrever o teste que falha**

Em `cmd/vpnmon-tray/main_test.go`, troque `import "testing"` por:

```go
import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)
```

e acrescente ao fim:

```go
// O log da bandeja cria a pasta, grava em texto e mascara segredos como o
// log do serviço.
func TestOpenTrayLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "VPNMonitor")
	log, closeLog, err := openTrayLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	log.Info("bandeja iniciada", "versao", "v2.1.0", "senha", "segredo")
	log.Debug("não aparece no nível info")
	closeLog()
	b, err := os.ReadFile(filepath.Join(dir, "vpnmon-tray.log"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "bandeja iniciada") || !strings.Contains(s, "versao=v2.1.0") {
		t.Fatalf("log: %q", s)
	}
	if strings.Contains(s, "segredo") || strings.Contains(s, "não aparece") {
		t.Fatalf("segredo ou debug no log: %q", s)
	}
	// Pasta que não pode ser criada (um arquivo no caminho): erro, sem pânico.
	blocker := filepath.Join(t.TempDir(), "arquivo")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openTrayLog(filepath.Join(blocker, "VPNMonitor")); err == nil {
		t.Fatal("esperava erro com a pasta bloqueada")
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./cmd/vpnmon-tray/`
Expected: FAIL de compilação — `undefined: openTrayLog`.

- [ ] **Step 3: Implementar**

`cmd/vpnmon-tray/main.go` — imports:

```go
import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
)
```

e, ao fim do arquivo:

```go
// Log da bandeja: um por usuário, em %LOCALAPPDATA%\VPNMonitor (o usuário não
// lê a pasta do serviço). Pequeno e rotativo: só avisos da interface e o
// ciclo de vida da bandeja.
const (
	trayLogName     = "vpnmon-tray.log"
	trayLogMaxBytes = 1 << 20
	trayLogMaxFiles = 3
)

// openTrayLog abre o log da bandeja em dir (criada se preciso), com a mesma
// redação de segredos do serviço. close fecha o arquivo.
func openTrayLog(dir string) (log *slog.Logger, closeLog func(), err error) {
	w, err := logging.OpenRotating(filepath.Join(dir, trayLogName), trayLogMaxBytes, trayLogMaxFiles)
	if err != nil {
		return nil, nil, err
	}
	return logging.New(w, new(slog.LevelVar)), func() { _ = w.Close() }, nil
}
```

`cmd/vpnmon-tray/main_windows.go` — acrescente `"path/filepath"` aos imports; logo depois de `defer release()`:

```go
	log, closeLog := trayLog()
	defer closeLog()
	log.Info("bandeja iniciada", "versao", appVersion())
	defer log.Info("bandeja encerrada")
```

troque `Log: slog.New(slog.DiscardHandler)})` por `Log: log})` e, no `if err != nil {` que segue o `view.Run`, acrescente antes de `fatal(err.Error())`:

```go
		log.Error("bandeja", "erro", err)
```

e, antes de `// fatal avisa numa caixa de mensagem…`:

```go
// trayLog abre o log em %LOCALAPPDATA%\VPNMonitor; sem a pasta, a bandeja
// segue sem log (descarta).
func trayLog() (*slog.Logger, func()) {
	if dir, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0); err == nil {
		if l, closeLog, err := openTrayLog(filepath.Join(dir, "VPNMonitor")); err == nil {
			return l, closeLog
		}
	}
	return slog.New(slog.DiscardHandler), func() {}
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `go test -race ./cmd/vpnmon-tray/ && GOOS=windows CGO_ENABLED=0 go build -o /dev/null ./cmd/vpnmon-tray && make lint`
Expected: `ok`; build Windows sem erro; lint limpo.

- [ ] **Step 5: Commit**

```bash
git add cmd/vpnmon-tray
git commit -m "feat(tray): log da bandeja em %LOCALAPPDATA%\\VPNMonitor"
```

---

### Task 10: Roteiro e2e do MSI (§10.3) e job `e2e`

**Files:**
- Create: `scripts/e2e/lib.ps1`
- Create: `scripts/e2e/run.ps1`
- Modify: `.github/workflows/ci.yml` (acrescenta o job `e2e`)
- Modify: `.gitignore`

**Interfaces:**
- Consumes: artefato `msi-e2e` (Task 7), `vpnmon-svc status --json` (Task 8), `svc.EnsurePolicy` (Task 5), MSI (Task 6).
- Produces: `pwsh scripts/e2e/run.ps1 -OldMsi <0.0.199> -NewMsi <0.0.299> -LogDir <pasta>` — passos 0–8 da §10.3 mais downgrade recusado e reinstalação; logs em `-LogDir` (artefato `e2e-logs`, sempre).

O roteiro, passo a passo: (0) sonda RasMan + `Add-VpnConnection` → "runner sem RAS" se falhar; (1) entradas SSTP `E2E VPN` e `E2E Link` para 192.0.2.1, `-AllUserConnection`; (2) MSI 0.0.199 com `VPN_ENTRY="E2E VPN" VPN_NAME=Matriz CHECK_HOST=192.0.2.10 INTERVAL=5`; (3) serviço `Running`, automático, RasMan, LocalSystem, política do SCM no registro, ACL e ausência de quarentena, `config.json` do seed, Event Log "iniciado", `Run` e atalho; (4) `Reconectando`, só erros `transitorio`, três tentativas com espera crescente; `vpn add --check link` aparece no status; (5) `credential set --password-stdin` com senha acentuada, `credential list` = cofre; (6) parada ≤ 10 s com discagem em curso e nova partida; (7) upgrade 0.0.299 com hash de config e cofre iguais, uma só instalação, política reaplicada, downgrade recusado; (8) desinstala (serviço, pasta do programa e `Run` somem; ProgramData intacta), reinstala (usa a config existente), desinstala com `PURGE=1` (ProgramData some).

- [ ] **Step 1: Escrever o roteiro**

`scripts/e2e/lib.ps1`:

```powershell
<#
.SYNOPSIS
  Funções do roteiro e2e do MSI (spec §10.3). Carregado por run.ps1.
#>

Set-StrictMode -Version Latest

$script:InstallDir = Join-Path $env:ProgramFiles 'VPN Monitor'
$script:Svc = Join-Path $script:InstallDir 'vpnmon-svc.exe'
$script:DataDir = Join-Path $env:ProgramData 'VPNMonitor'
$script:ServiceKey = 'HKLM:\SYSTEM\CurrentControlSet\Services\VPNMonitor'

function Write-Step {
    param([Parameter(Mandatory)][string]$Text)
    Write-Host ''
    Write-Host "=== $Text ==="
}

function Assert-That {
    param(
        [Parameter(Mandatory)][bool]$Condition,
        [Parameter(Mandatory)][string]$Message
    )
    if (-not $Condition) { throw "FALHOU: $Message" }
    Write-Host "ok: $Message"
}

# Wait-Until repete o bloco até ele devolver verdadeiro ou o prazo vencer.
function Wait-Until {
    param(
        [Parameter(Mandatory)][scriptblock]$Condition,
        [Parameter(Mandatory)][int]$TimeoutSeconds,
        [Parameter(Mandatory)][string]$Message,
        [int]$IntervalMs = 500
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if (& $Condition) {
            Write-Host "ok: $Message"
            return
        }
        Start-Sleep -Milliseconds $IntervalMs
    }
    throw "FALHOU (após $TimeoutSeconds s): $Message"
}

# Invoke-Msiexec roda o msiexec em silêncio com log detalhado e devolve o
# código de saída (0 e 3010 = sucesso).
function Invoke-Msiexec {
    param(
        [Parameter(Mandatory)][ValidateSet('/i', '/x')][string]$Mode,
        [Parameter(Mandatory)][string]$Msi,
        [Parameter(Mandatory)][string]$LogFile,
        [string[]]$Properties = @()
    )
    $msiArgs = @($Mode, "`"$Msi`"", '/qn', '/norestart', '/l*v', "`"$LogFile`"") + $Properties
    Write-Host "msiexec $($msiArgs -join ' ')"
    $p = Start-Process -FilePath 'msiexec.exe' -ArgumentList $msiArgs -Wait -PassThru
    return $p.ExitCode
}

# Invoke-Svc roda o vpnmon-svc instalado; os argumentos vão num array
# (um "--json" solto seria lido como parâmetro da função).
function Invoke-Svc {
    param([Parameter(Mandatory)][string[]]$Arguments)
    $out = & $script:Svc @Arguments 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) { throw "vpnmon-svc $($Arguments -join ' ') saiu com $LASTEXITCODE`: $out" }
    return $out
}

function Get-VpnStatus {
    return (Invoke-Svc @('status', '--json') | ConvertFrom-Json)
}

function Get-InstalledVersion {
    $keys = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*'
    return @(Get-ItemProperty $keys -ErrorAction SilentlyContinue |
            Where-Object { $_.PSObject.Properties['DisplayName'] -and $_.DisplayName -eq 'VPN Monitor' } |
            ForEach-Object { $_.DisplayVersion })
}

# Get-DataHash resume config.json e o cofre (para conferir que o upgrade não
# mexeu neles).
function Get-DataHash {
    $files = @(Join-Path $script:DataDir 'config.json') +
    @(Get-ChildItem (Join-Path $script:DataDir 'credentials') -Filter *.bin -File | ForEach-Object FullName)
    return ($files | Sort-Object | ForEach-Object { "$(Split-Path -Leaf $_)=$((Get-FileHash $_ -Algorithm SHA256).Hash)" }) -join ';'
}

# Test-RasAvailable (passo 0): RasMan sobe e Add-VpnConnection funciona.
function Test-RasAvailable {
    try {
        Set-Service RasMan -StartupType Manual -ErrorAction Stop
        Start-Service RasMan -ErrorAction Stop
        Add-VpnConnection -Name 'vpnmon-sonda' -ServerAddress 192.0.2.1 -TunnelType Sstp `
            -AllUserConnection -Force -ErrorAction Stop
        Remove-VpnConnection -Name 'vpnmon-sonda' -AllUserConnection -Force -ErrorAction Stop
    }
    catch {
        throw "runner sem RAS (RasMan ou Add-VpnConnection indisponível): $($_.Exception.Message)"
    }
}

# Assert-ServicePolicy confere no registro a política que o serviço aplica na
# partida (svc.EnsurePolicy): recuperação 5/30/60 s zerando em 1 dia, também
# em falha sem crash, e preshutdown de 15 s.
function Assert-ServicePolicy {
    Wait-Until -TimeoutSeconds 30 -Message 'política do SCM gravada pelo serviço' -Condition {
        $k = Get-ItemProperty $script:ServiceKey -ErrorAction SilentlyContinue
        $k -and $k.PSObject.Properties['FailureActions'] -and $k.PSObject.Properties['PreshutdownTimeout']
    }
    $k = Get-ItemProperty $script:ServiceKey
    $fa = [byte[]]$k.FailureActions
    # SERVICE_FAILURE_ACTIONS no registro: dwResetPeriod, 3 campos, cActions,
    # depois cActions pares (tipo, espera em ms) a partir do byte 20.
    $reset = [BitConverter]::ToUInt32($fa, 0)
    $count = [BitConverter]::ToUInt32($fa, 12)
    $actions = for ($i = 0; $i -lt $count; $i++) {
        '{0}:{1}' -f [BitConverter]::ToUInt32($fa, 20 + 8 * $i), [BitConverter]::ToUInt32($fa, 24 + 8 * $i)
    }
    Assert-That ($reset -eq 86400) "recuperação zera em 1 dia (veio $reset s)"
    Assert-That (($actions -join ',') -eq '1:5000,1:30000,1:60000') "reinicia após 5/30/60 s (veio $($actions -join ','))"
    Assert-That ($k.FailureActionsOnNonCrashFailures -eq 1) 'recuperação também em falha sem crash'
    Assert-That ($k.PreshutdownTimeout -eq 15000) "preshutdown de 15 s (veio $($k.PreshutdownTimeout) ms)"
}

# Assert-DataAcl confere a pasta de dados: dono BA ou SY, herança desligada,
# só SYSTEM e Administradores com controle total, e nada posto em quarentena.
function Assert-DataAcl {
    $sddl = (Get-Acl $script:DataDir).GetSecurityDescriptorSddlForm('Owner, Access')
    $ok = $sddl -match '^O:(BA|SY)D:PA?I?(\(A;OICI;FA;;;SY\)\(A;OICI;FA;;;BA\)|\(A;OICI;FA;;;BA\)\(A;OICI;FA;;;SY\))$'
    Assert-That $ok "ACL da pasta de dados ($sddl)"
    $quarantined = @(Get-ChildItem $env:ProgramData -Directory -Filter 'VPNMonitor.naoconfiavel-*' -ErrorAction SilentlyContinue)
    Assert-That ($quarantined.Count -eq 0) 'o serviço não pôs a pasta de dados em quarentena'
}

function Save-DiagnosticLog {
    param([Parameter(Mandatory)][string]$LogDir)
    $logs = Join-Path $script:DataDir 'logs'
    if (Test-Path $logs) { Copy-Item "$logs\*" $LogDir -Force -ErrorAction SilentlyContinue }
    Get-WinEvent -FilterHashtable @{ LogName = 'Application'; ProviderName = 'VPNMonitor' } -MaxEvents 50 -ErrorAction SilentlyContinue |
        Format-List TimeCreated, LevelDisplayName, Message | Out-File (Join-Path $LogDir 'eventlog.txt')
    Get-WinEvent -FilterHashtable @{ LogName = 'System'; ProviderName = 'Service Control Manager' } -MaxEvents 50 -ErrorAction SilentlyContinue |
        Format-List TimeCreated, Message | Out-File (Join-Path $LogDir 'scm.txt')
}
```

`scripts/e2e/run.ps1`:

```powershell
<#
.SYNOPSIS
  Roteiro e2e do MSI (spec §10.3) num Windows descartável (runner do CI).

.DESCRIPTION
  Instala, opera, atualiza e remove o VPN Monitor de verdade: exige um
  prompt elevado numa máquina sem o VPN Monitor e altera o sistema (cria
  entradas RAS, serviço, pastas). Os logs (msiexec, serviço, Event Log) ficam
  em -LogDir.

.EXAMPLE
  pwsh scripts/e2e/run.ps1 -OldMsi e2e\VPNMonitor-0.0.1-x64.msi -NewMsi e2e\VPNMonitor-0.0.2-x64.msi -LogDir e2e-logs
#>
[CmdletBinding()]
param(
    # MSI de ProductVersion 0.0.199 (instalação inicial).
    [Parameter(Mandatory)][string]$OldMsi,
    # MSI de ProductVersion 0.0.299 (upgrade).
    [Parameter(Mandatory)][string]$NewMsi,
    [Parameter(Mandatory)][string]$LogDir
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'lib.ps1')

# A senha vai ao vpnmon-svc pelo stdin em UTF-8 (o Windows PowerShell 5.1
# usaria ASCII e trocaria "ç" por "?").
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)

New-Item -ItemType Directory -Force -Path $LogDir | Out-Null
$LogDir = (Resolve-Path $LogDir).Path
$OldMsi = (Resolve-Path $OldMsi).Path
$NewMsi = (Resolve-Path $NewMsi).Path

$Entry = 'E2E VPN'
$LinkEntry = 'E2E Link'

try {
    Write-Step '0. Sonda: RasMan e Add-VpnConnection'
    Test-RasAvailable
    Assert-That (-not (Get-Service VPNMonitor -ErrorAction SilentlyContinue)) 'máquina sem VPN Monitor instalado'

    Write-Step '1. Entradas RAS para todos os usuários, servidor inalcançável (TEST-NET)'
    foreach ($name in $Entry, $LinkEntry) {
        Add-VpnConnection -Name $name -ServerAddress 192.0.2.1 -TunnelType Sstp `
            -AuthenticationMethod MSChapv2 -EncryptionLevel Required -AllUserConnection -Force
    }

    Write-Step '2. Instala o MSI 0.0.199 com seed'
    $code = Invoke-Msiexec -Mode '/i' -Msi $OldMsi -LogFile (Join-Path $LogDir 'install-0.0.199.log') -Properties @(
        "VPN_ENTRY=`"$Entry`"", 'VPN_NAME=Matriz', 'CHECK_HOST=192.0.2.10', 'INTERVAL=5')
    Assert-That ($code -in 0, 3010) "msiexec /i 0.0.199 (código $code)"

    Write-Step '3. Serviço, política do SCM, ACL, config do seed, Event Log, bandeja'
    Wait-Until -TimeoutSeconds 30 -Message 'serviço VPNMonitor em execução' -Condition {
        (Get-Service VPNMonitor -ErrorAction SilentlyContinue).Status -eq 'Running'
    }
    $service = Get-Service VPNMonitor
    Assert-That ($service.StartType -eq 'Automatic') "início automático (veio $($service.StartType))"
    Assert-That (@($service.ServicesDependedOn.Name) -contains 'RasMan') 'depende do RasMan'
    $account = (Get-CimInstance Win32_Service -Filter "Name='VPNMonitor'").StartName
    Assert-That ($account -eq 'LocalSystem') "conta LocalSystem (veio $account)"
    Assert-ServicePolicy
    Assert-DataAcl
    $cfg = Get-Content (Join-Path $DataDir 'config.json') -Raw | ConvertFrom-Json
    Assert-That (@($cfg.vpns).Count -eq 1) 'config gerada pelo seed com uma VPN'
    $v = $cfg.vpns[0]
    Assert-That ($v.name -eq 'Matriz' -and $v.rasEntry -eq $Entry) "VPN Matriz → $Entry"
    Assert-That ($v.check.kind -eq 'ping' -and $v.check.host -eq '192.0.2.10') 'verificação ping 192.0.2.10'
    Assert-That ($v.intervalSeconds -eq 5) 'intervalo 5 s'
    $started = Get-WinEvent -FilterHashtable @{ LogName = 'Application'; ProviderName = 'VPNMonitor' } -MaxEvents 20 |
        Where-Object Message -Like '*iniciado*'
    Assert-That ([bool]$started) 'Event Log: "VPN Monitor … iniciado" (origem registrada pelo MSI)'
    $run = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run' -Name VPNMonitorTray
    Assert-That ($run.VPNMonitorTray -eq "`"$InstallDir\vpnmon-tray.exe`"") 'bandeja no HKLM Run'
    Assert-That (Test-Path "$env:ProgramData\Microsoft\Windows\Start Menu\Programs\VPN Monitor.lnk") 'atalho no menu Iniciar'

    Write-Step '4. Reconectando → erro transitório → backoff crescente; segunda VPN (link)'
    $delays = @{}
    $sawReconnecting = $false
    Wait-Until -TimeoutSeconds 300 -IntervalMs 1000 -Message 'três tentativas com erro transitório' -Condition {
        $m = @((Get-VpnStatus).vpns | Where-Object name -EQ 'Matriz')[0]
        if ($m.state -eq 'Reconectando') { $script:sawReconnecting = $true }
        $next = if ($m.PSObject.Properties['nextAttemptUnix']) { $m.nextAttemptUnix } else { 0 }
        if ($m.attempt -gt 0 -and $next -gt 0 -and -not $delays.ContainsKey([int]$m.attempt)) {
            $delays[[int]$m.attempt] = $next - [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
            Write-Host "tentativa $($m.attempt): próxima em $($delays[[int]$m.attempt]) s ($($m.state))"
        }
        $err = if ($m.PSObject.Properties['lastError']) { $m.lastError } else { $null }
        if ($err -and $err.class -ne 'transitorio') {
            throw "erro não transitório: classe $($err.class), código $($err.code): $($err.message)"
        }
        $delays.Count -ge 3
    }
    Assert-That $sawReconnecting 'estado Reconectando observado'
    $keys = @($delays.Keys | Sort-Object)
    Assert-That ($delays[$keys[-1]] -gt $delays[$keys[0]]) "backoff crescente ($(($keys | ForEach-Object { "$_=$($delays[$_])s" }) -join ', '))"
    Invoke-Svc @('vpn', 'add', '--name', 'Link', '--entry', $LinkEntry, '--check', 'link') | Write-Host
    Wait-Until -TimeoutSeconds 15 -Message 'VPN Link aparece no status' -Condition {
        @((Get-VpnStatus).vpns | Where-Object name -EQ 'Link').Count -eq 1
    }

    Write-Step '5. Credencial pelo stdin'
    $out = 'senha-e2e-çã' | & $Svc credential set Matriz --user 'e2e\usuario' --password-stdin 2>&1 | Out-String
    Assert-That ($LASTEXITCODE -eq 0) "credential set ($out)"
    $list = Invoke-Svc @('credential', 'list')
    Assert-That ($list -match '(?m)^Matriz\s+cofre') "credential list mostra o cofre ($list)"
    Assert-DataAcl

    Write-Step '6. Parada em até 10 s, com discagem em curso; nova partida'
    $sw = [Diagnostics.Stopwatch]::StartNew()
    & sc.exe stop VPNMonitor | Out-Null
    Wait-Until -TimeoutSeconds 30 -IntervalMs 100 -Message 'serviço parado' -Condition {
        (Get-Service VPNMonitor).Status -eq 'Stopped'
    }
    $sw.Stop()
    Assert-That ($sw.Elapsed.TotalSeconds -le 10) "parada em $([math]::Round($sw.Elapsed.TotalSeconds, 1)) s"
    Start-Service VPNMonitor
    Wait-Until -TimeoutSeconds 30 -Message 'serviço de volta' -Condition { (Get-Service VPNMonitor).Status -eq 'Running' }

    Write-Step '7. Upgrade para 0.0.299 preservando config e credencial; downgrade bloqueado'
    $before = Get-DataHash
    $code = Invoke-Msiexec -Mode '/i' -Msi $NewMsi -LogFile (Join-Path $LogDir 'upgrade-0.0.299.log')
    Assert-That ($code -in 0, 3010) "msiexec /i 0.0.299 (código $code)"
    $versions = Get-InstalledVersion
    Assert-That (($versions -join ',') -eq '0.0.299') "uma só instalação, 0.0.299 (veio $($versions -join ','))"
    Wait-Until -TimeoutSeconds 30 -Message 'serviço em execução após o upgrade' -Condition {
        (Get-Service VPNMonitor -ErrorAction SilentlyContinue).Status -eq 'Running'
    }
    Assert-That ((Get-DataHash) -eq $before) 'config.json e cofre intactos no upgrade'
    Assert-That ((Invoke-Svc @('credential', 'list')) -match '(?m)^Matriz\s+cofre') 'credencial preservada'
    Assert-ServicePolicy
    $code = Invoke-Msiexec -Mode '/i' -Msi $OldMsi -LogFile (Join-Path $LogDir 'downgrade-0.0.199.log')
    Assert-That ($code -notin 0, 3010) "downgrade para 0.0.199 recusado (código $code)"
    Assert-That (((Get-InstalledVersion) -join ',') -eq '0.0.299') 'continua 0.0.299'

    Write-Step '8. Desinstala preservando a ProgramData; reinstala; desinstala com PURGE=1'
    $code = Invoke-Msiexec -Mode '/x' -Msi $NewMsi -LogFile (Join-Path $LogDir 'uninstall.log')
    Assert-That ($code -in 0, 3010) "msiexec /x (código $code)"
    Assert-That (-not (Get-Service VPNMonitor -ErrorAction SilentlyContinue)) 'serviço removido'
    Assert-That (-not (Test-Path $InstallDir)) 'Program Files\VPN Monitor removida'
    Assert-That (-not (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run' -Name VPNMonitorTray -ErrorAction SilentlyContinue)) 'bandeja fora do Run'
    Assert-That ((Get-DataHash) -eq $before) 'ProgramData preservada (config e cofre)'

    $code = Invoke-Msiexec -Mode '/i' -Msi $NewMsi -LogFile (Join-Path $LogDir 'reinstall.log')
    Assert-That ($code -in 0, 3010) "reinstalação (código $code)"
    Wait-Until -TimeoutSeconds 30 -Message 'serviço em execução após reinstalar' -Condition {
        (Get-Service VPNMonitor -ErrorAction SilentlyContinue).Status -eq 'Running'
    }
    Assert-That ((Get-DataHash) -eq $before) 'reinstalação usa a config existente'
    Save-DiagnosticLog -LogDir $LogDir
    $code = Invoke-Msiexec -Mode '/x' -Msi $NewMsi -LogFile (Join-Path $LogDir 'uninstall-purge.log') -Properties @('PURGE=1')
    Assert-That ($code -in 0, 3010) "msiexec /x PURGE=1 (código $code)"
    Assert-That (-not (Test-Path $DataDir)) 'PURGE=1 removeu a ProgramData'

    Write-Host ''
    Write-Host 'e2e do MSI: tudo certo'
}
catch {
    Save-DiagnosticLog -LogDir $LogDir
    throw
}
finally {
    foreach ($name in $Entry, $LinkEntry) {
        Remove-VpnConnection -Name $name -AllUserConnection -Force -ErrorAction SilentlyContinue
    }
}
```

Grave os dois em UTF-8 com BOM:

```bash
for f in scripts/e2e/*.ps1; do sed -i '1s/^\xEF\xBB\xBF//; 1s/^/\xEF\xBB\xBF/' "$f"; done
```

Acrescente ao fim de `.github/workflows/ci.yml`:

```yaml
  e2e:
    needs: msi
    runs-on: windows-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: msi-e2e
          path: e2e
      - name: roteiro e2e do MSI (§10.3)
        shell: pwsh
        run: >-
          ./scripts/e2e/run.ps1
          -OldMsi e2e/VPNMonitor-0.0.1-x64.msi
          -NewMsi e2e/VPNMonitor-0.0.2-x64.msi
          -LogDir e2e-logs
      - if: always()
        uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: e2e-logs
          path: e2e-logs
          if-no-files-found: ignore
          retention-days: 7
```

Acrescente ao fim de `.gitignore`:

```text
# Saídas do CI/release na raiz (scripts/e2e é código: só a raiz é ignorada)
/e2e/
/e2e-logs/
/portatil/
/sbom-input/
/notes.md
```

- [ ] **Step 2: Conferir localmente**

Run: `make lint-scripts && make lint-workflows && git check-ignore scripts/e2e/run.ps1; echo "ignorado? $?"`
Expected: `scripts PowerShell sem problemas`; actionlint sem saída; `ignorado? 1` (o roteiro **não** é ignorado — só `/e2e/` na raiz). Antes do BOM, o lint acusaria `PSUseBOMForUnicodeEncodedFile` nos dois arquivos.

- [ ] **Step 3: Commit e push; esperar o e2e**

```bash
git add scripts/e2e .github/workflows/ci.yml .gitignore
git commit -m "ci: e2e do MSI no Windows (instalação, upgrade, desinstalação e PURGE)"
git push
gh run watch --exit-status "$(gh run list --branch feat/v2-distribuicao --workflow ci --limit 1 --json databaseId --jq '.[0].databaseId')"
```

Expected: job `e2e` verde, terminando com `e2e do MSI: tudo certo`. Em falha, baixe `e2e-logs` (`gh run download <id> -n e2e-logs`): o log `install-0.0.199.log` do msiexec mostra erro de ACL/serviço; `eventlog.txt`/`scm.txt` mostram o serviço; uma classe de erro ≠ `transitorio` no passo 4 indica que a entrada SSTP pede interação (703) no runner — registre e ajuste o `-AuthenticationMethod`.

---

### Task 11: Release (`release.yml`, assinatura plugável, notas, Dependabot)

**Files:**
- Create: `.github/workflows/release.yml`
- Create: `scripts/sign.ps1`
- Create: `cliff.toml`
- Create: `.github/dependabot.yml`

**Interfaces:**
- Consumes: `ci.yml` via `workflow_call` (artefato `binarios`), `tools/msiversion` (Task 1), `scripts/build-msi.ps1` (Task 6).
- Produces: tag `v*` → `validate` (outputs `semver`, `product`, `prerelease`; recusa tag fora do formato/limites e commit fora do `main`) → `ci` → `package` (environment `release`; output `signed`; artefato `release` com `VPNMonitor-<semver>-x64.msi` e `VPNMonitor-<semver>-x64-portatil.zip`) → `publish` (`SHA256SUMS`, `VPNMonitor-<semver>-sbom.cdx.json`, proveniência, notas, GitHub Release; `--prerelease` para rc); `pwsh scripts/sign.ps1 -Path <arquivos>` (exige `SIGN_DLIB`, `SIGN_DLIB_METADATA`, `SIGN_TIMESTAMP_URL`).

- [ ] **Step 1: Escrever os arquivos**

`.github/workflows/release.yml`:

```yaml
# Release (spec §10.2): tag vX.Y.Z ou vX.Y.Z-rc.N num commit do main.
# validate → CI inteiro (reaproveitado, com a tag como versão) → package
# (Windows, environment "release": assinatura opcional, MSI, zip) → publish
# (somas, SBOM, proveniência, notas, GitHub Release).
name: release

on:
  push:
    tags: ["v*"]

concurrency:
  group: release-${{ github.ref }}
  cancel-in-progress: false

permissions:
  contents: read

jobs:
  validate:
    runs-on: ubuntu-latest
    outputs:
      semver: ${{ steps.version.outputs.semver }}
      product: ${{ steps.version.outputs.product }}
      prerelease: ${{ steps.version.outputs.prerelease }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - name: semver e limites da ProductVersion
        id: version
        run: go run ./tools/msiversion "$GITHUB_REF_NAME" | tee -a "$GITHUB_OUTPUT"
      - name: o commit da tag está no main
        run: |
          git fetch --no-tags origin main
          if ! git merge-base --is-ancestor "$GITHUB_SHA" origin/main; then
            echo "::error::a tag $GITHUB_REF_NAME não aponta para um commit do main"
            exit 1
          fi

  ci:
    needs: validate
    uses: ./.github/workflows/ci.yml
    with:
      version: ${{ github.ref_name }}
    permissions:
      contents: read
      actions: read
      security-events: write

  package:
    needs: [validate, ci]
    runs-on: windows-latest
    # Secrets e vars da assinatura ficam no environment (com aprovação, se o
    # time quiser).
    environment: release
    outputs:
      # vars do environment só existem nos jobs dele: o publish lê daqui.
      signed: ${{ steps.signing.outputs.signed }}
    env:
      SEMVER: ${{ needs.validate.outputs.semver }}
      PRODUCT: ${{ needs.validate.outputs.product }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      # Os exes do job build do CI acima (mesma execução, versão = tag).
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: binarios
          path: build
      - name: assinatura ligada?
        id: signing
        shell: bash
        env:
          SIGNED: ${{ vars.SIGNING_ENABLED == 'true' }}
        run: echo "signed=$SIGNED" >> "$GITHUB_OUTPUT"
      - name: assina os exes
        if: vars.SIGNING_ENABLED == 'true'
        shell: pwsh
        env:
          SIGN_DLIB: ${{ vars.SIGN_DLIB }}
          SIGN_DLIB_METADATA: ${{ vars.SIGN_DLIB_METADATA }}
          SIGN_TIMESTAMP_URL: ${{ vars.SIGN_TIMESTAMP_URL }}
        run: ./scripts/sign.ps1 -Path build/vpnmon-svc.exe, build/vpnmon-tray.exe
      - name: MSI
        shell: pwsh
        run: ./scripts/build-msi.ps1 -Semver $env:SEMVER -ProductVersion $env:PRODUCT -BinDir build -OutDir dist
      - name: assina o MSI
        if: vars.SIGNING_ENABLED == 'true'
        shell: pwsh
        env:
          SIGN_DLIB: ${{ vars.SIGN_DLIB }}
          SIGN_DLIB_METADATA: ${{ vars.SIGN_DLIB_METADATA }}
          SIGN_TIMESTAMP_URL: ${{ vars.SIGN_TIMESTAMP_URL }}
        run: ./scripts/sign.ps1 -Path "dist/VPNMonitor-$env:SEMVER-x64.msi"
      - name: zip portátil (exes, README, licença)
        shell: pwsh
        run: |
          New-Item -ItemType Directory -Force portatil | Out-Null
          Copy-Item build/vpnmon-svc.exe, build/vpnmon-tray.exe, README.md, installer/LICENCA.txt portatil/
          Compress-Archive -Path portatil/* -DestinationPath "dist/VPNMonitor-$env:SEMVER-x64-portatil.zip"
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: release
          path: |
            dist/*.msi
            dist/*.zip
          if-no-files-found: error

  publish:
    needs: [validate, package]
    runs-on: ubuntu-latest
    permissions:
      contents: write # criar a release
      id-token: write # proveniência
      attestations: write
    env:
      SEMVER: ${{ needs.validate.outputs.semver }}
      PRERELEASE: ${{ needs.validate.outputs.prerelease }}
      SIGNED: ${{ needs.package.outputs.signed }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0 # git-cliff lê o histórico desde a tag anterior
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: release
          path: dist
      - name: exes do zip para o SBOM
        run: unzip -q "dist/VPNMonitor-$SEMVER-x64-portatil.zip" -d sbom-input
      - name: SBOM CycloneDX (syft)
        uses: anchore/sbom-action@66cbf4bc1f1c0d2edc94016e65bc221b6bb0ad6c # v0.24.3
        with:
          path: sbom-input
          format: cyclonedx-json
          output-file: dist/VPNMonitor-${{ env.SEMVER }}-sbom.cdx.json
          upload-artifact: false
          upload-release-assets: false
      - name: SHA256SUMS
        working-directory: dist
        run: sha256sum -- *.msi *.zip *.cdx.json > SHA256SUMS && cat SHA256SUMS
      - name: proveniência do build
        uses: actions/attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8 # v4.2.2
        with:
          subject-path: |
            dist/*.msi
            dist/*.zip
      - name: notas (git-cliff)
        uses: orhun/git-cliff-action@a9a95522b26fe6403f7bb24031f21fb573d0f5ff # v4.9.1
        with:
          config: cliff.toml
          args: --latest --strip header
        env:
          OUTPUT: notes.md
      - name: aviso de binários não assinados
        if: env.SIGNED != 'true'
        run: |
          {
            echo
            echo "> **Binários não assinados.** O Windows SmartScreen e alguns antivírus podem alertar; confira as somas em SHA256SUMS e a proveniência com \`gh attestation verify <arquivo> --repo $GITHUB_REPOSITORY\`."
          } >> notes.md
      - name: GitHub Release
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          flags=()
          if [ "$PRERELEASE" = "true" ]; then flags+=(--prerelease); fi
          gh release create "$GITHUB_REF_NAME" dist/* \
            --verify-tag --title "VPN Monitor $SEMVER" --notes-file notes.md "${flags[@]}"
```

`scripts/sign.ps1` (UTF-8 com BOM):

```powershell
<#
.SYNOPSIS
  Assina arquivos com o signtool (gancho de assinatura da release, spec §10.2).

.DESCRIPTION
  Esqueleto documentado: o pipeline chama este script só quando a variável
  SIGNING_ENABLED do repositório (ou do environment "release") é 'true',
  primeiro com os dois exes e depois com o MSI. O time de infra pluga aqui o
  HSM/serviço de assinatura escolhido; nada de chave ou senha no repositório.

  Configuração por variáveis de ambiente (secrets/vars do environment
  "release", repassadas pelo passo do workflow):

    SIGN_DLIB           DLL de assinatura do provedor (signtool /dlib), por
                        exemplo a do Azure Trusted Signing:
                        C:\tools\TrustedSigning\bin\x64\Azure.CodeSigning.Dlib.dll
    SIGN_DLIB_METADATA  arquivo de metadados lido pela DLL (signtool /dmdf),
                        por exemplo metadata.json com Endpoint, conta e perfil
    SIGN_TIMESTAMP_URL  carimbo de tempo RFC 3161 (signtool /tr)

  Exemplo do comando que este script monta para cada arquivo:

    signtool sign /v /fd SHA256 /td SHA256 /tr http://timestamp.exemplo
      /dlib Azure.CodeSigning.Dlib.dll /dmdf metadata.json vpnmon-svc.exe

  Para um HSM com KSP próprio (sem /dlib), troque os argumentos por
  /csp "<nome do KSP>" /kc "<nome da chave>" /f <certificado.cer>.
  A autenticação no provedor (ex.: azure/login com OIDC) fica em passos do
  workflow antes deste.

.EXAMPLE
  pwsh scripts/sign.ps1 -Path build\vpnmon-svc.exe, build\vpnmon-tray.exe
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string[]]$Path
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Get-SignTool {
    $cmd = Get-Command signtool.exe -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $kits = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits\10\bin'
    $found = Get-ChildItem $kits -Recurse -Filter signtool.exe -ErrorAction SilentlyContinue |
        Where-Object { $_.Directory.Name -eq 'x64' } |
        Sort-Object { [version]$_.Directory.Parent.Name } -ErrorAction SilentlyContinue |
        Select-Object -Last 1
    if (-not $found) { throw 'signtool.exe não encontrado (instale o Windows SDK)' }
    return $found.FullName
}

foreach ($name in 'SIGN_DLIB', 'SIGN_DLIB_METADATA', 'SIGN_TIMESTAMP_URL') {
    if (-not [Environment]::GetEnvironmentVariable($name)) {
        throw "assinatura ligada (SIGNING_ENABLED=true), mas $name não está definida; veja o cabeçalho de scripts/sign.ps1"
    }
}

$signtool = Get-SignTool
foreach ($file in $Path) {
    $full = (Resolve-Path $file).Path
    Write-Host "assinando $full"
    & $signtool sign /v /fd SHA256 /td SHA256 /tr $env:SIGN_TIMESTAMP_URL `
        /dlib $env:SIGN_DLIB /dmdf $env:SIGN_DLIB_METADATA $full
    if ($LASTEXITCODE -ne 0) { throw "signtool sign falhou em $full ($LASTEXITCODE)" }
    & $signtool verify /pa /v $full
    if ($LASTEXITCODE -ne 0) { throw "assinatura de $full não confere ($LASTEXITCODE)" }
}
```

`cliff.toml`:

```toml
# Notas de release (git-cliff, commits convencionais em pt-BR). A release usa
# `git cliff --latest --strip header`: só a seção da tag atual.
[changelog]
header = "# Mudanças\n"
body = """
{% if version %}\
## {{ version | trim_start_matches(pat="v") }} — {{ timestamp | date(format="%Y-%m-%d") }}
{% else %}\
## Não lançado
{% endif %}\
{% for group, commits in commits | group_by(attribute="group") %}
### {{ group | striptags | trim | upper_first }}
{% for commit in commits %}
- {% if commit.scope %}**{{ commit.scope }}:** {% endif %}{{ commit.message | upper_first }} ({{ commit.id | truncate(length=7, end="") }})\
{% endfor %}
{% endfor %}\n
"""
trim = true

[git]
conventional_commits = true
filter_unconventional = true
split_commits = false
commit_parsers = [
  { message = "^feat", group = "<!-- 0 -->Novidades" },
  { message = "^fix", group = "<!-- 1 -->Correções" },
  { message = "^perf", group = "<!-- 2 -->Desempenho" },
  { message = "^refactor", group = "<!-- 3 -->Refatoração" },
  { message = "^docs", group = "<!-- 4 -->Documentação" },
  { message = "^(build|ci)", group = "<!-- 5 -->Build e CI" },
  { message = "^test", group = "<!-- 6 -->Testes" },
  { message = "^chore\\(release\\)", skip = true },
  { message = "^chore", group = "<!-- 7 -->Diversos" },
]
protect_breaking_commits = true
filter_commits = false
tag_pattern = "v[0-9]+\\.[0-9]+\\.[0-9]+(-rc\\.[0-9]+)?$"
sort_commits = "oldest"
```

`.github/dependabot.yml`:

```yaml
# Atualizações semanais (spec §10.4): módulos Go (o principal e o do
# go-winres) e as actions, que seguem fixadas por SHA nos PRs do Dependabot.
version: 2
updates:
  - package-ecosystem: gomod
    directories:
      - "/"
      - "/tools/winres"
    schedule:
      interval: weekly
    commit-message:
      prefix: "build(deps)"
  - package-ecosystem: github-actions
    directory: "/"
    schedule:
      interval: weekly
    commit-message:
      prefix: "ci(deps)"
```

```bash
sed -i '1s/^\xEF\xBB\xBF//; 1s/^/\xEF\xBB\xBF/' scripts/sign.ps1
```

- [ ] **Step 2: Conferir localmente**

Run:

```bash
make lint-workflows && make lint-scripts && \
go run ./tools/msiversion v2.1.0-beta.1; echo "código $?" && \
git cliff --config cliff.toml --unreleased --strip header --tag v2.1.0 | head -5
```

Expected: actionlint e PSScriptAnalyzer limpos; `erro: tag "v2.1.0-beta.1" fora do formato…` e `código 1` (é o que o `validate` faz com uma tag ruim); o git-cliff (v2.14, se instalado) imprime `## 2.1.0 — <data>` seguido de `### Novidades`. Sem git-cliff local, pule só esta última linha.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/release.yml .github/dependabot.yml scripts/sign.ps1 cliff.toml
git commit -m "ci: release por tag com assinatura plugável, SBOM, proveniência e notas; Dependabot"
```

A primeira execução real do `release.yml` é uma tag `-rc.1` depois do merge (ver README, "Release"); não crie tag a partir deste ramo (o `validate` recusa commit fora do `main`).

---

### Task 12: Implantação de exemplo, README e roteiro manual

**Files:**
- Create: `scripts/deploy-exemplo.ps1`
- Modify: `README.md` (conteúdo completo)
- Modify: `docs/TESTE-MANUAL.md`

**Interfaces:**
- Consumes: propriedades do MSI (Task 6), `credential set --password-stdin` (Marco A), `status` (Task 8).
- Produces: `scripts/deploy-exemplo.ps1 -Msi … -Entry … [-Name] [-CheckKind] [-CheckHost] [-CheckPort] [-Interval] [-User] [-Password <SecureString>] [-LogFile]` (Windows PowerShell 5.1 e 7; senha em UTF-8 pelo stdin, BSTR zerado); README com instalação (MSI, silenciosa, GPO, Intune), atualização, desinstalação, diagnóstico, desenvolvimento, CI/CD, release e proteção de branch; roteiro manual do instalador.

- [ ] **Step 1: Escrever**

`scripts/deploy-exemplo.ps1` (UTF-8 com BOM):

```powershell
<#
.SYNOPSIS
  Exemplo de implantação silenciosa do VPN Monitor com credencial.

.DESCRIPTION
  Instala o MSI sem interface, com o seed da primeira VPN, espera o serviço
  subir e grava a credencial pelo stdin (a senha nunca vai para a linha de
  comando nem para o MSI). Roda no Windows PowerShell 5.1 e no PowerShell 7,
  num prompt de administrador; serve de base para um script de Intune (app
  Win32) ou de inicialização de GPO.

  A entrada RAS precisa existir para todos os usuários antes:
    Add-VpnConnection -Name "VPN Matriz" -ServerAddress vpn.exemplo -AllUserConnection

.EXAMPLE
  .\deploy-exemplo.ps1 -Msi .\VPNMonitor-2.1.0-x64.msi -Entry "VPN Matriz" -Name Matriz `
      -CheckHost 10.254.1.172 -User "DOMINIO\svc-vpn"
  (pede a senha sem eco)

.EXAMPLE
  $senha = Read-Host -AsSecureString
  .\deploy-exemplo.ps1 -Msi .\VPNMonitor-2.1.0-x64.msi -Entry "VPN Matriz" -User "DOMINIO\svc-vpn" -Password $senha
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Msi,
    # Entrada RAS (VPN_ENTRY).
    [Parameter(Mandatory)][string]$Entry,
    # Nome da VPN no VPN Monitor (VPN_NAME); padrão: igual à entrada.
    [string]$Name,
    [ValidateSet('', 'ping', 'tcp', 'link')][string]$CheckKind = '',
    [string]$CheckHost,
    [string]$CheckPort,
    [string]$Interval,
    # Usuário da VPN; sem ele, nenhuma credencial é gravada (VPN por
    # certificado ou credencial salva no Windows).
    [string]$User,
    [securestring]$Password,
    [string]$LogFile = (Join-Path $env:TEMP 'VPNMonitor-install.log')
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'rode num prompt de administrador'
}
if (-not $Name) { $Name = $Entry }

# Propriedades do seed (§5.3); valores com espaço vão entre aspas.
$props = @("VPN_ENTRY=`"$Entry`"", "VPN_NAME=`"$Name`"")
if ($CheckKind) { $props += "CHECK_KIND=$CheckKind" }
if ($CheckHost) { $props += "CHECK_HOST=`"$CheckHost`"" }
if ($CheckPort) { $props += "CHECK_PORT=$CheckPort" }
if ($Interval) { $props += "INTERVAL=$Interval" }

$msiArgs = @('/i', "`"$((Resolve-Path $Msi).Path)`"", '/qn', '/norestart', '/l*v', "`"$LogFile`"") + $props
$p = Start-Process -FilePath 'msiexec.exe' -ArgumentList $msiArgs -Wait -PassThru
switch ($p.ExitCode) {
    0 { Write-Host 'VPN Monitor instalado' }
    3010 { Write-Host 'VPN Monitor instalado; a troca de arquivos em uso termina na próxima reinicialização' }
    default { throw "msiexec saiu com $($p.ExitCode); veja $LogFile" }
}

$deadline = (Get-Date).AddSeconds(30)
while ((Get-Service VPNMonitor -ErrorAction SilentlyContinue).Status -ne 'Running') {
    if ((Get-Date) -gt $deadline) { throw 'o serviço VPNMonitor não entrou em execução em 30 s' }
    Start-Sleep -Milliseconds 500
}

if ($User) {
    if (-not $Password) { $Password = Read-Host -AsSecureString "Senha de $User" }
    $svc = Join-Path $env:ProgramFiles 'VPN Monitor\vpnmon-svc.exe'
    # UTF-8 no stdin do vpnmon-svc: o Windows PowerShell 5.1 mandaria ASCII
    # e trocaria acentos por "?".
    $OutputEncoding = New-Object System.Text.UTF8Encoding($false)
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Password)
    try {
        [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr) |
            & $svc credential set $Name --user $User --password-stdin
        if ($LASTEXITCODE -ne 0) { throw "credential set saiu com $LASTEXITCODE" }
    }
    finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }
}

& (Join-Path $env:ProgramFiles 'VPN Monitor\vpnmon-svc.exe') status
```

```bash
sed -i '1s/^\xEF\xBB\xBF//; 1s/^/\xEF\xBB\xBF/' scripts/deploy-exemplo.ps1
```

`README.md` — conteúdo completo:

````markdown
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

A credencial é gravada depois, pela CLI elevada, lendo a senha do stdin:

```powershell
"senha" | & "$env:ProgramFiles\VPN Monitor\vpnmon-svc.exe" credential set Matriz --user DOMINIO\usuario --password-stdin
```

[`scripts/deploy-exemplo.ps1`](scripts/deploy-exemplo.ps1) junta os dois
passos (instalação silenciosa + `credential set` com a senha num
`SecureString`) e serve de base para Intune e GPO.

### GPO e Intune

- **GPO (atribuição de software, computador):** publique o MSI num
  compartilhamento lido pelas contas de computador. Propriedades do seed
  entram por uma transformação (`.mst`, ex.: gerada no Orca) — a GPO não
  aceita linha de comando. A credencial vai num script de inicialização que
  roda o `credential set` (ou use VPN por certificado / credencial salva no
  Windows, que o serviço também usa).
- **Intune:** como app de linha de negócios (MSI, com os argumentos acima) ou
  como app Win32 empacotando o `deploy-exemplo.ps1` com o MSI. Detecção pelo
  código de produto do MSI ou pela chave `HKLM\SOFTWARE\VPNMonitor\Version`.

### Atualização

Instalar uma versão mais nova por cima atualiza (o `UpgradeCode` é fixo);
`config.json`, estado, cofre e logs ficam. As VPNs conectadas **não caem**
durante a troca. Instalar uma versão mais velha por cima é recusado. Com a
bandeja aberta em alguma sessão, o exe dela está em uso: o msiexec termina
com 3010 e a troca do arquivo da bandeja fica para a reinicialização (o
serviço já roda a versão nova).

### Desinstalação

```powershell
msiexec /x VPNMonitor-2.1.0-x64.msi /qn            # mantém C:\ProgramData\VPNMonitor
msiexec /x VPNMonitor-2.1.0-x64.msi /qn PURGE=1    # apaga também a pasta de dados
```

Quem instalou à mão com `vpnmon-svc install` deve rodar
`vpnmon-svc uninstall` antes de instalar o MSI.

## Uso (prompt de administrador)

```text
vpnmon-svc status [--json]              estado de cada VPN (--json: para scripts)
vpnmon-svc vpn add --name Matriz --entry "VPN Matriz" --check ping --host 10.254.1.172
vpnmon-svc credential set Matriz --user dominio\usuario
vpnmon-svc check Matriz                 verificação única, sem discar
vpnmon-svc install | uninstall          registro manual do serviço (sem MSI)
vpnmon-svc version
```

O serviço reaplica a cada partida a política do SCM — reiniciar após 5 s,
30 s e 60 s (zerando em 1 dia, também quando para com erro) e 15 s de
preshutdown — porque a tabela de recuperação do Windows Installer não
funciona (a Microsoft documenta isso); assim, MSI e `install` ficam iguais.

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
| Log do serviço | `%ProgramData%\VPNMonitor\logs\vpnmon.log` (+ `.1` … `.5`); pela bandeja, "Abrir log" |
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

O `make build` gera antes os recursos dos exes (`go-winres`: manifest,
ícone e versão; o manifest com comctl32 v6 é obrigatório para a bandeja
abrir). O go-winres fica fixado por versão e hash num módulo só de
ferramenta, `tools/winres/go.mod`, fora do grafo dos binários
(`go tool -modfile=tools/winres/go.mod go-winres …`). A versão dos exes vem
da tag (`tools/msiversion`); fora de tag, 0.0.0.0.

O MSI só é gerado no Windows:
`pwsh scripts/build-msi.ps1 -Semver 2.1.0 -ProductVersion 2.1.99 -BinDir build -OutDir dist`
(instala o WiX 5.0.2 como ferramenta do dotnet se faltar). O
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
downgrade recusado, desinstalação e `PURGE=1`; logs no artefato `e2e-logs`).

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
assinados". Para ligar, crie o environment `release`, defina a variável de
repositório `SIGNING_ENABLED=true` e as variáveis `SIGN_DLIB`,
`SIGN_DLIB_METADATA` e `SIGN_TIMESTAMP_URL` (e a autenticação do provedor)
conforme o cabeçalho de [`scripts/sign.ps1`](scripts/sign.ps1).

### Proteção de branch recomendada (`main`)

- Exigir PR, com 1 aprovação e revisão descartada a cada push novo.
- Exigir os checks `lint`, `security`, `codeql`, `test-linux`,
  `test-windows`, `build`, `msi` e `e2e`, com o ramo atualizado.
- Histórico linear; sem push forçado nem exclusão.
- Proteger as tags `v*` (regra de tag: só mantenedores criam).
- Environment `release` com revisores obrigatórios, se a assinatura usar HSM.
````

`docs/TESTE-MANUAL.md`:

1. No parágrafo de abertura, troque
   "Rodar antes de cada release, em Windows 10 e 11,\ncom o `vpnmon-svc.exe` e o `vpnmon-tray.exe` gerados por `make build`."
   por:

```markdown
Rodar antes de cada release, em Windows 10 e 11,
com o MSI da release candidata (`vX.Y.Z-rc.N`, artefato do CI ou pré-release)
ou, para serviço e bandeja isolados, com os exes de `make build`.
```

2. Troque o item `- [ ] Upgrade com a VPN de pé (fica para o MSI, Marco C).` por:

```markdown
- [ ] Upgrade com a VPN de pé: ver "Instalador", abaixo.
- [ ] Após reiniciar o serviço, uma VPN com credencial rejeitada tenta de
      novo **uma vez** ao fim da janela de 15 min; credencial trocada com o
      serviço parado só vale após a janela ou um "Reconectar agora".
- [ ] Discagem manual (rasphone) da mesma entrada enquanto o serviço
      espera na fila: o serviço pode derrubá-la e discar de novo (aceito; o
      resultado final é a VPN de pé).
```

3. Acrescente ao fim do arquivo:

```markdown
## Instalador (Marco C)

O e2e do CI já cobre instalação silenciosa, seed, ACL, política do SCM,
upgrade, downgrade, desinstalação e `PURGE=1` num runner limpo. Aqui fica o
que depende de usuário, sessão, domínio ou versão do Windows.

### 17. Instalação interativa

- [ ] Duplo clique no MSI: pede UAC, mostra só o progresso, termina sem erro.
- [ ] A bandeja **não** abre ao fim; o atalho "VPN Monitor" do menu Iniciar
      a abre (sem elevação: confira no Gerenciador de Tarefas, coluna
      "Elevado" = Não).
- [ ] Outro usuário faz login: a bandeja abre sozinha (HKLM Run).
- [ ] "Aplicativos instalados" mostra VPN Monitor com ícone, versão e sem
      "Modificar".
- [ ] `%LOCALAPPDATA%\VPNMonitor\vpnmon-tray.log` criado com "bandeja
      iniciada" e a versão.

### 18. Upgrade

- [ ] Com a VPN conectada, instalar a versão nova por cima: a VPN **não
      cai** (ping contínuo ao alvo durante o upgrade).
- [ ] Com a bandeja aberta em duas sessões (RDP): msiexec termina com 0 ou
      3010; as bandejas antigas seguem funcionando com o serviço novo; após
      reiniciar (ou novo login), a bandeja nova abre.
- [ ] "Sobre / versão" na bandeja e `vpnmon-svc version` mostram a versão
      nova.

### 19. Política do serviço

- [ ] `sc qfailure VPNMonitor`: reiniciar 5000/30000/60000 ms, reset 86400;
      `sc qfailureflag VPNMonitor`: TRUE.
- [ ] Matar o `vpnmon-svc.exe` no Gerenciador de Tarefas: o serviço volta
      em ~5 s e as VPNs continuam de pé.
- [ ] Desligar a máquina com a VPN de pé: sem atraso perceptível nem
      evento de serviço que não respondeu.

### 20. Implantação gerenciada

- [ ] GPO (atribuição de computador) com `.mst` contendo o seed numa
      máquina do domínio: instala no boot, config gerada pelo seed.
- [ ] Intune (LOB ou Win32 com `scripts/deploy-exemplo.ps1`): instala,
      grava a credencial e reporta sucesso.
- [ ] `deploy-exemplo.ps1` no Windows PowerShell 5.1 com senha acentuada:
      a VPN disca (a senha chegou intacta).

### 21. Versões do Windows

- [ ] Windows 10 1809, Windows 11 e Windows Server 2019/2022: instala e
      funciona.
- [ ] Windows 10 anterior ao 1809 (ou 32 bits): o MSI recusa com a
      mensagem de requisito.

### 22. Desinstalação

- [ ] Desinstalar com a bandeja aberta: termina (3010 se o exe da bandeja
      estiver em uso); a pasta de dados fica.
- [ ] Reinstalar: as VPNs e credenciais voltam como estavam.
- [ ] `msiexec /x … PURGE=1`: a pasta de dados some; pastas
      `VPNMonitor.naoconfiavel-*` de quarentena, se houver, ficam (apague à
      mão).

### 23. Assinatura (quando ligada)

- [ ] Propriedades → Assinaturas digitais do MSI e dos exes mostram o
      certificado da empresa com carimbo de tempo.
- [ ] SmartScreen não alerta no download do MSI assinado.
```

- [ ] **Step 2: Conferir**

Run: `make lint-scripts && grep -c -- '- \[ \]' docs/TESTE-MANUAL.md && make lint && go test -race ./...`
Expected: scripts limpos; `73` itens no roteiro manual; lint e testes verdes.

- [ ] **Step 3: Commit e push final**

```bash
git add scripts/deploy-exemplo.ps1 README.md docs/TESTE-MANUAL.md
git commit -m "docs: instalação por MSI/GPO/Intune, deploy silencioso, release e roteiro manual do instalador"
git push
```

Expected: CI inteiro verde no ramo (os oito jobs da proteção de branch recomendada).

---

## Pendências registradas (fora deste marco)

- **`vpnmon-svc diag`** (empacotar config sem segredos, state, logs, Event Log e `sc qc`/`qfailure` num zip): a tabela "Diagnóstico" do README cobre o manual por ora.
- **Go 1.26.6**: o govulncheck aponta vulnerabilidades da stdlib/`net` do 1.26.5 corrigidas no 1.26.6, **não alcançáveis** pelo código (o job passa); subir o `go` do `go.mod` (e do `tools/winres/go.mod`) num PR próprio, que o Dependabot não faz.
- **Cobertura por pacote** abaixo de 80 % em `core/logging` (71,7 %), `core/platform/icmp` (68 %) e `core/platform/svc` (74,2 %) — o total passa; subir com testes dos ramos `_other`/erro.
- **Bandeja com `SetIcon`/`SetToolTip` falhando todo tique**: agora vai para o arquivo (limitado a 3 MB pela rotação); limitar a 1 aviso por minuto (minor do Marco B).
- **`rasBusy` barrando o pedido do snapshot** (lista "Adicionar VPN" velha por até 60 s, minor do Marco B).
- **Ajuste manual de recuperação no `services.msc`** é desfeito pelo `EnsurePolicy` na próxima partida (documentado; se incomodar, aplicar só quando divergir *e* registrar no log).
- **Arquivo da bandeja em uso no upgrade** → msiexec 3010 e troca no reboot (sem `util:CloseApplication`, que é custom action); validar no roteiro manual §18.
- Os minors adiados dos Marcos A e B que não foram citados acima continuam nos respectivos documentos de decisões.

## Autorrevisão (feita)

- **Cobertura do spec:** §9 — MSI por máquina/x64/SO (Task 6, `Launch`), Program Files com exes/licença/README, `ServiceInstall`/`ServiceControl`, recuperação e preshutdown (Task 5, pelo serviço — desvio justificado), `PermissionEx` (Task 6), `util:EventSource`, nenhuma CA própria (teste), `RemoveFolderEx` só com `PURGE=1`, `Run` + atalho, seed `Secure`, sem senha, `MajorUpgrade`/downgrade, ProgramData preservada, `deploy-exemplo.ps1` (Task 12). §10.1 — lint/security/test-linux (cobertura + resumo)/test-windows/build/e2e (Tasks 2, 3, 7, 10), concorrência, permissões, SHA. §10.2 — validação semver + main, regra de versão (Task 1), reprodutível (Task 4), sign plugável, SHA256SUMS, SBOM, proveniência, git-cliff, release com MSI/zip/somas/SBOM, rc = pré-release, aviso sem assinatura (Task 11). §10.3 — passos 0–8 (Task 10). §10.4 — Dependabot (Task 11), Makefile espelhando o CI (Tasks 2–4), README com proteção de branch e release (Task 12). §13 item 3 — README e TESTE-MANUAL (Task 12). Notas do Marco A (FailureActionsWhen, preshutdown) e do Marco B (go-winres como tool, versão duplicada, `--product-version`, log da bandeja) — Tasks 4, 5 e 9.
- **Placeholders:** nenhum "TBD"/"similar à Task N"; todo código está completo; as únicas saídas geradas por comando são `tools/winres/go.sum` (hashes) e os `.syso`.
- **Consistência de nomes:** `FromTag`/`Version` (Task 1) usados só pela CLI; `RecoveryDelays()`, `RecoveryReset`, `PreshutdownTimeout`, `Dependencies()`, `EnsurePolicy()` (Task 5) batem com `installer_test.go` (Task 6) e `Assert-ServicePolicy` (Task 10: 86400, `1:5000,1:30000,1:60000`, 15000); `SeedRegistryPath`/`SeedValueNames()` (Task 6); artefatos `binarios` → `msi`/`msi-e2e` → `e2e`/`release` e nomes `VPNMonitor-<semver>-x64.msi` iguais em `build-msi.ps1`, ci.yml, run.ps1 (0.0.1/0.0.2) e release.yml.
- **Review Focus:** os cinco itens têm teste na tarefa dona (Tasks 1, 5, 6 e 10).
