# Kith developer shortcuts. Plain `go` commands work too.
.PHONY: build install uninstall test test-race vet dist clean mac-app mac-dmg mac-test integrations

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

# Kith for macOS (SwiftUI, needs Xcode 26+): mac/build/Kith.app
mac-app:
	sh mac/scripts/bundle.sh

mac-dmg:
	sh mac/scripts/bundle.sh "" --dmg

mac-test:
	cd mac && swift test

clean:
	rm -rf dist mac/build

# Agent plugin bundles (Claude Code marketplace, Antigravity plugin, bare skill)
# from internal/agents; `go test ./internal/agents` checks they are current.
integrations:
	go run ./cmd/kith agents export integrations
