# Stage 1: Download Go module dependencies (cached separately for faster rebuilds).
FROM golang:1.25 AS modules
WORKDIR /modules
COPY go.mod go.sum ./
RUN go mod download

# Stage 2: Build the Go binary and install the Playwright CLI.
FROM golang:1.25 AS builder
WORKDIR /app

# Copy cached modules from stage 1 to avoid re-downloading.
COPY --from=modules /go/pkg /go/pkg

# Copy source code.
COPY . .

# Build the server binary.
# CGO_ENABLED=0: required for modernc.org/sqlite (pure Go, no CGo).
# GOOS/GOARCH: explicit cross-compilation target.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -o /bin/server ./cmd/server

# Extract playwright-go version from go.mod and install the Playwright CLI.
# This is needed so the runtime stage can install the correct Chromium version.
RUN PW_VERSION=$(grep 'playwright-go' go.mod | awk '{print $2}') && \
    echo "Installing playwright CLI version: $PW_VERSION" && \
    go install "github.com/playwright-community/playwright-go/cmd/playwright@${PW_VERSION}"

# Stage 3: Minimal runtime image with Chromium.
# Use ubuntu:noble (not alpine) -- Chromium on alpine requires musl workarounds.
FROM ubuntu:noble

# Install system dependencies: CA certs for HTTPS, tzdata for time zones.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        tzdata && \
    rm -rf /var/lib/apt/lists/*

# Copy the Playwright CLI from the builder stage and install Chromium with system deps.
# --with-deps installs ~50 system libraries required by Chromium (libx11, libnss3, etc.).
# Without --with-deps, Chromium fails to launch with "error while loading shared libraries".
COPY --from=builder /go/bin/playwright /usr/local/bin/playwright
RUN playwright install --with-deps chromium

# Copy the compiled server binary.
COPY --from=builder /bin/server /usr/local/bin/server

# Default environment variables for production deployment.
ENV PLAYWRIGHT_HEADLESS=true

# Expose the HTTP API port.
EXPOSE 8080

ENTRYPOINT ["server"]
