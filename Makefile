VERSION := $(shell cat VERSION)
ISCC    := C:/Users/vladimir/AppData/Local/Programs/Inno Setup 6/ISCC.exe

.PHONY: build build-linux build-arm64 build-windows installer test vet docker-build docker-run test-integration

# Build for the current OS/architecture (development).
build:
	go build -o bin/server ./cmd/server

# Cross-compile a static Linux amd64 binary (COMPAT-03).
# CGO_ENABLED=0 ensures a fully static binary with no glibc dependency.
# Required for Docker image targeting linux/amd64.
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/server-linux ./cmd/server

# Cross-compile a static Linux arm64 binary (COMPAT-03 extended).
# Useful for ARM-based hosts (Raspberry Pi, Apple Silicon Docker, ARM cloud VMs).
build-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/server-linux-arm64 ./cmd/server

# Cross-compile a static Windows amd64 binary.
build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dnshenet-server-windows-amd64.exe ./cmd/server

# Build Windows installer via Inno Setup 6 (runs build-windows first).
# WHY cp before ISCC: the .iss [Files] section expects dnshenet-server.exe (no arch suffix)
#   in the project root. The Windows build produces dnshenet-server-windows-amd64.exe —
#   a plain cp renames it to match what ISCC looks for.
# DEPENDENCY: Inno Setup 6 must be installed at ISCC path above.
# Output: dnshenet-server-installer.exe in the project root.
installer: build-windows
	cp dnshenet-server-windows-amd64.exe dnshenet-server.exe
	"$(ISCC)" /DMyAppVersion=$(VERSION) installer/dnshenet-server.iss

test:
	go test ./...

vet:
	go vet ./...

# Build the Docker image locally.
# Tag: dns-he-net-automation:latest
docker-build:
	docker build -t dns-he-net-automation:latest .

# Run the Docker image with minimal required env vars.
# Override env vars as needed: make docker-run HE_ACCOUNTS='[...]' JWT_SECRET=secret
docker-run:
	docker run --rm \
		-p 8080:8080 \
		-e HE_ACCOUNTS='$(HE_ACCOUNTS)' \
		-e JWT_SECRET='$(JWT_SECRET)' \
		-e DB_PATH=/data/dnshenet-server.db \
		-v dns-he-net-data:/data \
		dns-he-net-automation:latest

# Run integration tests (requires live dns.he.net credentials).
test-integration:
	go test -tags=integration ./...
