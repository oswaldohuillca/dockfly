-- API tokens for the CLI and CI.
CREATE TABLE api_tokens (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	name         TEXT NOT NULL,
	token_hash   TEXT NOT NULL UNIQUE, -- SHA-256: the token is only shown when it is created
	prefix       TEXT NOT NULL,        -- to recognize it in the list ("dfy_ab12…")
	role         TEXT NOT NULL,        -- never more than its user's role
	last_used_at TIMESTAMP,
	expires_at   TIMESTAMP,
	created_at   TIMESTAMP NOT NULL
);

--bun:split

CREATE INDEX api_tokens_user ON api_tokens (user_id);
