-- +goose Up

-- Add password column to accounts table.
--
-- WHY store password in SQLite (change from env-var-only design):
--   Phase 1-4 stored credentials only in HE_ACCOUNTS env var or Vault.
--   Operators managing a single self-hosted instance found the env var
--   approach friction-heavy — every restart required the JSON array.
--   Storing the password in SQLite (0600 file permissions, SEC-03) makes
--   the admin UI the primary credential management interface.
--   Vault is still preferred for multi-host / secret-rotation deployments.
--
-- DEFAULT '' so existing rows (migrated from schema without password) are
-- valid. The DB credential provider returns an error for accounts with empty
-- password so the missing credential is caught at runtime, not at migration.
--
-- SQLite cannot ADD a NOT NULL column without DEFAULT, so we use DEFAULT ''.
-- Re-creating the table (SQLite's only way to change constraints) is not
-- needed here because we are just adding a column with a default.
ALTER TABLE accounts ADD COLUMN password TEXT NOT NULL DEFAULT '';

-- Zones table: persistent zone-to-account mapping stored in SQLite.
--
-- WHY store zones in DB instead of always live-fetching:
--   Live-fetching requires a Playwright browser session per page load.
--   Storing in DB allows the Zones page and REST API to enumerate zones
--   without browser automation. Zones are populated by the admin UI
--   "Load from dns.he.net" button (which upserts into this table).
--
-- he_zone_id: numeric zone ID assigned by dns.he.net.
--   Empty for manually-registered zones until the first "Load" sync.
--   Must be populated before export/import/sync browser operations can run.
--
-- CASCADE on account delete: removing an account also removes its zones.
CREATE TABLE IF NOT EXISTS zones (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    he_zone_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (account_id, name)
);

-- +goose Down

DROP TABLE IF EXISTS zones;

-- SQLite cannot DROP a column directly (requires table recreation).
-- This down migration recreates accounts without the password column.
CREATE TABLE accounts_bak AS SELECT id, username, created_at, updated_at FROM accounts;
DROP TABLE accounts;
ALTER TABLE accounts_bak RENAME TO accounts;
