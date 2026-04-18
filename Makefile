GO ?= go
GOPATH ?= $(CURDIR)/.go
GOCACHE ?= $(CURDIR)/.go/build-cache
BINARY ?= bin/varsynth
ARGS ?=

export GOPATH
export GOCACHE

.PHONY: build run test clean go-env

build: go-env
	$(GO) build -o $(BINARY) ./cmd/varsynth

run: go-env
	$(GO) run ./cmd/varsynth $(ARGS)

test: go-env
	$(GO) test ./...

clean:
	rm -rf bin

go-env:
	mkdir -p "$(GOPATH)" "$(GOCACHE)" "$(dir $(BINARY))"
