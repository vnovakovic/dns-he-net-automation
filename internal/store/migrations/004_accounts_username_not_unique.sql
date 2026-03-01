-- +goose Up
-- Remove UNIQUE constraint from accounts.username.
--
-- WHY: A single dns.he.net login (username) can legitimately be registered
-- under multiple account IDs (e.g. separate IDs per project or environment).
-- The UNIQUE constraint blocked this without any real security or correctness
-- benefit — the PRIMARY KEY on id already prevents duplicate account IDs.
--
-- SQLite cannot DROP a constraint with ALTER TABLE, so we recreate the table.
-- Goose wraps this in a transaction for atomicity.
CREATE TABLE accounts_new (
    id         TEXT PRIMARY KEY,
    username   TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO accounts_new (id, username, created_at, updated_at)
SELECT id, username, created_at, updated_at FROM accounts;

DROP TABLE accounts;

ALTER TABLE accounts_new RENAME TO accounts;

-- +goose Down
-- Re-create the UNIQUE constraint on username (best-effort — will fail if
-- duplicate usernames were inserted while the constraint was absent).
CREATE TABLE accounts_old (
    id         TEXT PRIMARY KEY,
    username   TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO accounts_old (id, username, created_at, updated_at)
SELECT id, username, created_at, updated_at FROM accounts;

DROP TABLE accounts;

ALTER TABLE accounts_old RENAME TO accounts;
