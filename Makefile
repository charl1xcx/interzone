# The nearest tag, so self-update can tell a newer release apart; a date
# would read as version 2026.10.29 and outrank every release.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

interzone:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o interzone .

test:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
	go test -race ./...

.PHONY: interzone test
