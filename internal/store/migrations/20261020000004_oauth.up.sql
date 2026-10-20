-- GitHub App propia de esta instalación, creada con el flujo de manifiesto.
CREATE TABLE github_apps (
	id                 INTEGER PRIMARY KEY AUTOINCREMENT,
	web_url            TEXT NOT NULL, -- https://github.com o el de GitHub Enterprise
	api_url            TEXT NOT NULL,
	app_id             INTEGER NOT NULL,
	slug               TEXT NOT NULL,
	name               TEXT NOT NULL,
	owner              TEXT NOT NULL,
	html_url           TEXT NOT NULL,
	client_id          TEXT NOT NULL,
	client_secret_enc  BLOB NOT NULL,
	webhook_secret_enc BLOB,
	private_key_enc    BLOB NOT NULL,
	webhook_active     BOOLEAN NOT NULL DEFAULT FALSE,
	created_at         DATETIME NOT NULL,
	UNIQUE (web_url, app_id)
);

--bun:split

-- Aplicación OAuth de GitLab (gitlab.com o una instancia propia).
CREATE TABLE gitlab_apps (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	web_url           TEXT NOT NULL UNIQUE,
	client_id         TEXT NOT NULL,
	client_secret_enc BLOB NOT NULL,
	created_at        DATETIME NOT NULL
);

--bun:split

-- Cómo se autentica cada conexión: "token" (de acceso personal),
-- "github_app" (instalación de la GitHub App) o "gitlab_oauth".
ALTER TABLE git_connections ADD COLUMN auth_type TEXT NOT NULL DEFAULT 'token';

--bun:split

ALTER TABLE git_connections ADD COLUMN github_app_id INTEGER REFERENCES github_apps (id) ON DELETE CASCADE;

--bun:split

ALTER TABLE git_connections ADD COLUMN installation_id INTEGER;

--bun:split

ALTER TABLE git_connections ADD COLUMN gitlab_app_id INTEGER REFERENCES gitlab_apps (id) ON DELETE CASCADE;

--bun:split

ALTER TABLE git_connections ADD COLUMN refresh_token_enc BLOB;

--bun:split

ALTER TABLE git_connections ADD COLUMN token_expires_at DATETIME;

--bun:split

ALTER TABLE git_connections ADD COLUMN redirect_uri TEXT NOT NULL DEFAULT '';

--bun:split

-- Página donde el usuario gestiona el acceso (p. ej. qué repos ve la app).
ALTER TABLE git_connections ADD COLUMN settings_url TEXT NOT NULL DEFAULT '';

--bun:split

ALTER TABLE git_connections ADD COLUMN account_type TEXT NOT NULL DEFAULT '';
