# Espelha o CI (.github/workflows/ci.yml). Rode `make lint test` antes de abrir PR.
BUILD   := build
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -buildid= -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
WIN     := GOOS=windows GOARCH=amd64

.PHONY: all lint test cover build clean

all: lint test build

lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt pendente:"; echo "$$out"; exit 1; fi
	go mod tidy -diff
	go vet ./...
	$(WIN) go vet ./...

test:
	go test -race -shuffle=on ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

build:
	@mkdir -p $(BUILD)
	$(WIN) CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BUILD)/vpnmon-svc.exe ./cmd/vpnmon-svc
	@ls -lh $(BUILD)/*.exe

clean:
	rm -rf $(BUILD) coverage.out
