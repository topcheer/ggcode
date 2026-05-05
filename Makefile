BINARY  := bin/ggcode
PKG     := github.com/topcheer/ggcode/cmd/ggcode
INSTALLER_PKG := github.com/topcheer/ggcode/cmd/ggcode-installer

.PHONY: build test lint verify-ci knight-eval install install-installer install-git-hooks clean

TAGS := goolm

build:
	go build -tags "$(TAGS)" -o $(BINARY) ./cmd/ggcode

test:
	go test -tags "$(TAGS)" ./...

lint:
	go vet -tags "$(TAGS)" ./...

verify-ci:
	./scripts/dev/verify-ci.sh

knight-eval:
	./scripts/dev/knight-eval.sh

install:
	go install $(PKG)

install-installer:
	go install $(INSTALLER_PKG)

install-git-hooks:
	git config core.hooksPath .githooks
	chmod +x .githooks/pre-commit scripts/dev/verify-ci.sh

clean:
	rm -rf bin/
