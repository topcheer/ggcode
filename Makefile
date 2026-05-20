BINARY  := bin/ggcode
PKG     := github.com/topcheer/ggcode/cmd/ggcode
INSTALLER_PKG := github.com/topcheer/ggcode/cmd/ggcode-installer

.PHONY: build build-desktop test lint verify-ci knight-eval install install-installer install-git-hooks clean store-deploy store-deploy-ios store-deploy-android store-version store-screenshots

TAGS := goolm

build:
	go build -tags "$(TAGS)" -o $(BINARY) ./cmd/ggcode

build-desktop:
	cd desktop/ggcode-desktop && CGO_ENABLED=1 go build -tags "$(TAGS)" -ldflags "-X main.Version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o ../../bin/ggcode-desktop .

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

# ── Mobile App Store Deployment ──────────────────────
# Usage:
#   make store-deploy VERSION=1.3.9              # Both platforms
#   make store-deploy-ios VERSION=1.3.9          # iOS only
#   make store-deploy-android VERSION=1.3.9      # Android only
#   make store-version                           # Show current version
#   make store-screenshots                       # Upload metadata + screenshots

VERSION ?= $(shell cd mobile/flutter && grep '^version:' pubspec.yaml | sed 's/version: \([0-9.]*\)+.*/\1/')

store-deploy:
	cd mobile/flutter && bash scripts/store_deploy.sh $(VERSION)

store-deploy-ios:
	cd mobile/flutter && bash scripts/store_deploy.sh $(VERSION) ios

store-deploy-android:
	cd mobile/flutter && bash scripts/store_deploy.sh $(VERSION) android

store-version:
	cd mobile/flutter && bash scripts/store_deploy.sh --current

store-screenshots:
	cd mobile/flutter && bash scripts/store_deploy.sh --screenshots
