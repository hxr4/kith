# Kith developer shortcuts. Plain `go` commands work too.
.PHONY: build install uninstall test test-race vet dist clean

build:
	CGO_ENABLED=0 go build -trimpath -o dist/kith ./cmd/kith

install:
	./scripts/install.sh

uninstall:
	./scripts/install.sh --uninstall

vet:
	go vet ./...
	GOOS=windows GOARCH=amd64 go vet ./...

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

dist:
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -o dist/kith-darwin-arm64 ./cmd/kith
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -trimpath -o dist/kith-darwin-amd64 ./cmd/kith
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o dist/kith-windows-amd64.exe ./cmd/kith

clean:
	rm -rf dist
