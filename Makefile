BIN     := whytop
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build run tidy vet install snapshot

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN) ./cmd/whytop

run: build
	sudo ./bin/$(BIN)

tidy:
	go mod tidy

vet:
	go vet ./...

install: build
	sudo install -m 0755 bin/$(BIN) /usr/local/bin/$(BIN)

snapshot:
	goreleaser release --snapshot --clean
