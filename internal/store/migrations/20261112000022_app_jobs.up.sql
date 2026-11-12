-- Tareas programadas de una app: un comando que se ejecuta en su contenedor
-- según una expresión cron (UTC).
CREATE TABLE app_jobs (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	app_id         INTEGER NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
	command        TEXT NOT NULL,
	schedule       TEXT NOT NULL,
	enabled        BOOLEAN NOT NULL DEFAULT TRUE,
	last_run_at    TIMESTAMP,
	last_exit_code INTEGER,           -- NULL = aún no terminó ninguna vez; -1 = no llegó a ejecutarse
	last_output    TEXT NOT NULL DEFAULT '',
	created_at     TIMESTAMP NOT NULL
);

--bun:split

CREATE INDEX app_jobs_app ON app_jobs (app_id, id);
