.PHONY: build test fmt vet

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo v0.2.0)
LDFLAGS := -X github.com/merefield/termcourse.buildVersion=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" ./cmd/termcourse

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...
