# Espelha o CI (.github/workflows/ci.yml). Rode `make lint test` antes de abrir PR.
BUILD   := build
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -buildid= -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
WIN     := GOOS=windows GOARCH=amd64
# Versão numérica dos recursos do exe (X.Y.Z.0); fora de uma tag, 0.0.0.0.
# A conversão da §10.2 (rc → Z×100+N) entra no release do Marco C.
WINVER  := $(or $(shell echo $(VERSION) | sed -nE 's/^v?([0-9]+)\.([0-9]+)\.([0-9]+).*/\1.\2.\3.0/p'),0.0.0.0)
WINRES  := go run github.com/tc-hib/go-winres@v0.3.3
GOLANGCI ?= golangci-lint
COVER_PKGS := ./internal/core/... ./internal/features/monitor/domain ./internal/features/monitor/service ./internal/features/tray/viewmodel

.PHONY: all lint lint-go lint-golangci lint-workflows test cover cover-tray winres build clean

all: lint test build

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

# Manifest (comctl32 v6, que o walk exige; DPI por monitor), ícone e versão
# do vpnmon-tray.exe: gera cmd/vpnmon-tray/rsrc_windows_amd64.syso (não
# versionado) a partir de cmd/vpnmon-tray/winres/winres.json.
winres:
	$(WINRES) make --in cmd/vpnmon-tray/winres/winres.json --out cmd/vpnmon-tray/rsrc --arch amd64 \
		--product-version $(WINVER) --file-version $(WINVER)

build: winres
	@mkdir -p $(BUILD)
	$(WIN) CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BUILD)/vpnmon-svc.exe ./cmd/vpnmon-svc
	$(WIN) CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS) -H windowsgui" -o $(BUILD)/vpnmon-tray.exe ./cmd/vpnmon-tray
	@ls -lh $(BUILD)/*.exe

clean:
	rm -rf $(BUILD) coverage.out coverage-tray.out cmd/vpnmon-tray/*.syso
