-- Copias de seguridad de las apps de bases de datos (Postgres, MySQL, Redis).
CREATE TABLE db_backup_policies (
	app_id         INTEGER PRIMARY KEY REFERENCES apps (id) ON DELETE CASCADE,
	enabled        BOOLEAN NOT NULL DEFAULT FALSE,
	interval_hours INTEGER NOT NULL DEFAULT 24,
	keep           INTEGER NOT NULL DEFAULT 7,
	updated_at     TIMESTAMP NOT NULL
);

--bun:split

CREATE TABLE db_backups (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	app_id      INTEGER NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
	status      TEXT NOT NULL,       -- running | success | failed
	trigger     TEXT NOT NULL,       -- schedule | manual
	engine      TEXT NOT NULL,       -- postgres | mysql | redis
	location    TEXT NOT NULL DEFAULT '', -- dónde se guardó ("s3://bucket/prefijo")
	object_key  TEXT NOT NULL DEFAULT '',
	size        INTEGER NOT NULL DEFAULT 0,
	error       TEXT NOT NULL DEFAULT '',
	created_at  TIMESTAMP NOT NULL,
	finished_at TIMESTAMP
);

--bun:split

CREATE INDEX db_backups_app ON db_backups (app_id, id);
