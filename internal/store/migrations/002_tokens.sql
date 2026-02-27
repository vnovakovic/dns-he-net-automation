-- +goose Up
CREATE TABLE IF NOT EXISTS tokens (
    jti        TEXT PRIMARY KEY,
    account_id TEXT NOT NULL
               REFERENCES accounts(id)
               ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('admin', 'viewer')),
    label      TEXT,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_tokens_account_id ON tokens(account_id);

-- +goose Down
DROP INDEX IF EXISTS idx_tokens_account_id;
DROP TABLE IF EXISTS tokens;
