.PHONY: build test vet lint install release clean hooks

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

build:
	go build -ldflags "$(LDFLAGS)" -o atlas ./cmd/atlas

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/atlas

release:
	goreleaser release --clean

hooks:
	git config core.hooksPath .githooks

clean:
	rm -f atlas
	rm -rf dist/
