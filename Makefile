.PHONY: fmt check test build
fmt:
	gofmt -w cmd internal
check:
	test -z "$$(gofmt -l cmd internal)"
	go vet ./...
test:
	go test -race -count=1 ./...
build:
	go build -trimpath -o bin/ordarr ./cmd/ordarr
