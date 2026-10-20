-- Cuentas de GitHub/GitLab vinculadas. El token se guarda cifrado.
CREATE TABLE git_connections (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	provider   TEXT NOT NULL,
	base_url   TEXT NOT NULL,
	username   TEXT NOT NULL,
	token_enc  BLOB NOT NULL,
	created_at DATETIME NOT NULL
);

--bun:split

-- Ajustes globales clave/valor (p. ej. la URL pública para los webhooks).
CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

--bun:split

-- Configuración de build de un sitio enlazado a un repositorio.
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

-- Variables de entorno del build, cifradas (pueden contener claves de API).
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

-- Los despliegues que vienen de un build recuerdan su commit.
ALTER TABLE deployments ADD COLUMN commit_sha TEXT NOT NULL DEFAULT '';

--bun:split

ALTER TABLE deployments ADD COLUMN commit_message TEXT NOT NULL DEFAULT '';

--bun:split

ALTER TABLE deployments ADD COLUMN branch TEXT NOT NULL DEFAULT '';
