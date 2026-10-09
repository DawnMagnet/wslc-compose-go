# wslc-compose-go — common developer tasks.
BINARY  := wslc-compose
PKG     := github.com/DawnMagnet/wslc-compose-go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(PKG)/internal/app.Version=$(VERSION) \
	-X $(PKG)/internal/app.Commit=$(COMMIT) \
	-X $(PKG)/internal/app.Date=$(DATE)

.PHONY: all build windows linux compress dist test race cover golden lint fmt tidy install clean

all: lint test build

build: ## Build for the host platform into bin/
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/wslc-compose

windows: ## Cross-compile Windows binaries (amd64 + arm64) into dist/
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-windows-amd64.exe ./cmd/wslc-compose
	GOOS=windows GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-windows-arm64.exe ./cmd/wslc-compose

linux: ## Cross-compile Linux binaries for use inside WSL distros
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-linux-amd64 ./cmd/wslc-compose
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-linux-arm64 ./cmd/wslc-compose

# UPX shrinks the release binaries to roughly 30% of their size (they unpack
# in memory at start-up, which costs a few milliseconds). Only targets that are
# smoke-tested on real hardware are packed; the arm64 builds stay uncompressed.
# Install UPX from https://github.com/upx/upx/releases (or `apt install upx-ucl`);
# when it is missing, `compress` is skipped with a warning. Disable with `make dist UPX=`.
UPX         ?= upx
UPX_FLAGS   ?= --best --lzma -q
UPX_TARGETS ?= dist/$(BINARY)-windows-amd64.exe dist/$(BINARY)-linux-amd64

compress: ## Pack UPX_TARGETS with UPX and verify each packed file
	@if [ -z "$(UPX)" ] || ! command -v $(UPX) >/dev/null 2>&1; then \
		echo "warning: UPX not found, release binaries left uncompressed" >&2; exit 0; fi; \
	for f in $(UPX_TARGETS); do $(UPX) $(UPX_FLAGS) "$$f" && $(UPX) -t -q "$$f" || exit 1; done

dist: clean windows linux compress ## Release assets: binaries (UPX-packed) + scripts + checksums.txt
	cp scripts/install.ps1 scripts/wslc-compose.profile.ps1 scripts/wslc-compose.sh scripts/wslc.cmd dist/
	cd dist && sha256sum * > checksums.txt

test: ## Run unit and golden tests
	go test ./...

race: ## Run tests with the race detector
	go test -race -count=1 ./...

cover: ## Write coverage.out and print a summary
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

golden: ## Regenerate testdata/golden after an intended output change
	go test ./... -update

lint: ## gofmt check + go vet
	@test -z "$$(gofmt -l .)" || (gofmt -l .; echo "run make fmt"; exit 1)
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

install: ## go install with version metadata
	go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/wslc-compose

clean:
	rm -rf bin dist coverage.out
