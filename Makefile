BINARY  := bin/ggcode
PKG     := github.com/topcheer/ggcode/cmd/ggcode
INSTALLER_PKG := github.com/topcheer/ggcode/cmd/ggcode-installer

.PHONY: build build-desktop-wails test lint verify-ci ci knight-eval install install-installer install-git-hooks clean store-deploy store-deploy-ios store-deploy-android store-version store-screenshots sync-model-caps

TAGS := goolm

build:
	go build -tags "$(TAGS)" -o $(BINARY) ./cmd/ggcode

build-desktop-wails:
	cd desktop/ggcode-desktop-wails && wails build -tags "$(TAGS)" -clean

test:
	# GOMEMLIMIT + GOGC prevent OOM kills on shared/CI machines with limited RAM.
	# -p 1 compiles one test binary at a time; -parallel 1 runs one test func at a time.
	# 2GiB limit gives headroom for large packages (tui, agent, a2a) on shared machines.
	GOMEMLIMIT=2GiB GOGC=50 go test -tags "$(TAGS)" -p 1 -parallel 1 -timeout 600s ./...

lint:
	go vet -tags "$(TAGS)" ./...

verify-ci:
	./scripts/dev/verify-ci.sh

## ci is an alias for verify-ci, used by harness verification
ci: verify-ci

## sync-model-caps regenerates the static model capability database
## (internal/config/context_window.go + vendor_defaults.go) from
## charmbracelet/catwalk. Run it any time; it is also a required pre-release
## step (docs/release-process.md §3.3). Needs network access to GitHub.
sync-model-caps:
	go run scripts/sync-model-caps.go
	gofmt -w internal/config/context_window.go internal/config/vendor_defaults.go
	@go build -tags "$(TAGS)" ./internal/config/ || (echo "sync-model-caps: generated code failed to build" && exit 1)
	@echo "sync-model-caps: done. Review 'git diff internal/config/' and include it in your commit."

knight-eval:
	./scripts/dev/knight-eval.sh

install:
	go install -tags "$(TAGS)" $(PKG)

install-installer:
	go install $(INSTALLER_PKG)

install-git-hooks:
	git config core.hooksPath .githooks
	chmod +x .githooks/pre-commit scripts/dev/verify-ci.sh

clean:
	rm -rf bin/

# ── Mobile App Store Deployment ──────────────────────
# Usage:
#   make store-deploy                     # Both platforms, auto version from git tag
#   make store-deploy-ios                 # iOS only
#   make store-deploy-android             # Android only
#   make store-deploy VERSION=1.4.0       # Bump version, deploy both
#   make store-version                    # Show current version
#   make store-tag                        # Create git tag for current version

store-deploy:
	cd mobile/flutter && bash scripts/store_deploy.sh $(VERSION)

store-deploy-ios:
	cd mobile/flutter && bash scripts/store_deploy.sh ios

store-deploy-android:
	cd mobile/flutter && bash scripts/store_deploy.sh android

store-version:
	cd mobile/flutter && bash scripts/store_deploy.sh --current

store-tag:
	cd mobile/flutter && bash scripts/store_deploy.sh --tag

store-release:
	cd mobile/flutter && bash scripts/store_deploy.sh --release
