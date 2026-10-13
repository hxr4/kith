# Kith developer shortcuts. Plain `go` commands work too.
.PHONY: build install uninstall test test-race vet dist clean

build:
	CGO_ENABLED=0 go build -trimpath -o dist/kith ./cmd/kith

install:
	sh scripts/install.sh

uninstall:
	sh scripts/install.sh --uninstall

TARGETS := darwin/arm64 darwin/amd64 windows/amd64 linux/amd64 linux/arm64 linux/arm

vet:
	@for t in $(TARGETS); do echo "vet $$t"; GOOS=$${t%/*} GOARCH=$${t#*/} go vet ./... || exit 1; done

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

dist:
	@for t in $(TARGETS); do o=$${t%/*}; a=$${t#*/}; ext=; [ $$o = windows ] && ext=.exe; \
		echo "build dist/kith-$$o-$$a$$ext"; \
		CGO_ENABLED=0 GOOS=$$o GOARCH=$$a go build -trimpath -o dist/kith-$$o-$$a$$ext ./cmd/kith || exit 1; done

clean:
	rm -rf dist
