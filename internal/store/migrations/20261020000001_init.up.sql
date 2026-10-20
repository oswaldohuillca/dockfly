CREATE TABLE users (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	email         TEXT NOT NULL UNIQUE,
	name          TEXT NOT NULL,
	password_hash TEXT NOT NULL,
	created_at    DATETIME NOT NULL
);

--bun:split

CREATE TABLE proxy_hosts (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	domains         TEXT NOT NULL,
	target_type     TEXT NOT NULL,
	forward_scheme  TEXT NOT NULL,
	forward_host    TEXT NOT NULL,
	forward_port    INTEGER NOT NULL,
	ssl             BOOLEAN NOT NULL DEFAULT FALSE,
	force_https     BOOLEAN NOT NULL DEFAULT FALSE,
	hsts            BOOLEAN NOT NULL DEFAULT FALSE,
	skip_tls_verify BOOLEAN NOT NULL DEFAULT FALSE,
	preserve_host   BOOLEAN NOT NULL DEFAULT TRUE,
	enabled         BOOLEAN NOT NULL DEFAULT TRUE,
	created_at      DATETIME NOT NULL,
	updated_at      DATETIME NOT NULL
);
