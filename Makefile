.PHONY: build release clean

# Binary name
BINARY := exisort

# Build info
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
DATE := $(shell date -u '+%Y%m%d')

# Linker flags for release builds
LDFLAGS := -s -w -X main.Version=$(VERSION)-$(DATE)

# Output directory
DIST := dist

# Default target
build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

# Release build for Linux x64
release: clean
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-amd64 .
	@echo "Built: $(DIST)/$(BINARY)-linux-amd64"

# Release builds for all common platforms
release-all: clean
	@mkdir -p $(DIST)
	
	@echo "Building Linux amd64..."
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-amd64 .
	
	@echo "Building Linux arm64..."
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-arm64 .
	
	@echo "Building macOS amd64..."
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-amd64 .
	
	@echo "Building macOS arm64..."
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-arm64 .
	
	@echo "Building Windows amd64..."
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-windows-amd64.exe .
	
	@echo "Done. Binaries in $(DIST)/"

# Create compressed archives for GitHub release
release-tgz: release-all
	cd $(DIST) && tar -czvf $(BINARY)-linux-amd64.tar.gz $(BINARY)-linux-amd64
	cd $(DIST) && tar -czvf $(BINARY)-linux-arm64.tar.gz $(BINARY)-linux-arm64
	cd $(DIST) && tar -czvf $(BINARY)-darwin-amd64.tar.gz $(BINARY)-darwin-amd64
	cd $(DIST) && tar -czvf $(BINARY)-darwin-arm64.tar.gz $(BINARY)-darwin-arm64
	cd $(DIST) && zip $(BINARY)-windows-amd64.zip $(BINARY)-windows-amd64.exe
	@echo "Archives ready in $(DIST)/"

clean:
	rm -rf $(DIST)
	rm -f $(BINARY)
