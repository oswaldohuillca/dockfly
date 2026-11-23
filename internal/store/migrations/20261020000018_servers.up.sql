-- Remote servers: each one with an agent that connects to the panel.
CREATE TABLE servers (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	name         TEXT NOT NULL UNIQUE,
	token_hash   TEXT NOT NULL UNIQUE, -- SHA-256 of the agent's token
	last_seen_at TIMESTAMP,
	last_addr    TEXT NOT NULL DEFAULT '',
	version      TEXT NOT NULL DEFAULT '',
	created_at   TIMESTAMP NOT NULL
);

--bun:split

-- Server the app runs on (NULL = the panel's own).
ALTER TABLE apps ADD COLUMN server_id INTEGER REFERENCES servers (id);
