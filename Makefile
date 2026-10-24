VERSION ?= $(shell date +%Y.%m.%d)

interzone:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o interzone .

.PHONY: interzone
