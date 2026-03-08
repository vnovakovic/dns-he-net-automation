-- +goose Up

-- token_value stores the AES-256-GCM encrypted raw token string (base64-encoded).
-- It is NULL when TOKEN_RECOVERY_ENABLED=false or when the token was issued before
-- the feature was enabled. The column is always present in the schema regardless of
-- the feature flag — toggling the flag on/off at runtime only affects new issuances
-- and whether the /admin/tokens/{jti}/reveal endpoint responds (no schema migration needed).
--
-- WHY encrypted (not plaintext):
--   A plaintext token in the DB is as good as an active credential — anyone with
--   read access to the DB file can impersonate any account immediately. AES-256-GCM
--   with a key derived from JWT_SECRET means the token value is useless without the
--   server secret. The security model is: DB breach alone is not sufficient; the
--   attacker also needs JWT_SECRET (which is in the environment, not the DB file).
--
-- WHY a separate column (not reusing token_hash):
--   token_hash is a one-way SHA-256 digest used for revocation checks on every
--   authenticated request. It must remain immutable and one-way. token_value is
--   an independently nullable, reversible ciphertext — mixing them would violate
--   the one-way guarantee and complicate the revocation hot path.
ALTER TABLE tokens ADD COLUMN token_value TEXT;

-- +goose Down
-- SQLite does not support DROP COLUMN before v3.35. The column is nullable with no
-- NOT NULL constraint, so downgrading gracefully leaves the column populated with NULLs.
-- If running SQLite >= 3.35 and a true rollback is needed, recreate the table without
-- the column; otherwise accept the orphaned column (it is harmless when the feature is off).
SELECT 1; -- no-op placeholder so goose does not reject an empty Down section
