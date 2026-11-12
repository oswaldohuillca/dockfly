-- Esquema inicial para PostgreSQL, equivalente a las migraciones de SQLite
-- (../migrations) hasta 20261112000022. Cada migración nueva necesita su
-- archivo en las tres carpetas.

CREATE TABLE users (
	id            BIGSERIAL PRIMARY KEY,
	email         TEXT NOT NULL UNIQUE,
	name          TEXT NOT NULL,
	password_hash TEXT NOT NULL,
	created_at    TIMESTAMPTZ NOT NULL,
	totp_secret_enc BYTEA,
	totp_enabled BOOLEAN NOT NULL DEFAULT FALSE,
	totp_last_step BIGINT NOT NULL DEFAULT 0,
	recovery_codes TEXT NOT NULL DEFAULT '[]',
	role TEXT NOT NULL DEFAULT 'admin',
	session_version BIGINT NOT NULL DEFAULT 0
);

--bun:split

CREATE TABLE access_lists (
	id         BIGSERIAL PRIMARY KEY,
	name       TEXT NOT NULL,
	users      TEXT NOT NULL DEFAULT '[]',
	allow_ips  TEXT NOT NULL DEFAULT '[]',
	created_at TIMESTAMPTZ NOT NULL
);

--bun:split

CREATE TABLE github_apps (
	id                 BIGSERIAL PRIMARY KEY,
	web_url            TEXT NOT NULL,
	api_url            TEXT NOT NULL,
	app_id             BIGINT NOT NULL,
	slug               TEXT NOT NULL,
	name               TEXT NOT NULL,
	owner              TEXT NOT NULL,
	html_url           TEXT NOT NULL,
	client_id          TEXT NOT NULL,
	client_secret_enc  BYTEA NOT NULL,
	webhook_secret_enc BYTEA,
	private_key_enc    BYTEA NOT NULL,
	webhook_active     BOOLEAN NOT NULL DEFAULT FALSE,
	created_at         TIMESTAMPTZ NOT NULL,
	UNIQUE (web_url, app_id)
);

--bun:split

CREATE TABLE gitlab_apps (
	id                BIGSERIAL PRIMARY KEY,
	web_url           TEXT NOT NULL UNIQUE,
	client_id         TEXT NOT NULL,
	client_secret_enc BYTEA NOT NULL,
	created_at        TIMESTAMPTZ NOT NULL
);

--bun:split

CREATE TABLE git_connections (
	id         BIGSERIAL PRIMARY KEY,
	provider   TEXT NOT NULL,
	base_url   TEXT NOT NULL,
	username   TEXT NOT NULL,
	token_enc  BYTEA NOT NULL,
	created_at TIMESTAMPTZ NOT NULL,
	auth_type TEXT NOT NULL DEFAULT 'token',
	github_app_id BIGINT REFERENCES github_apps (id) ON DELETE CASCADE,
	installation_id BIGINT,
	gitlab_app_id BIGINT REFERENCES gitlab_apps (id) ON DELETE CASCADE,
	refresh_token_enc BYTEA,
	token_expires_at TIMESTAMPTZ,
	redirect_uri TEXT NOT NULL DEFAULT '',
	settings_url TEXT NOT NULL DEFAULT '',
	account_type TEXT NOT NULL DEFAULT ''
);

--bun:split

CREATE TABLE servers (
	id           BIGSERIAL PRIMARY KEY,
	name         TEXT NOT NULL UNIQUE,
	token_hash   TEXT NOT NULL UNIQUE,
	last_seen_at TIMESTAMPTZ,
	last_addr    TEXT NOT NULL DEFAULT '',
	version      TEXT NOT NULL DEFAULT '',
	created_at   TIMESTAMPTZ NOT NULL
);

--bun:split

CREATE TABLE proxy_hosts (
	id              BIGSERIAL PRIMARY KEY,
	domains         TEXT NOT NULL,
	target_type     TEXT NOT NULL,
	forward_scheme  TEXT NOT NULL,
	forward_host    TEXT NOT NULL,
	forward_port    BIGINT NOT NULL,
	ssl             BOOLEAN NOT NULL DEFAULT FALSE,
	force_https     BOOLEAN NOT NULL DEFAULT FALSE,
	hsts            BOOLEAN NOT NULL DEFAULT FALSE,
	skip_tls_verify BOOLEAN NOT NULL DEFAULT FALSE,
	preserve_host   BOOLEAN NOT NULL DEFAULT TRUE,
	enabled         BOOLEAN NOT NULL DEFAULT TRUE,
	created_at      TIMESTAMPTZ NOT NULL,
	updated_at      TIMESTAMPTZ NOT NULL,
	access_list_id BIGINT REFERENCES access_lists (id) ON DELETE SET NULL,
	locations TEXT NOT NULL DEFAULT '[]'
);

--bun:split

CREATE TABLE sites (
	id                   BIGSERIAL PRIMARY KEY,
	name                 TEXT NOT NULL UNIQUE,
	domains              TEXT NOT NULL,
	ssl                  BOOLEAN NOT NULL DEFAULT FALSE,
	force_https          BOOLEAN NOT NULL DEFAULT FALSE,
	hsts                 BOOLEAN NOT NULL DEFAULT FALSE,
	spa                  BOOLEAN NOT NULL DEFAULT FALSE,
	active_deployment_id BIGINT,
	created_at           TIMESTAMPTZ NOT NULL,
	updated_at           TIMESTAMPTZ NOT NULL,
	git_connection_id BIGINT REFERENCES git_connections (id) ON DELETE SET NULL,
	repo TEXT NOT NULL DEFAULT '',
	branch TEXT NOT NULL DEFAULT '',
	root_dir TEXT NOT NULL DEFAULT '',
	install_command TEXT NOT NULL DEFAULT '',
	build_command TEXT NOT NULL DEFAULT '',
	output_dir TEXT NOT NULL DEFAULT '',
	build_image TEXT NOT NULL DEFAULT '',
	build_env_enc BYTEA,
	webhook_id TEXT NOT NULL DEFAULT '',
	webhook_secret TEXT NOT NULL DEFAULT '',
	access_list_id BIGINT REFERENCES access_lists (id) ON DELETE SET NULL,
	previews BOOLEAN NOT NULL DEFAULT FALSE,
	locations TEXT NOT NULL DEFAULT '[]',
	deploy_hook TEXT NOT NULL DEFAULT ''
);

--bun:split

CREATE TABLE deployments (
	id          BIGSERIAL PRIMARY KEY,
	site_id     BIGINT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
	source      TEXT NOT NULL,
	file_count  BIGINT NOT NULL,
	total_bytes BIGINT NOT NULL,
	created_at  TIMESTAMPTZ NOT NULL,
	commit_sha TEXT NOT NULL DEFAULT '',
	commit_message TEXT NOT NULL DEFAULT '',
	branch TEXT NOT NULL DEFAULT '',
	preview TEXT NOT NULL DEFAULT ''
);

--bun:split

CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

--bun:split

CREATE TABLE builds (
	id             BIGSERIAL PRIMARY KEY,
	site_id        BIGINT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
	status         TEXT NOT NULL,
	trigger        TEXT NOT NULL,
	branch         TEXT NOT NULL,
	commit_sha     TEXT NOT NULL DEFAULT '',
	commit_message TEXT NOT NULL DEFAULT '',
	deployment_id  BIGINT,
	error          TEXT NOT NULL DEFAULT '',
	created_at     TIMESTAMPTZ NOT NULL,
	started_at     TIMESTAMPTZ,
	finished_at    TIMESTAMPTZ,
	preview TEXT NOT NULL DEFAULT ''
);

--bun:split

CREATE TABLE notification_channels (
	id           BIGSERIAL PRIMARY KEY,
	type         TEXT NOT NULL,
	name         TEXT NOT NULL,
	config_enc   BYTEA NOT NULL,
	events       TEXT NOT NULL,
	enabled      BOOLEAN NOT NULL DEFAULT TRUE,
	last_error   TEXT NOT NULL DEFAULT '',
	last_sent_at TIMESTAMPTZ,
	created_at   TIMESTAMPTZ NOT NULL
);

--bun:split

CREATE TABLE apps (
	id                BIGSERIAL PRIMARY KEY,
	name              TEXT NOT NULL UNIQUE,
	source            TEXT NOT NULL,
	image             TEXT NOT NULL DEFAULT '',
	git_connection_id BIGINT REFERENCES git_connections (id) ON DELETE SET NULL,
	repo              TEXT NOT NULL DEFAULT '',
	branch            TEXT NOT NULL DEFAULT '',
	root_dir          TEXT NOT NULL DEFAULT '',
	dockerfile        TEXT NOT NULL DEFAULT '',
	webhook_id        TEXT NOT NULL DEFAULT '',
	webhook_secret    TEXT NOT NULL DEFAULT '',
	port              BIGINT NOT NULL DEFAULT 0,
	domains           TEXT NOT NULL DEFAULT '[]',
	ssl               BOOLEAN NOT NULL DEFAULT FALSE,
	force_https       BOOLEAN NOT NULL DEFAULT FALSE,
	hsts              BOOLEAN NOT NULL DEFAULT FALSE,
	env_enc           BYTEA,
	volumes           TEXT NOT NULL DEFAULT '[]',
	command           TEXT NOT NULL DEFAULT '',
	memory_mb         BIGINT NOT NULL DEFAULT 0,
	cpus              DOUBLE PRECISION NOT NULL DEFAULT 0,
	health_path       TEXT NOT NULL DEFAULT '',
	stopped           BOOLEAN NOT NULL DEFAULT FALSE,
	current_deployment_id BIGINT,
	created_at        TIMESTAMPTZ NOT NULL,
	updated_at        TIMESTAMPTZ NOT NULL,
	compose_file TEXT NOT NULL DEFAULT '',
	compose_service TEXT NOT NULL DEFAULT '',
	access_list_id BIGINT REFERENCES access_lists (id) ON DELETE SET NULL,
	server_id BIGINT REFERENCES servers (id)
);

--bun:split

CREATE TABLE app_deployments (
	id             BIGSERIAL PRIMARY KEY,
	app_id         BIGINT NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
	status         TEXT NOT NULL,
	trigger        TEXT NOT NULL,
	image          TEXT NOT NULL DEFAULT '',
	container      TEXT NOT NULL DEFAULT '',
	commit_sha     TEXT NOT NULL DEFAULT '',
	commit_message TEXT NOT NULL DEFAULT '',
	error          TEXT NOT NULL DEFAULT '',
	created_at     TIMESTAMPTZ NOT NULL,
	started_at     TIMESTAMPTZ,
	finished_at    TIMESTAMPTZ
);

--bun:split

CREATE TABLE redirect_hosts (
	id            BIGSERIAL PRIMARY KEY,
	domains       TEXT NOT NULL,
	target        TEXT NOT NULL,
	permanent     BOOLEAN NOT NULL DEFAULT TRUE,
	preserve_path BOOLEAN NOT NULL DEFAULT TRUE,
	ssl           BOOLEAN NOT NULL DEFAULT FALSE,
	enabled       BOOLEAN NOT NULL DEFAULT TRUE,
	created_at    TIMESTAMPTZ NOT NULL,
	updated_at    TIMESTAMPTZ NOT NULL
);

--bun:split

CREATE TABLE streams (
	id           BIGSERIAL PRIMARY KEY,
	protocol     TEXT NOT NULL,
	listen_port  BIGINT NOT NULL,
	forward_host TEXT NOT NULL,
	forward_port BIGINT NOT NULL,
	enabled      BOOLEAN NOT NULL DEFAULT TRUE,
	created_at   TIMESTAMPTZ NOT NULL,
	UNIQUE (protocol, listen_port)
);

--bun:split

CREATE TABLE dns_providers (
	id         BIGSERIAL PRIMARY KEY,
	provider   TEXT NOT NULL UNIQUE,
	zones      TEXT NOT NULL,
	env_enc    BYTEA NOT NULL,
	created_at TIMESTAMPTZ NOT NULL
);

--bun:split

CREATE TABLE certificates (
	id         BIGSERIAL PRIMARY KEY,
	name       TEXT NOT NULL,
	domains    TEXT NOT NULL,
	issuer     TEXT NOT NULL DEFAULT '',
	not_after  TIMESTAMPTZ NOT NULL,
	cert_pem   TEXT NOT NULL,
	key_enc    BYTEA NOT NULL,
	created_at TIMESTAMPTZ NOT NULL
);

--bun:split

CREATE TABLE api_tokens (
	id           BIGSERIAL PRIMARY KEY,
	user_id      BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	name         TEXT NOT NULL,
	token_hash   TEXT NOT NULL UNIQUE,
	prefix       TEXT NOT NULL,
	role         TEXT NOT NULL,
	last_used_at TIMESTAMPTZ,
	expires_at   TIMESTAMPTZ,
	created_at   TIMESTAMPTZ NOT NULL
);

--bun:split

CREATE TABLE db_backup_policies (
	app_id         BIGINT PRIMARY KEY REFERENCES apps (id) ON DELETE CASCADE,
	enabled        BOOLEAN NOT NULL DEFAULT FALSE,
	interval_hours BIGINT NOT NULL DEFAULT 24,
	keep           BIGINT NOT NULL DEFAULT 7,
	updated_at     TIMESTAMPTZ NOT NULL
);

--bun:split

CREATE TABLE db_backups (
	id          BIGSERIAL PRIMARY KEY,
	app_id      BIGINT NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
	status      TEXT NOT NULL,
	trigger     TEXT NOT NULL,
	engine      TEXT NOT NULL,
	location    TEXT NOT NULL DEFAULT '',
	object_key  TEXT NOT NULL DEFAULT '',
	size        BIGINT NOT NULL DEFAULT 0,
	error       TEXT NOT NULL DEFAULT '',
	created_at  TIMESTAMPTZ NOT NULL,
	finished_at TIMESTAMPTZ
);

--bun:split

CREATE TABLE traffic_minutes (
	owner        TEXT NOT NULL,
	minute       BIGINT NOT NULL,
	requests     BIGINT NOT NULL,
	s4xx         BIGINT NOT NULL,
	s5xx         BIGINT NOT NULL,
	duration_sum DOUBLE PRECISION NOT NULL,
	buckets      TEXT NOT NULL,
	PRIMARY KEY (owner, minute)
);

--bun:split

CREATE TABLE app_jobs (
	id             BIGSERIAL PRIMARY KEY,
	app_id         BIGINT NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
	command        TEXT NOT NULL,
	schedule       TEXT NOT NULL,
	enabled        BOOLEAN NOT NULL DEFAULT TRUE,
	last_run_at    TIMESTAMPTZ,
	last_exit_code BIGINT,
	last_output    TEXT NOT NULL DEFAULT '',
	created_at     TIMESTAMPTZ NOT NULL
);

--bun:split

CREATE INDEX deployments_site_id ON deployments (site_id, id DESC);

--bun:split

CREATE INDEX builds_site_id ON builds (site_id, id DESC);

--bun:split

CREATE INDEX builds_status ON builds (status);

--bun:split

CREATE INDEX app_deployments_app ON app_deployments (app_id, id DESC);

--bun:split

CREATE INDEX app_deployments_status ON app_deployments (status);

--bun:split

CREATE INDEX deployments_preview ON deployments (site_id, preview);

--bun:split

CREATE INDEX api_tokens_user ON api_tokens (user_id);

--bun:split

CREATE INDEX db_backups_app ON db_backups (app_id, id);

--bun:split

CREATE INDEX traffic_minutes_minute ON traffic_minutes (minute);

--bun:split

CREATE INDEX app_jobs_app ON app_jobs (app_id, id);
