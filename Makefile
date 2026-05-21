.PHONY: build test bench clean

BINARY := bin/pull-all-tui
MAIN   := ./cmd/pull-all-tui

build:
	@mkdir -p bin
	go build -ldflags="-s -w" -o $(BINARY) $(MAIN)

test:
	go test ./...

bench:
	go test -bench=. -benchmem ./...

clean:
	rm -f $(BINARY)

.DEFAULT_GOAL := build
