-- Apps: containers Dockfly manages end to end.
CREATE TABLE apps (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	name              TEXT NOT NULL UNIQUE, -- also its name on the Dockfly network
	source            TEXT NOT NULL,        -- "image" | "git"
	image             TEXT NOT NULL DEFAULT '',
	git_connection_id INTEGER REFERENCES git_connections (id) ON DELETE SET NULL,
	repo              TEXT NOT NULL DEFAULT '',
	branch            TEXT NOT NULL DEFAULT '',
	root_dir          TEXT NOT NULL DEFAULT '',
	dockerfile        TEXT NOT NULL DEFAULT '',
	webhook_id        TEXT NOT NULL DEFAULT '',
	webhook_secret    TEXT NOT NULL DEFAULT '',
	port              INTEGER NOT NULL DEFAULT 0, -- 0 = no web traffic (workers, databases)
	domains           TEXT NOT NULL DEFAULT '[]',
	ssl               BOOLEAN NOT NULL DEFAULT FALSE,
	force_https       BOOLEAN NOT NULL DEFAULT FALSE,
	hsts              BOOLEAN NOT NULL DEFAULT FALSE,
	env_enc           BLOB,
	volumes           TEXT NOT NULL DEFAULT '[]', -- [{"name":"data","path":"/var/lib/postgresql/data"}]
	command           TEXT NOT NULL DEFAULT '',
	memory_mb         INTEGER NOT NULL DEFAULT 0,
	cpus              REAL NOT NULL DEFAULT 0,
	health_path       TEXT NOT NULL DEFAULT '',
	stopped           BOOLEAN NOT NULL DEFAULT FALSE, -- stopped on purpose by the user
	current_deployment_id INTEGER,
	created_at        DATETIME NOT NULL,
	updated_at        DATETIME NOT NULL
);

--bun:split

CREATE TABLE app_deployments (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	app_id         INTEGER NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
	status         TEXT NOT NULL,
	trigger        TEXT NOT NULL,
	image          TEXT NOT NULL DEFAULT '', -- the image that runs (pulled or built)
	container      TEXT NOT NULL DEFAULT '', -- container name
	commit_sha     TEXT NOT NULL DEFAULT '',
	commit_message TEXT NOT NULL DEFAULT '',
	error          TEXT NOT NULL DEFAULT '',
	created_at     DATETIME NOT NULL,
	started_at     DATETIME,
	finished_at    DATETIME
);

--bun:split

CREATE INDEX app_deployments_app ON app_deployments (app_id, id DESC);

--bun:split

CREATE INDEX app_deployments_status ON app_deployments (status);
