GO_TARGETS := ./cmd/... ./internal/...
TEST_TARGETS ?= $(GO_TARGETS)
TEST_FLAGS ?=
ifneq ($(strip $(RUN)),)
TEST_FLAGS += -run '$(RUN)'
endif

export GOWORK := off
export GOCACHE ?= $(CURDIR)/.gocache
export GOMODCACHE ?= $(CURDIR)/.gomodcache

.PHONY: build lint test

build:
	go build -o v100 ./cmd/v100

lint:
	./scripts/lint.sh

test:
	TEST_TARGETS="$(TEST_TARGETS)" ./scripts/test.sh $(TEST_FLAGS)
