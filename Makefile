BINARY  := bin/ggcode
PKG     := github.com/topcheer/ggcode/cmd/ggcode
INSTALLER_PKG := github.com/topcheer/ggcode/cmd/ggcode-installer

.PHONY: build test lint verify-ci install install-installer install-git-hooks clean

build:
	go build -o $(BINARY) ./cmd/ggcode

test:
	go test ./...

lint:
	go vet ./...

verify-ci:
	./scripts/dev/verify-ci.sh

install:
	go install $(PKG)

install-installer:
	go install $(INSTALLER_PKG)

install-git-hooks:
	git config core.hooksPath .githooks
	chmod +x .githooks/pre-commit scripts/dev/verify-ci.sh

clean:
	rm -rf bin/
