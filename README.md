# ES DNS Engine — DNS Automation Portal

A self-hosted portal that automates [dns.he.net](https://dns.he.net) via headless browser automation.
Exposes a REST API and an admin web UI for managing DNS records, zones, and bearer token authentication.

---

## Prerequisites

| Tool | Required for |
|------|-------------|
| Go 1.25+ | Building the binary |
| [templ](https://templ.guide) | Regenerating UI templates (only when `.templ` files change) |
| [Inno Setup 6](https://jrsoftware.org/ispage.php) | Building the Windows installer |
| Docker | Building and running the container image |
| sqlite3 | Inspecting / resetting the database (optional) |

---

## 1. Build

### Linux binary (amd64)

```bash
make build-linux
# Output: bin/server-linux
```

### Linux binary (arm64)

```bash
make build-arm64
# Output: bin/server-linux-arm64
```

### Windows binary (amd64)

```bash
make build-windows
# Output: dnshenet-server-windows-amd64.exe
```

### Windows installer (Inno Setup)

Builds the Windows binary first, then packages it into an installer.

```bash
make installer
# Output: dnshenet-server-installer.exe
```

Manual step-by-step (if not using make):

```bash
# Step 1 — compile Windows binary
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -o dnshenet-server.exe ./cmd/server

# Step 2 — build installer (version from VERSION file)
VERSION=$(cat VERSION)
"C:/Users/vladimir/AppData/Local/Programs/Inno Setup 6/ISCC.exe" \
  /DMyAppVersion=$VERSION \
  installer/dnshenet-server.iss

# Output: dnshenet-server-installer.exe in project root
```

### Windows installer — what it does

Running `dnshenet-server-installer.exe` as Administrator performs these steps in order:

1. Copies `dnshenet-server.exe` to `C:\Program Files\dnshenet-server\`
2. Generates a self-signed TLS certificate at `{app}\server.crt` / `server.key` (skipped on reinstall if the cert already exists)
3. Registers `dnshenet-server` as a Windows auto-start service (`sc create ... start= auto`)
4. Downloads Playwright/Chromium browser binaries (~200 MB) to `{app}\browsers\` — this is required for the headless browser automation
5. Sets the service environment variables in the registry (`ENV_FILE`, `PLAYWRIGHT_BROWSERS_PATH`, `PLAYWRIGHT_DRIVER_PATH`, `LOG_FILE`)
6. **Auto-generates a random `JWT_SECRET`** and writes it into `C:\dnshenet-server.env` (replaces the placeholder from the template)
7. Starts the service — `https://localhost:9001/admin` is accessible immediately

#### Why the service needs auto-generated JWT_SECRET

The installer copies `.env.example` to `C:\dnshenet-server.env` on first install. That template contains the literal placeholder `REPLACE_WITH_RANDOM_32_CHAR_SECRET` as the `JWT_SECRET` value. Before this was automated, the service start at the end of the installer would silently fail — the server rejected the placeholder, exited before reporting `SERVICE_RUNNING`, and Windows SCM left the service in the `Stopped` state. The operator had to manually edit the env file and run `sc start dnshenet-server`.

The fix generates a 32-byte cryptographically random secret (via the Windows CSPRNG, equivalent to `openssl rand -hex 32`) and patches it into the env file in-place before the service start command runs.

#### After install

- **Admin UI:** `https://localhost:9001/admin` — default credentials `admin` / `admin123`
- **Config file:** `C:\dnshenet-server.env` — edit to change admin password, log level, etc.
- **Service logs:** `C:\dnshenet-service.log` — written on every start; tail it to see startup errors
- **Database:** `C:\Program Files\dnshenet-server\dnshenet-server.db` — survives reinstall and uninstall (uninstaller asks before deleting)

#### Service management

```powershell
# Start / stop / restart
Start-Service   dnshenet-server
Stop-Service    dnshenet-server
Restart-Service dnshenet-server

# Check status
Get-Service dnshenet-server

# Follow live logs
Get-Content C:\dnshenet-service.log -Wait
```

#### Change admin password after install

```powershell
# 1. Edit C:\dnshenet-server.env — set:
#      ADMIN_PASSWORD=your-new-password
# 2. Restart — the server bcrypt-hashes it and stores it in the DB
Restart-Service dnshenet-server
# 3. Clear the value in the env file (the hash is now in the DB):
#      ADMIN_PASSWORD=
# 4. Restart again
Restart-Service dnshenet-server
```

---

### Regenerate UI templates (only needed when `.templ` files change)

```bash
templ generate
go build -o dnshenet-server.exe ./cmd/server
```

---

## 2. Docker

### Build image

```bash
docker build -t dns-he-net-automation:latest .

# With explicit version tag
VERSION=$(cat VERSION)
docker build -t dns-he-net-automation:$VERSION -t dns-he-net-automation:latest .
```

### Run container

```bash
docker run -d \
  --name dns-he-net \
  --restart unless-stopped \
  -p 9001:9001 \
  -v /opt/dnshenet-data:/data \
  -e JWT_SECRET="your-random-32-char-secret-here" \
  -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD=admin123 \
  -e TOKEN_RECOVERY_ENABLED=true \
  dns-he-net-automation:latest
```

### docker-compose

Create `docker-compose.yaml` alongside the project:

```yaml
services:
  dns-he-net:
    image: dns-he-net-automation:latest
    container_name: dns-he-net
    restart: unless-stopped
    ports:
      - "9001:9001"
    volumes:
      - /opt/dnshenet-data:/data
    environment:
      - JWT_SECRET=your-random-32-char-secret-here
      - ADMIN_USERNAME=admin
      - ADMIN_PASSWORD=admin123
      - TOKEN_RECOVERY_ENABLED=true
      - DB_PATH=/data/dnshenet-server.db
      - SSL_CERT=/data/server.crt
      - SSL_KEY=/data/server.key
      - LOG_LEVEL=info
```

```bash
# Start
docker-compose up -d

# View logs
docker-compose logs -f

# Stop
docker-compose down

# Stop and remove data volume
docker-compose down -v
```

### Useful Docker commands

```bash
docker logs dns-he-net          # view logs
docker logs -f dns-he-net       # follow logs
docker stop dns-he-net          # stop container
docker start dns-he-net         # start container
docker restart dns-he-net       # restart container
docker rm dns-he-net            # remove container
docker exec -it dns-he-net sh   # open shell inside container
```

---

## 3. Run locally (development)

```bash
# Generate a self-signed TLS certificate (first time only)
go run ./cmd/server gen-cert --cert ./server.crt --key ./server.key

# Start the server
DB_PATH="./dnshenet-server.db" \
JWT_SECRET="dev-secret-32-bytes-minimum-len!" \
ADMIN_USERNAME="admin" \
SSL_CERT="./server.crt" \
SSL_KEY="./server.key" \
LOG_LEVEL="debug" \
  ./dnshenet-server.exe

# API health check
curl -sk https://localhost:9001/healthz
# → {"status":"ok"}

# Admin UI
# Open https://localhost:9001/admin/login  (default: admin / admin123)
```

---

## 4. Configuration

All configuration is via environment variables or an env file.

Copy `.env.example` to `.env` and edit:

```bash
cp .env.example .env
```

| Variable | Default | Description |
|----------|---------|-------------|
| `JWT_SECRET` | required | HMAC-SHA256 secret for token signing (min 32 chars) |
| `DB_PATH` | `./dns-he-net.db` | Path to SQLite database |
| `PORT` | `9001` | HTTPS listen port |
| `SSL_CERT` | required | Path to TLS certificate (PEM) |
| `SSL_KEY` | required | Path to TLS private key (PEM) |
| `ADMIN_USERNAME` | `admin` | Server admin username |
| `ADMIN_PASSWORD` | *(from DB)* | Admin password — hashed and stored on first set |
| `TOKEN_RECOVERY_ENABLED` | `true` | Enable encrypted token storage for recovery |
| `LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |

### Generate JWT secret

```bash
openssl rand -hex 32
```

### Reset forgotten admin password

**Linux / Docker:**
```bash
docker exec -it dns-he-net sh
sqlite3 /data/dnshenet-server.db \
  "DELETE FROM server_config WHERE key = 'admin_password_hash';"
# Restart container — password resets to admin123
```

**Windows (service):**
```powershell
sqlite3.exe "C:\Program Files\dnshenet-server\dnshenet-server.db" `
  "DELETE FROM server_config WHERE key = 'admin_password_hash';"
Restart-Service dnshenet-server
```

---

## 5. Tests

```bash
# Unit tests
make test

# With coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Integration tests (requires live dns.he.net credentials)
make test-integration
```

---

## 6. Screenshots (About page workflow images)

Screenshots are captured from a running portal instance using a headless Playwright command:

```bash
# Start the server first, then:
go run ./cmd/screenshots/ \
  --url https://localhost:9001 \
  --username admin \
  --password admin123 \
  --out internal/api/admin/static/screenshots

# Rebuild to embed the new screenshots into the binary
make build-windows   # or build-linux
```

---

## Project layout

```
cmd/server/          — server entry point (main.go)
cmd/screenshots/     — standalone screenshot capture command
internal/
  api/               — HTTP handlers, middleware, router
  api/admin/         — admin UI (templ templates, static assets)
  browser/           — Playwright session manager + page objects
  store/             — SQLite storage + migrations
  token/             — JWT issuance and validation
  model/             — shared domain types
  bindio/            — BIND zone file import/export
  reconcile/         — zone diff and sync engine
installer/           — Inno Setup 6 script (Windows installer)
```
