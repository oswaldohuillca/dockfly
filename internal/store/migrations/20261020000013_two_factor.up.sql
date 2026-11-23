-- Two-factor authentication (TOTP) and recovery codes.
ALTER TABLE users ADD COLUMN totp_secret_enc BLOB;

--bun:split

ALTER TABLE users ADD COLUMN totp_enabled BOOLEAN NOT NULL DEFAULT FALSE;

--bun:split

-- Last accepted 30 s step: stops a code from being reused.
ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;

--bun:split

ALTER TABLE users ADD COLUMN recovery_codes TEXT NOT NULL DEFAULT '[]'; -- hashes SHA-256
