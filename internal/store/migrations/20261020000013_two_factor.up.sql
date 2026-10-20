-- Verificación en dos pasos (TOTP) y códigos de recuperación.
ALTER TABLE users ADD COLUMN totp_secret_enc BLOB;

--bun:split

ALTER TABLE users ADD COLUMN totp_enabled BOOLEAN NOT NULL DEFAULT FALSE;

--bun:split

-- Último paso de 30 s aceptado: impide reutilizar un código.
ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;

--bun:split

ALTER TABLE users ADD COLUMN recovery_codes TEXT NOT NULL DEFAULT '[]'; -- hashes SHA-256
