GO      ?= go
BINARY  := bin/bothy
LDFLAGS := -s -w

.PHONY: all build test lint fmt clean

all: lint test build

build:
	CGO_ENABLED=0 $(GO) build -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/bothy

test:
	$(GO) test ./...

# Uses golangci-lint when available, otherwise falls back to go vet + gofmt.
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		$(GO) vet ./...; \
		unformatted=$$(gofmt -l .); \
		if [ -n "$$unformatted" ]; then \
			echo "gofmt needed on:"; echo "$$unformatted"; exit 1; \
		fi; \
	fi

fmt:
	gofmt -w .

clean:
	rm -rf bin dist
