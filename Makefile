BINARY  := bin/ggcode
PKG     := github.com/topcheer/ggcode/cmd/ggcode
INSTALLER_PKG := github.com/topcheer/ggcode/cmd/ggcode-installer

.PHONY: build test lint install install-installer clean

build:
	go build -o $(BINARY) ./cmd/ggcode

test:
	go test ./...

lint:
	go vet ./...

install:
	go install $(PKG)

install-installer:
	go install $(INSTALLER_PKG)

clean:
	rm -rf bin/
