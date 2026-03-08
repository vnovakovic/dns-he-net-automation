-- +goose Up

-- users table: stores Account Users (operators who log in to the admin UI).
--
-- WHY a separate users table (not reusing the admin env var):
--   The single env-var admin is "Server Admin" — it cannot be scoped to a subset of accounts.
--   Account Users need DB-backed credentials so the admin can create, list, and delete them
--   without restarting the server. Each Account User owns a slice of HE accounts (the new
--   user_id FK below) so they only see and manage their own dns.he.net credentials.
--
-- WHY id = username (immutable after creation):
--   Keeps the FK on accounts.user_id stable — renaming the username would require updating
--   every FK row. Using username as PK avoids a synthetic UUID while keeping the table simple.
--   Operators should treat usernames as immutable labels (delete + recreate to rename).
--
-- WHY bcrypt hash (not plaintext):
--   SEC-03 — credentials must not be stored in plaintext. bcrypt is the standard Go
--   password hashing library (golang.org/x/crypto/bcrypt). Cost 12 is a reasonable
--   default: ~300ms on a modern CPU, making brute-force attacks expensive.
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,           -- same as username (immutable after creation)
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,             -- bcrypt hash, NOT plaintext
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- user_id FK: HE accounts now belong to an Account User.
--
-- WHY NULL (not DEFAULT ''):
--   NULL means "admin-owned" — no Account User owns this account, so the server admin
--   can see it and Account Users cannot (WHERE user_id = ? never matches NULL in SQL).
--   SQLite allows adding a nullable column without a DEFAULT to an existing table.
--   Previously DEFAULT '' was used but that broke FK constraint checks in tests:
--   '' is not a valid users.id value, so PRAGMA foreign_keys = ON rejects any INSERT
--   that leaves user_id as the empty-string default. NULL FK is always allowed (it
--   means "no parent row"), which is correct semantics for admin-owned accounts.
--
-- WHY ON DELETE CASCADE:
--   Deleting an Account User should also delete their HE accounts (and via the accounts→zones
--   cascade, their zones too). This keeps the DB self-consistent without requiring application-
--   level cascade logic.
ALTER TABLE accounts ADD COLUMN user_id TEXT REFERENCES users(id) ON DELETE CASCADE;

-- +goose Down

-- Rebuild accounts table without the user_id column.
-- SQLite cannot DROP a column directly (ALTER TABLE ... DROP COLUMN requires SQLite 3.35+
-- and the deployed SQLite may be older). Recreate the table as the safe portable approach.
CREATE TABLE accounts_bak AS SELECT id, username, password, created_at, updated_at FROM accounts;
DROP TABLE accounts;
ALTER TABLE accounts_bak RENAME TO accounts;

DROP TABLE IF EXISTS users;
