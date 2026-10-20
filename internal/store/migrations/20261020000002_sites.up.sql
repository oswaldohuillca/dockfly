CREATE TABLE sites (
	id                   INTEGER PRIMARY KEY AUTOINCREMENT,
	name                 TEXT NOT NULL UNIQUE,
	domains              TEXT NOT NULL,
	ssl                  BOOLEAN NOT NULL DEFAULT FALSE,
	force_https          BOOLEAN NOT NULL DEFAULT FALSE,
	hsts                 BOOLEAN NOT NULL DEFAULT FALSE,
	spa                  BOOLEAN NOT NULL DEFAULT FALSE,
	active_deployment_id INTEGER,
	created_at           DATETIME NOT NULL,
	updated_at           DATETIME NOT NULL
);

--bun:split

CREATE TABLE deployments (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	site_id     INTEGER NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
	source      TEXT NOT NULL,
	file_count  INTEGER NOT NULL,
	total_bytes INTEGER NOT NULL,
	created_at  DATETIME NOT NULL
);

--bun:split

CREATE INDEX deployments_site_id ON deployments (site_id, id DESC);
