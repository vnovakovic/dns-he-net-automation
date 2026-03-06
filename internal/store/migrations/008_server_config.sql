-- +goose Up

-- server_config: key-value store for server-level settings managed via the database.
--
-- WHY a general key-value table (not a dedicated column per setting):
--   A single table lets future server settings land here without additional schema
--   migrations for every new field. The table is internal — never exposed via the REST API.
--
-- Current keys:
--   admin_password_hash — bcrypt hash of the server admin password.
--     Written on every startup when ADMIN_PASSWORD env var is set (forced override).
--     Written once on first startup when ADMIN_PASSWORD env var is empty (seeds "admin123").
--     After the initial seed, the env var can be cleared; the hash persists in the DB
--     and is used for all subsequent admin logins and Basic Auth checks.
--
-- WHY bcrypt hash (not plaintext):
--   Storing a plaintext password in the DB provides no security benefit over an env var.
--   A bcrypt hash is a one-way transform — an attacker with DB read access cannot recover
--   the password. The env var (ADMIN_PASSWORD) is plaintext only in transit (process env
--   at startup) and never written to the DB.
--
-- WHY NOT store the admin username in server_config:
--   The admin username is an identifier, not a secret. It is set once via ADMIN_USERNAME
--   env var and has no reason to change. Storing it in the DB would add complexity
--   (UI to change it, migration to set initial value) with no benefit.
CREATE TABLE IF NOT EXISTS server_config (
    key   TEXT NOT NULL PRIMARY KEY,
    value TEXT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS server_config;
