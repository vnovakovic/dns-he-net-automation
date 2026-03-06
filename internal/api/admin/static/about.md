# DNS HE.NET Automation Portal

**Version {{VERSION}}**

A self-hosted portal that automates [dns.he.net](https://dns.he.net) via headless browser automation. It exposes a REST API and an admin web UI for managing DNS records, zones, and bearer token authentication — suitable for use in Ansible playbooks, CI/CD pipelines, and automation scripts.

---

## Purpose

dns.he.net does not provide an official REST API. This portal fills that gap by:

- Running a headless Chromium browser session authenticated to dns.he.net
- Exposing a standard JSON REST API that maps to browser actions (create, update, delete records)
- Managing multiple dns.he.net accounts, each with scoped bearer tokens
- Providing BIND zone file import/export for bulk operations
- Logging all mutations to an audit trail

---

## User and Account Types

There are three distinct identity layers in this portal:

### 1. Server Admin

A single built-in superuser configured via environment variables or the database.

- **Username:** set by `ADMIN_USERNAME` env var (default: `admin`)
- **Password:** set by `ADMIN_PASSWORD` env var on first start, then stored as **bcrypt** in the `server_config` table. Once written to the DB the env var can be cleared.
- **Default password on fresh install:** `admin123`
- **Capabilities:** full access to all portal sections including Users management; can see and manage all accounts regardless of owner

#### Resetting a forgotten Admin password

**Docker / Linux:**
```bash
# Connect to the running container
docker exec -it <container-name> sh

# Or connect directly to the SQLite database with sqlite3
sqlite3 /data/dnshenet-server.db \
  "DELETE FROM server_config WHERE key = 'admin_password_hash';"
```
Then restart the container. On next start the password resets to `admin123`. Log in and change it immediately via **Admin → Change Password** or by setting `ADMIN_PASSWORD=<newpassword>` and restarting once.

Alternatively, set the env var without connecting to the container:
```bash
# docker-compose: add to environment section and restart
ADMIN_PASSWORD=mynewpassword
docker-compose up -d
```

**Windows (service):**
```powershell
# Open PowerShell as Administrator
$db = "C:\dnshenet-server\dnshenet-server.db"
& sqlite3.exe $db "DELETE FROM server_config WHERE key = 'admin_password_hash';"
Restart-Service dnshenet-server
```
Or set the environment variable via the registry and restart:
```powershell
$regPath = 'HKLM:\SYSTEM\CurrentControlSet\Services\dnshenet-server'
$env = (Get-ItemProperty $regPath -Name Environment -ErrorAction SilentlyContinue).Environment
Set-ItemProperty $regPath -Name Environment -Value ($env + @('ADMIN_PASSWORD=mynewpassword'))
Restart-Service dnshenet-server
```
After first start with the new password, remove `ADMIN_PASSWORD` from the registry and restart again.

---

### 2. Portal Users (Account Users)

Human operators created by the Server Admin via the **Users** page.

- Each portal user has their own login (username + password)
- Each portal user can register one or more **dns.he.net accounts** under their own namespace
- Account names are **unique per user** — two users can each have an account named `primary`
- Portal users cannot see or manage other users' accounts or tokens
- Portal users do **not** have access to the Users management page

---

### 3. HE.net Accounts (DNS Accounts)

A dns.he.net credential set registered under a portal user (or the Server Admin).

- **Account Name:** a friendly label chosen by the operator (e.g. `primary`, `eyodwa.org`)
- **Username + Password:** actual dns.he.net login credentials
- Each account owns a list of **zones** (fetched from dns.he.net via browser automation)
- Each account can have multiple **bearer tokens** issued for API access

---

## Admin UI

The left sidebar gives access to all sections:

| Page | Purpose |
|------|---------|
| **Accounts** | Register dns.he.net credentials; load and manage zones per account |
| **Tokens** | Issue, view, and revoke bearer tokens for API access |
| **Zones** | Export/import BIND zone files; view zones grouped by account |
| **Sync** | Preview or apply full zone synchronisation from a BIND file |
| **Audit Log** | Read-only log of all API mutations (create, update, delete) |
| **Users** | Manage portal user accounts (Server Admin only) |
| **About** | This page |

### Accounts

Register one or more dns.he.net credentials under a friendly **Account Name** (e.g. `primary`). Once registered, click **Load zones from HE** to fetch the zone list from dns.he.net and store it locally. The zone list is cached in the database; it is not re-fetched on every page load.

### Tokens

Tokens are JWTs issued per account. Each token can be:

- **Account-wide** — full access to all zones in that account
- **Zone-scoped** — restricted to a single zone (enforced by middleware)
- **Role: admin** — can create, update, delete records
- **Role: viewer** — read-only (GET endpoints only)

Tokens are prefixed with a human-readable label:

```
dns-he-net.{accountName}.{role}--{jti}.{jwt}
```

The raw token is shown **once** at issuance and never again (unless Token Recovery is enabled). Store it immediately.

If **Token Recovery** is enabled (`TOKEN_RECOVERY_ENABLED=true`), the **Show** button appears on active tokens. Clicking it prompts for your portal password and decrypts the stored token value — it is shown masked (click the eye icon to reveal). The Copy button copies it to clipboard without revealing it on screen.

### Zones

The Zones page shows all loaded zones grouped by account, with **Export BIND** and **Import BIND** actions per zone.

- **Export BIND** — downloads the full zone as a standard BIND zone file
- **Import BIND** — additive import; existing records not in the file are kept. Dry-run is checked by default — uncheck to apply changes.

### Sync

Sync compares a pasted BIND zone file against the live dns.he.net zone and computes a diff. In dry-run mode it shows what would be added/removed. Uncheck dry-run to apply.

### Curl Templates

On the Accounts page, each zone row shows **GET / POST / DELETE** template buttons. Click any button, paste your bearer token into the dialog, and get a ready-to-run `curl` command for bash, cmd, or PowerShell.

---

## Authentication

All REST API calls require a bearer token in the `Authorization` header:

```
Authorization: Bearer dns-he-net.primary.admin--<jti>.<jwt>
```

Tokens are issued via the admin UI (Tokens page) or via the REST API.

**Zone-scoped tokens** are enforced server-side — a token bound to zone `1110810` is rejected on requests for any other zone.

---

## REST API Reference

Base URL: `https://<host>:9001`

All `/api/v1/` routes require bearer token authentication. `POST`, `PUT`, and `DELETE` mutations additionally require `role: admin`.

### Health

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/healthz` | Returns `{"status":"ok"}`. No auth required. |

### Accounts

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/accounts` | List all accounts for the authenticated token's account |
| `POST` | `/api/v1/accounts` | Create a new account |
| `GET` | `/api/v1/accounts/{accountID}` | Get a single account by UUID |
| `DELETE` | `/api/v1/accounts/{accountID}` | Delete account and all its zones and tokens |

**POST /api/v1/accounts** body:
```json
{ "name": "primary", "username": "your@email.com", "password": "dns.he.net-password" }
```

### Tokens

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/accounts/{accountID}/tokens` | List tokens for an account |
| `POST` | `/api/v1/accounts/{accountID}/tokens` | Issue a new token |
| `DELETE` | `/api/v1/accounts/{accountID}/tokens/{jti}` | Revoke a token |

**POST /api/v1/accounts/{accountID}/tokens** body:
```json
{ "role": "admin", "label": "ansible-prod", "zone_id": "1110810", "expires_in_days": 365 }
```
Omit `zone_id` for an account-wide token. `role` must be `admin` or `viewer`.

### Zones

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/zones` | List zones for the authenticated account |
| `POST` | `/api/v1/zones` | Register a new zone |
| `DELETE` | `/api/v1/zones/{zoneID}` | Delete a zone from dns.he.net and the local DB |

### Records

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/zones/{zoneID}/records` | List all records (optionally filter with `?type=A`) |
| `GET` | `/api/v1/zones/{zoneID}/records/{recordID}` | Get a single record by HE row ID |
| `POST` | `/api/v1/zones/{zoneID}/records` | Create a new DNS record |
| `PUT` | `/api/v1/zones/{zoneID}/records/{recordID}` | Update an existing record |
| `DELETE` | `/api/v1/zones/{zoneID}/records/{recordID}` | Delete a record by HE row ID (idempotent, 204) |
| `DELETE` | `/api/v1/zones/{zoneID}/records?name=...&type=...` | Delete record(s) by name + type; returns JSON confirmation |

**POST /api/v1/zones/{zoneID}/records** body examples:

```json
{ "type": "A",    "name": "www.example.org", "content": "1.2.3.4",            "ttl": 300 }
{ "type": "AAAA", "name": "www.example.org", "content": "2001:db8::1",         "ttl": 300 }
{ "type": "CNAME","name": "alias.example.org","content": "www.example.org",    "ttl": 300 }
{ "type": "TXT",  "name": "example.org",      "content": "v=spf1 ~all",        "ttl": 300 }
{ "type": "MX",   "name": "example.org",      "content": "10 mail.example.org","ttl": 3600 }
{ "type": "SRV",  "name": "_sip._tcp.example.org", "content": "10 mail.example.org", "weight": 20, "port": 5060, "ttl": 300 }
{ "type": "CAA",  "name": "example.org",      "content": "0 issue \"letsencrypt.org\"", "ttl": 3600 }
{ "type": "NS",   "name": "sub.example.org",  "content": "ns1.example.org",    "ttl": 3600 }
{ "type": "A",    "name": "dyn.example.org",  "content": "0.0.0.0", "dynamic": true, "ttl": 300 }
```

**Delete by name** returns a JSON body confirming what was deleted:
```json
{ "deleted_count": 1, "deleted": [{"name": "sub.example.org", "type": "TXT", "id": "8900607586"}] }
```
Use `?type=ANY` to delete all record types matching the name.

**Supported record types:** `A`, `AAAA`, `CNAME`, `MX`, `TXT`, `SRV`, `CAA`, `NS`

**Allowed TTL values:** `300`, `1800`, `3600`, `7200`, `14400`, `86400` (seconds)

### BIND Import / Export (Admin UI only)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/admin/zones/{zoneID}/export?account_id={uuid}` | Download zone as BIND file |
| `POST` | `/admin/zones/{zoneID}/import` | Import BIND zone file (additive, with dry-run option) |

---

## Dynamic DNS (DDNS)

A record with `"dynamic": true` enables HE.net's built-in DDNS. The stored IP is set by dns.he.net to the requester's public IP at creation time (the `content` field is ignored). Update via the standard HE.net DDNS endpoint or by re-issuing a `PUT`.

---

## Security Notes

- All tokens are stored as **SHA-256 hashes** only — plaintext is never persisted
- Tokens are shown **once** at issuance (or via Token Recovery if enabled)
- Zone-scoped tokens are enforced server-side; a token cannot access another zone even with a modified JWT
- Admin password is stored as **bcrypt** in the database (`server_config` table)
- All HTTPS — minimum TLS 1.2
- Rate limiting: global and per-token request limits apply
- The `none` JWT algorithm is explicitly rejected

---

## Configuration

Key environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `JWT_SECRET` | required | HMAC-SHA256 secret for token signing (min 32 chars) |
| `DB_PATH` | `./dns-he-net.db` | Path to the SQLite database file |
| `ADMIN_USERNAME` | `admin` | Server admin login username |
| `ADMIN_PASSWORD` | *(from DB)* | Admin password (bcrypt-hashed on first set) |
| `PORT` | `9001` | HTTPS listen port |
| `SSL_CERT` | required | Path to TLS certificate |
| `SSL_KEY` | required | Path to TLS private key |
| `TOKEN_RECOVERY_ENABLED` | `true` | Enable encrypted token storage for recovery |
| `LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |

---

## Audit Log

Every mutation (create, update, delete record; revoke token) is appended to the audit log with:

- Token JTI (which token made the request)
- Account ID
- Action and resource
- Result (success / failure) and error message if failed
- Timestamp

The audit log is append-only and visible in the **Audit Log** page.

---

## CLI Token Issuance

The binary supports issuing tokens without the web UI:

```bash
./dnshenet-server --issue-token \
  --account primary \
  --role admin \
  --label ansible-prod \
  --expires 365
```

`--account` resolves by account name. The raw token is printed to stdout.
