.PHONY: build build-linux test vet

# Build for the current OS/architecture (development).
build:
	go build -o bin/server ./cmd/server

# Cross-compile a static Linux amd64 binary (COMPAT-03).
# CGO_ENABLED=0 ensures a fully static binary with no glibc dependency.
# Required for Docker image targeting linux/amd64.
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/server-linux ./cmd/server

test:
	go test ./...

vet:
	go vet ./...
