SHELL := /bin/bash
BINARY := termcourse
OUTPUT ?= $(BINARY)
PACKAGE := ./cmd/termcourse

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo v0.2.1)
LDFLAGS := -X github.com/merefield/termcourse.buildVersion=$(VERSION)

.PHONY: build test race-test fmt fmt-check vet integration-test check install clean

build:
	go build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o $(OUTPUT) $(PACKAGE)

test:
	go test ./...

race-test:
	go test -race ./...

fmt:
	gofmt -w .

fmt-check:
	test -z "$$(gofmt -l .)"

vet:
	go vet ./...

integration-test:
	bats test

check: fmt-check vet race-test integration-test build

install: build
	mkdir -p $${DESTDIR}$${PREFIX:-/usr/local}/bin
	install -m 0755 $(OUTPUT) $${DESTDIR}$${PREFIX:-/usr/local}/bin/$(BINARY)

clean:
	go clean
	rm -f $(OUTPUT)
