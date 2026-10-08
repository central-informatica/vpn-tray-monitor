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
	GOOS=windows GOARCH=amd64 go vet ./...

# Linux e Windows: boa parte do código só compila com GOOS=windows.
lint-golangci:
	$(GOLANGCI) run ./...
	GOOS=windows GOARCH=amd64 $(GOLANGCI) run ./...

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
	pkgs=$$(go list $(COVER_PKGS)) && pkgs=$$(printf '%s\n' "$$pkgs" | grep -v /platform/fake) && [ -n "$$pkgs" ] \
		|| { echo "cover: lista de pacotes vazia ou go list falhou" >&2; exit 1; }; \
	go test -coverprofile=coverage.out $$pkgs && \
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
