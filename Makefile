.PHONY: build test fmt vet

build:
	go build ./cmd/termcourse

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...
