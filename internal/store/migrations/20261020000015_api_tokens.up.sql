-- Tokens de API para el CLI y la integración continua.
CREATE TABLE api_tokens (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	name         TEXT NOT NULL,
	token_hash   TEXT NOT NULL UNIQUE, -- SHA-256: el token solo se muestra al crearlo
	prefix       TEXT NOT NULL,        -- para reconocerlo en la lista ("dfy_ab12…")
	role         TEXT NOT NULL,        -- nunca más que el rol de su usuario
	last_used_at TIMESTAMP,
	expires_at   TIMESTAMP,
	created_at   TIMESTAMP NOT NULL
);

--bun:split

CREATE INDEX api_tokens_user ON api_tokens (user_id);
