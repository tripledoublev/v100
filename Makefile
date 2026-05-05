GO_TARGETS := ./cmd/... ./internal/...

export GOWORK := off
export GOCACHE ?= $(CURDIR)/.gocache
export GOMODCACHE ?= $(CURDIR)/.gomodcache

.PHONY: build lint test

build:
	go build -o v100 ./cmd/v100

lint:
	./scripts/lint.sh

test:
	go test -race -coverprofile=coverage.out $(GO_TARGETS)
