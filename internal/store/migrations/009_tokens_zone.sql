-- +goose Up
-- Add optional zone scope to tokens.
-- NULL = account-wide token (current/default behavior, backward-compatible).
-- Non-NULL = zone-scoped token (access limited to one zone).
--
-- WHY NOT a foreign key to zones(he_zone_id):
--   zones rows can be deleted (e.g. zone removed from HE). A FK would either cascade-delete
--   valid tokens or block zone removal. Storing zone_id as a plain TEXT column lets issued
--   tokens remain in the DB for audit purposes even after the zone is removed.
ALTER TABLE tokens ADD COLUMN zone_id   TEXT;
ALTER TABLE tokens ADD COLUMN zone_name TEXT;

-- +goose Down
-- SQLite does not support DROP COLUMN before 3.35.0.
-- These columns are nullable so a downgrade simply leaves them present with NULL values.
