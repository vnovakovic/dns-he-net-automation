-- +goose Up
CREATE TABLE IF NOT EXISTS audit_log (
    id           INTEGER  PRIMARY KEY AUTOINCREMENT,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    token_id     TEXT     NOT NULL,
    account_id   TEXT     NOT NULL,
    action       TEXT     NOT NULL CHECK (action IN ('create','update','delete','sync')),
    resource     TEXT     NOT NULL,
    result       TEXT     NOT NULL CHECK (result IN ('success','failure')),
    error_msg    TEXT
);

CREATE INDEX IF NOT EXISTS idx_audit_log_account_id ON audit_log(account_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at  ON audit_log(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_token_id    ON audit_log(token_id);

-- +goose Down
DROP INDEX IF EXISTS idx_audit_log_token_id;
DROP INDEX IF EXISTS idx_audit_log_created_at;
DROP INDEX IF EXISTS idx_audit_log_account_id;
DROP TABLE IF EXISTS audit_log;
