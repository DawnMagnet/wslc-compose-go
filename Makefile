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

.PHONY: all build windows linux dist test race cover golden lint fmt tidy install clean

all: lint test build

build: ## Build for the host platform into bin/
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/wslc-compose

windows: ## Cross-compile Windows binaries (amd64 + arm64) into dist/
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-windows-amd64.exe ./cmd/wslc-compose
	GOOS=windows GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-windows-arm64.exe ./cmd/wslc-compose

linux: ## Cross-compile Linux binaries for use inside WSL distros
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-linux-amd64 ./cmd/wslc-compose
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-linux-arm64 ./cmd/wslc-compose

dist: clean windows linux ## Release assets: all binaries + wrapper scripts + checksums.txt
	cp scripts/install.ps1 scripts/wslc-compose.ps1 scripts/wslc-compose.sh scripts/wslc.cmd dist/
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
