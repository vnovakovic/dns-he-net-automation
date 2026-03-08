-- +goose Up
CREATE TABLE IF NOT EXISTS accounts (
    id         TEXT PRIMARY KEY,
    username   TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- NOTE: password is NOT stored in SQLite (SEC-03).
-- Credentials come from CredentialProvider (env var in Phase 1, Vault in Phase 4).
-- This table stores only metadata about known accounts.

CREATE TABLE IF NOT EXISTS schema_info (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO schema_info (key, value) VALUES ('version', '1');

-- +goose Down
DROP TABLE IF EXISTS schema_info;
DROP TABLE IF EXISTS accounts;
