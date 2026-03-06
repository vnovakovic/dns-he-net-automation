-- +goose Up
-- +goose StatementBegin

-- Migration 010: Convert accounts.id from user-chosen string to UUID.
--
-- PROBLEM:
--   accounts.id is the PRIMARY KEY and is user-chosen (e.g. "primary").
--   This prevents two different portal users from each having an account
--   named "primary" — a valid multi-user requirement.
--
-- SOLUTION:
--   Separate the internal identifier from the user-facing label:
--     - accounts.id  → auto-generated UUID (globally unique; FK in tokens/zones)
--     - accounts.name → user-chosen label (unique per user, not globally)
--
-- UNIQUENESS CONSTRAINT (per-user, not global):
--   UNIQUE(user_id, name) alone fails for admin accounts because SQLite treats
--   two NULLs as distinct — so two admin accounts named "primary" would NOT
--   conflict. Using COALESCE(user_id, '') collapses all NULLs to '', making
--   the unique index enforce uniqueness across all admin-owned accounts too.
--
-- TOKEN REVOCATION:
--   All existing tokens embed the old string account_id in their JWT payload.
--   After migration the account_id claim will no longer match the UUID stored
--   in the tokens table → silent auth failures. To prevent this confusion,
--   all tokens are revoked here. Operators must re-issue tokens after migration.
--
-- audit_log.account_id is a plain TEXT column (not a FK) — left as-is since
-- it holds historical values from before this migration.

-- Step 1: Add temporary columns for uuid and name.
ALTER TABLE accounts ADD COLUMN uuid TEXT NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN name TEXT NOT NULL DEFAULT '';

-- Step 2: Populate uuid from randomblob(16) formatted as lowercase hex (no dashes).
--   WHY lower(hex(randomblob(16))): SQLite has no built-in uuid() before 3.38.
--   lower(hex(randomblob(16))) produces a 32-char hex string which is globally
--   unique with the same collision probability as UUID v4 (122 random bits).
--   We use this format instead of inserting dashes because it avoids string
--   concatenation in pure SQL, and the Go code generates proper UUID v4 strings
--   for all new accounts going forward (only migration rows use this format).
UPDATE accounts SET uuid = lower(hex(randomblob(16))), name = id;

-- Step 3: Recreate accounts table with uuid as PK and name as the label column.
CREATE TABLE accounts_new (
    id         TEXT PRIMARY KEY,           -- UUID (was user-chosen string)
    name       TEXT NOT NULL,              -- user-chosen label (was id)
    username   TEXT NOT NULL,
    password   TEXT NOT NULL DEFAULT '',
    user_id    TEXT REFERENCES users(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO accounts_new (id, name, username, password, user_id, created_at, updated_at)
SELECT uuid, name, username, password, user_id, created_at, updated_at
FROM accounts;

-- Step 4: Recreate zones table with account_id FK pointing to the new UUID.
CREATE TABLE zones_new (
    account_id TEXT NOT NULL REFERENCES accounts_new(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    he_zone_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (account_id, name)
);

INSERT INTO zones_new (account_id, name, he_zone_id, created_at)
SELECT accounts_new.id, zones.name, zones.he_zone_id, zones.created_at
FROM zones
JOIN accounts ON zones.account_id = accounts.id
JOIN accounts_new ON accounts_new.name = accounts.id AND accounts_new.username = accounts.username;

-- Step 5: Recreate tokens table with account_id FK pointing to the new UUID.
--   Also set revoked_at = CURRENT_TIMESTAMP on ALL rows so old JWTs (which embed
--   the old string account_id in their payload) are cleanly rejected rather than
--   causing confusing "token not found" errors.
CREATE TABLE tokens_new (
    jti          TEXT PRIMARY KEY,
    account_id   TEXT NOT NULL REFERENCES accounts_new(id) ON DELETE CASCADE,
    role         TEXT NOT NULL,
    label        TEXT,
    token_hash   TEXT NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at   DATETIME,
    revoked_at   DATETIME,
    token_value  TEXT,
    zone_id      TEXT,
    zone_name    TEXT
);

INSERT INTO tokens_new (jti, account_id, role, label, token_hash, created_at, expires_at,
                        revoked_at, token_value, zone_id, zone_name)
SELECT t.jti, accounts_new.id, t.role, t.label, t.token_hash, t.created_at, t.expires_at,
       -- Revoke all existing tokens: they embed the old string account_id in their JWT
       -- claim and would fail auth with a confusing mismatch after this migration.
       -- Operators must re-issue tokens using the new UUID-based account IDs.
       CURRENT_TIMESTAMP,
       t.token_value, t.zone_id, t.zone_name
FROM tokens t
JOIN accounts ON t.account_id = accounts.id
JOIN accounts_new ON accounts_new.name = accounts.id AND accounts_new.username = accounts.username;

-- Step 6: Swap tables (drop old, rename new).
DROP TABLE tokens;
DROP TABLE zones;
DROP TABLE accounts;
ALTER TABLE tokens_new RENAME TO tokens;
ALTER TABLE zones_new RENAME TO zones;
ALTER TABLE accounts_new RENAME TO accounts;

-- Step 7: Recreate indexes.
--
-- Per-user uniqueness index using COALESCE:
--   WHY not UNIQUE(user_id, name) directly:
--     SQLite treats NULL as distinct from NULL, so two rows where user_id IS NULL
--     and name = 'primary' would NOT violate UNIQUE(user_id, name). This would
--     allow an admin to create two accounts named "primary", which we must prevent.
--     COALESCE(user_id, '') maps NULL → '' so all admin accounts share the same
--     "user slot" for uniqueness purposes, enforcing global uniqueness for admin-owned
--     accounts and per-user uniqueness for account-user-owned accounts.
CREATE UNIQUE INDEX idx_accounts_user_name
    ON accounts(COALESCE(user_id, ''), name);

CREATE INDEX idx_tokens_account ON tokens(account_id);
CREATE INDEX idx_zones_account ON zones(account_id);

-- +goose StatementEnd

-- +goose Down
-- Down migration is a no-op comment. Reversing a UUID migration would require
-- knowing the original string IDs (stored in the name column) and all issued tokens
-- are already revoked, so there is no safe automated rollback.
-- To roll back: restore from backup taken before applying this migration.
SELECT 1;
