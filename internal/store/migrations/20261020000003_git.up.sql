-- Linked GitHub/GitLab accounts. The token is stored encrypted.
CREATE TABLE git_connections (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	provider   TEXT NOT NULL,
	base_url   TEXT NOT NULL,
	username   TEXT NOT NULL,
	token_enc  BLOB NOT NULL,
	created_at DATETIME NOT NULL
);

--bun:split

-- Global key/value settings (for example the public URL for the webhooks).
CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

--bun:split

-- Build configuration of a site linked to a repository.
ALTER TABLE sites ADD COLUMN git_connection_id INTEGER REFERENCES git_connections (id) ON DELETE SET NULL;

--bun:split

ALTER TABLE sites ADD COLUMN repo TEXT NOT NULL DEFAULT '';

--bun:split

ALTER TABLE sites ADD COLUMN branch TEXT NOT NULL DEFAULT '';

--bun:split

ALTER TABLE sites ADD COLUMN root_dir TEXT NOT NULL DEFAULT '';

--bun:split

ALTER TABLE sites ADD COLUMN install_command TEXT NOT NULL DEFAULT '';

--bun:split

ALTER TABLE sites ADD COLUMN build_command TEXT NOT NULL DEFAULT '';

--bun:split

ALTER TABLE sites ADD COLUMN output_dir TEXT NOT NULL DEFAULT '';

--bun:split

ALTER TABLE sites ADD COLUMN build_image TEXT NOT NULL DEFAULT '';

--bun:split

-- Build environment variables, encrypted (they may hold API keys).
ALTER TABLE sites ADD COLUMN build_env_enc BLOB;

--bun:split

ALTER TABLE sites ADD COLUMN webhook_id TEXT NOT NULL DEFAULT '';

--bun:split

ALTER TABLE sites ADD COLUMN webhook_secret TEXT NOT NULL DEFAULT '';

--bun:split

CREATE TABLE builds (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	site_id        INTEGER NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
	status         TEXT NOT NULL,
	trigger        TEXT NOT NULL,
	branch         TEXT NOT NULL,
	commit_sha     TEXT NOT NULL DEFAULT '',
	commit_message TEXT NOT NULL DEFAULT '',
	deployment_id  INTEGER,
	error          TEXT NOT NULL DEFAULT '',
	created_at     DATETIME NOT NULL,
	started_at     DATETIME,
	finished_at    DATETIME
);

--bun:split

CREATE INDEX builds_site_id ON builds (site_id, id DESC);

--bun:split

CREATE INDEX builds_status ON builds (status);

--bun:split

-- Deployments that come from a build remember their commit.
ALTER TABLE deployments ADD COLUMN commit_sha TEXT NOT NULL DEFAULT '';

--bun:split

ALTER TABLE deployments ADD COLUMN commit_message TEXT NOT NULL DEFAULT '';

--bun:split

ALTER TABLE deployments ADD COLUMN branch TEXT NOT NULL DEFAULT '';
