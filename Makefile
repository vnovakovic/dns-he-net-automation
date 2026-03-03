.PHONY: build build-linux build-arm64 test vet docker-build docker-run test-integration

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
