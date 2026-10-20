-- Servidores remotos: cada uno con un agente que se conecta al panel.
CREATE TABLE servers (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	name         TEXT NOT NULL UNIQUE,
	token_hash   TEXT NOT NULL UNIQUE, -- SHA-256 del token del agente
	last_seen_at TIMESTAMP,
	last_addr    TEXT NOT NULL DEFAULT '',
	version      TEXT NOT NULL DEFAULT '',
	created_at   TIMESTAMP NOT NULL
);

--bun:split

-- Servidor donde corre la app (NULL = el del panel).
ALTER TABLE apps ADD COLUMN server_id INTEGER REFERENCES servers (id);
