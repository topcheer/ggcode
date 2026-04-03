BINARY  := bin/ggcode
PKG     := github.com/topcheer/ggcode/cmd/ggcode

.PHONY: build test lint install clean

build:
	go build -o $(BINARY) ./cmd/ggcode

test:
	go test ./...

lint:
	go vet ./...

install:
	go install $(PKG)

clean:
	rm -rf bin/
