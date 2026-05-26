BINARY  = darkline-agent
VERSION = $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS = -s -w -X main.version=$(VERSION)

.PHONY: build build-linux build-arm install clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/agent

# Linux amd64 (most VPS)
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY)-linux-amd64 ./cmd/agent

# Linux arm64 (ARM VPS / Raspberry Pi)
build-arm:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BINARY)-linux-arm64 ./cmd/agent

# Build all targets
all: build-linux build-arm

clean:
	rm -f $(BINARY) $(BINARY)-linux-*

# Install to system (run as root on VPN server)
install: build-linux
	install -m 755 $(BINARY)-linux-amd64 /usr/local/bin/$(BINARY)
	@echo "Installed to /usr/local/bin/$(BINARY)"
