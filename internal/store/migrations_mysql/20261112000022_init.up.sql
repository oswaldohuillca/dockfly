-- Esquema inicial para MySQL y MariaDB, equivalente a las migraciones de SQLite
-- (../migrations) hasta 20261112000022. Cada migración nueva necesita su
-- archivo en las tres carpetas.

-- utf8mb4_bin: compara como SQLite y PostgreSQL (distingue mayúsculas).

CREATE TABLE users (
	id            BIGINT PRIMARY KEY AUTO_INCREMENT,
	email         VARCHAR(255) NOT NULL UNIQUE,
	name          LONGTEXT NOT NULL,
	password_hash LONGTEXT NOT NULL,
	created_at    DATETIME(6) NOT NULL,
	totp_secret_enc LONGBLOB,
	totp_enabled BOOLEAN NOT NULL DEFAULT FALSE,
	totp_last_step BIGINT NOT NULL DEFAULT 0,
	recovery_codes LONGTEXT NOT NULL DEFAULT ('[]'),
	role LONGTEXT NOT NULL DEFAULT ('admin'),
	session_version BIGINT NOT NULL DEFAULT 0
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE access_lists (
	id         BIGINT PRIMARY KEY AUTO_INCREMENT,
	name       LONGTEXT NOT NULL,
	users      LONGTEXT NOT NULL DEFAULT ('[]'),
	allow_ips  LONGTEXT NOT NULL DEFAULT ('[]'),
	created_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE github_apps (
	id                 BIGINT PRIMARY KEY AUTO_INCREMENT,
	web_url            VARCHAR(255) NOT NULL,
	api_url            LONGTEXT NOT NULL,
	app_id             BIGINT NOT NULL,
	slug               LONGTEXT NOT NULL,
	name               LONGTEXT NOT NULL,
	owner              VARCHAR(255) NOT NULL,
	html_url           LONGTEXT NOT NULL,
	client_id          LONGTEXT NOT NULL,
	client_secret_enc  LONGBLOB NOT NULL,
	webhook_secret_enc LONGBLOB,
	private_key_enc    LONGBLOB NOT NULL,
	webhook_active     BOOLEAN NOT NULL DEFAULT FALSE,
	created_at         DATETIME(6) NOT NULL,
	UNIQUE (web_url, app_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE gitlab_apps (
	id                BIGINT PRIMARY KEY AUTO_INCREMENT,
	web_url           VARCHAR(255) NOT NULL UNIQUE,
	client_id         LONGTEXT NOT NULL,
	client_secret_enc LONGBLOB NOT NULL,
	created_at        DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE git_connections (
	id         BIGINT PRIMARY KEY AUTO_INCREMENT,
	provider   VARCHAR(255) NOT NULL,
	base_url   LONGTEXT NOT NULL,
	username   LONGTEXT NOT NULL,
	token_enc  LONGBLOB NOT NULL,
	created_at DATETIME(6) NOT NULL,
	auth_type LONGTEXT NOT NULL DEFAULT ('token'),
	github_app_id BIGINT,
	installation_id BIGINT,
	gitlab_app_id BIGINT,
	refresh_token_enc LONGBLOB,
	token_expires_at DATETIME(6),
	redirect_uri LONGTEXT NOT NULL DEFAULT (''),
	settings_url LONGTEXT NOT NULL DEFAULT (''),
	account_type LONGTEXT NOT NULL DEFAULT (''),
	FOREIGN KEY (github_app_id) REFERENCES github_apps (id) ON DELETE CASCADE,
	FOREIGN KEY (gitlab_app_id) REFERENCES gitlab_apps (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE servers (
	id           BIGINT PRIMARY KEY AUTO_INCREMENT,
	name         VARCHAR(255) NOT NULL UNIQUE,
	token_hash   VARCHAR(255) NOT NULL UNIQUE,
	last_seen_at DATETIME(6),
	last_addr    LONGTEXT NOT NULL DEFAULT (''),
	version      LONGTEXT NOT NULL DEFAULT (''),
	created_at   DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE proxy_hosts (
	id              BIGINT PRIMARY KEY AUTO_INCREMENT,
	domains         LONGTEXT NOT NULL,
	target_type     LONGTEXT NOT NULL,
	forward_scheme  LONGTEXT NOT NULL,
	forward_host    LONGTEXT NOT NULL,
	forward_port    BIGINT NOT NULL,
	`ssl`             BOOLEAN NOT NULL DEFAULT FALSE,
	force_https     BOOLEAN NOT NULL DEFAULT FALSE,
	hsts            BOOLEAN NOT NULL DEFAULT FALSE,
	skip_tls_verify BOOLEAN NOT NULL DEFAULT FALSE,
	preserve_host   BOOLEAN NOT NULL DEFAULT TRUE,
	enabled         BOOLEAN NOT NULL DEFAULT TRUE,
	created_at      DATETIME(6) NOT NULL,
	updated_at      DATETIME(6) NOT NULL,
	access_list_id BIGINT,
	locations LONGTEXT NOT NULL DEFAULT ('[]'),
	FOREIGN KEY (access_list_id) REFERENCES access_lists (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE sites (
	id                   BIGINT PRIMARY KEY AUTO_INCREMENT,
	name                 VARCHAR(255) NOT NULL UNIQUE,
	domains              LONGTEXT NOT NULL,
	`ssl`                  BOOLEAN NOT NULL DEFAULT FALSE,
	force_https          BOOLEAN NOT NULL DEFAULT FALSE,
	hsts                 BOOLEAN NOT NULL DEFAULT FALSE,
	spa                  BOOLEAN NOT NULL DEFAULT FALSE,
	active_deployment_id BIGINT,
	created_at           DATETIME(6) NOT NULL,
	updated_at           DATETIME(6) NOT NULL,
	git_connection_id BIGINT,
	repo LONGTEXT NOT NULL DEFAULT (''),
	branch LONGTEXT NOT NULL DEFAULT (''),
	root_dir LONGTEXT NOT NULL DEFAULT (''),
	install_command LONGTEXT NOT NULL DEFAULT (''),
	build_command LONGTEXT NOT NULL DEFAULT (''),
	output_dir LONGTEXT NOT NULL DEFAULT (''),
	build_image LONGTEXT NOT NULL DEFAULT (''),
	build_env_enc LONGBLOB,
	webhook_id LONGTEXT NOT NULL DEFAULT (''),
	webhook_secret LONGTEXT NOT NULL DEFAULT (''),
	access_list_id BIGINT,
	previews BOOLEAN NOT NULL DEFAULT FALSE,
	locations LONGTEXT NOT NULL DEFAULT ('[]'),
	deploy_hook LONGTEXT NOT NULL DEFAULT (''),
	FOREIGN KEY (git_connection_id) REFERENCES git_connections (id) ON DELETE SET NULL,
	FOREIGN KEY (access_list_id) REFERENCES access_lists (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE deployments (
	id          BIGINT PRIMARY KEY AUTO_INCREMENT,
	site_id     BIGINT NOT NULL,
	source      LONGTEXT NOT NULL,
	file_count  BIGINT NOT NULL,
	total_bytes BIGINT NOT NULL,
	created_at  DATETIME(6) NOT NULL,
	commit_sha LONGTEXT NOT NULL DEFAULT (''),
	commit_message LONGTEXT NOT NULL DEFAULT (''),
	branch LONGTEXT NOT NULL DEFAULT (''),
	preview VARCHAR(255) NOT NULL DEFAULT '',
	FOREIGN KEY (site_id) REFERENCES sites (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE settings (
	`key`   VARCHAR(255) PRIMARY KEY,
	value LONGTEXT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE builds (
	id             BIGINT PRIMARY KEY AUTO_INCREMENT,
	site_id        BIGINT NOT NULL,
	status         VARCHAR(255) NOT NULL,
	`trigger`        LONGTEXT NOT NULL,
	branch         LONGTEXT NOT NULL,
	commit_sha     LONGTEXT NOT NULL DEFAULT (''),
	commit_message LONGTEXT NOT NULL DEFAULT (''),
	deployment_id  BIGINT,
	error          LONGTEXT NOT NULL DEFAULT (''),
	created_at     DATETIME(6) NOT NULL,
	started_at     DATETIME(6),
	finished_at    DATETIME(6),
	preview VARCHAR(255) NOT NULL DEFAULT '',
	FOREIGN KEY (site_id) REFERENCES sites (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE notification_channels (
	id           BIGINT PRIMARY KEY AUTO_INCREMENT,
	type         LONGTEXT NOT NULL,
	name         LONGTEXT NOT NULL,
	config_enc   LONGBLOB NOT NULL,
	events       LONGTEXT NOT NULL,
	enabled      BOOLEAN NOT NULL DEFAULT TRUE,
	last_error   LONGTEXT NOT NULL DEFAULT (''),
	last_sent_at DATETIME(6),
	created_at   DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE apps (
	id                BIGINT PRIMARY KEY AUTO_INCREMENT,
	name              VARCHAR(255) NOT NULL UNIQUE,
	source            LONGTEXT NOT NULL,
	image             LONGTEXT NOT NULL DEFAULT (''),
	git_connection_id BIGINT,
	repo              LONGTEXT NOT NULL DEFAULT (''),
	branch            LONGTEXT NOT NULL DEFAULT (''),
	root_dir          LONGTEXT NOT NULL DEFAULT (''),
	dockerfile        LONGTEXT NOT NULL DEFAULT (''),
	webhook_id        LONGTEXT NOT NULL DEFAULT (''),
	webhook_secret    LONGTEXT NOT NULL DEFAULT (''),
	port              BIGINT NOT NULL DEFAULT 0,
	domains           LONGTEXT NOT NULL DEFAULT ('[]'),
	`ssl`               BOOLEAN NOT NULL DEFAULT FALSE,
	force_https       BOOLEAN NOT NULL DEFAULT FALSE,
	hsts              BOOLEAN NOT NULL DEFAULT FALSE,
	env_enc           LONGBLOB,
	volumes           LONGTEXT NOT NULL DEFAULT ('[]'),
	command           LONGTEXT NOT NULL DEFAULT (''),
	memory_mb         BIGINT NOT NULL DEFAULT 0,
	cpus              DOUBLE NOT NULL DEFAULT 0,
	health_path       LONGTEXT NOT NULL DEFAULT (''),
	stopped           BOOLEAN NOT NULL DEFAULT FALSE,
	current_deployment_id BIGINT,
	created_at        DATETIME(6) NOT NULL,
	updated_at        DATETIME(6) NOT NULL,
	compose_file LONGTEXT NOT NULL DEFAULT (''),
	compose_service LONGTEXT NOT NULL DEFAULT (''),
	access_list_id BIGINT,
	server_id BIGINT,
	FOREIGN KEY (git_connection_id) REFERENCES git_connections (id) ON DELETE SET NULL,
	FOREIGN KEY (access_list_id) REFERENCES access_lists (id) ON DELETE SET NULL,
	FOREIGN KEY (server_id) REFERENCES servers (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE app_deployments (
	id             BIGINT PRIMARY KEY AUTO_INCREMENT,
	app_id         BIGINT NOT NULL,
	status         VARCHAR(255) NOT NULL,
	`trigger`        LONGTEXT NOT NULL,
	image          LONGTEXT NOT NULL DEFAULT (''),
	container      LONGTEXT NOT NULL DEFAULT (''),
	commit_sha     LONGTEXT NOT NULL DEFAULT (''),
	commit_message LONGTEXT NOT NULL DEFAULT (''),
	error          LONGTEXT NOT NULL DEFAULT (''),
	created_at     DATETIME(6) NOT NULL,
	started_at     DATETIME(6),
	finished_at    DATETIME(6),
	FOREIGN KEY (app_id) REFERENCES apps (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE redirect_hosts (
	id            BIGINT PRIMARY KEY AUTO_INCREMENT,
	domains       LONGTEXT NOT NULL,
	target        LONGTEXT NOT NULL,
	permanent     BOOLEAN NOT NULL DEFAULT TRUE,
	preserve_path BOOLEAN NOT NULL DEFAULT TRUE,
	`ssl`           BOOLEAN NOT NULL DEFAULT FALSE,
	enabled       BOOLEAN NOT NULL DEFAULT TRUE,
	created_at    DATETIME(6) NOT NULL,
	updated_at    DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE streams (
	id           BIGINT PRIMARY KEY AUTO_INCREMENT,
	protocol     VARCHAR(255) NOT NULL,
	listen_port  BIGINT NOT NULL,
	forward_host LONGTEXT NOT NULL,
	forward_port BIGINT NOT NULL,
	enabled      BOOLEAN NOT NULL DEFAULT TRUE,
	created_at   DATETIME(6) NOT NULL,
	UNIQUE (protocol, listen_port)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE dns_providers (
	id         BIGINT PRIMARY KEY AUTO_INCREMENT,
	provider   VARCHAR(255) NOT NULL UNIQUE,
	zones      LONGTEXT NOT NULL,
	env_enc    LONGBLOB NOT NULL,
	created_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE certificates (
	id         BIGINT PRIMARY KEY AUTO_INCREMENT,
	name       LONGTEXT NOT NULL,
	domains    LONGTEXT NOT NULL,
	issuer     LONGTEXT NOT NULL DEFAULT (''),
	not_after  DATETIME(6) NOT NULL,
	cert_pem   LONGTEXT NOT NULL,
	key_enc    LONGBLOB NOT NULL,
	created_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE api_tokens (
	id           BIGINT PRIMARY KEY AUTO_INCREMENT,
	user_id      BIGINT NOT NULL,
	name         LONGTEXT NOT NULL,
	token_hash   VARCHAR(255) NOT NULL UNIQUE,
	prefix       LONGTEXT NOT NULL,
	role         LONGTEXT NOT NULL,
	last_used_at DATETIME(6),
	expires_at   DATETIME(6),
	created_at   DATETIME(6) NOT NULL,
	FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE db_backup_policies (
	app_id         BIGINT PRIMARY KEY,
	enabled        BOOLEAN NOT NULL DEFAULT FALSE,
	interval_hours BIGINT NOT NULL DEFAULT 24,
	keep           BIGINT NOT NULL DEFAULT 7,
	updated_at     DATETIME(6) NOT NULL,
	FOREIGN KEY (app_id) REFERENCES apps (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE db_backups (
	id          BIGINT PRIMARY KEY AUTO_INCREMENT,
	app_id      BIGINT NOT NULL,
	status      VARCHAR(255) NOT NULL,
	`trigger`     LONGTEXT NOT NULL,
	engine      LONGTEXT NOT NULL,
	location    LONGTEXT NOT NULL DEFAULT (''),
	object_key  LONGTEXT NOT NULL DEFAULT (''),
	size        BIGINT NOT NULL DEFAULT 0,
	error       LONGTEXT NOT NULL DEFAULT (''),
	created_at  DATETIME(6) NOT NULL,
	finished_at DATETIME(6),
	FOREIGN KEY (app_id) REFERENCES apps (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE traffic_minutes (
	owner        VARCHAR(255) NOT NULL,
	minute       BIGINT NOT NULL,
	requests     BIGINT NOT NULL,
	s4xx         BIGINT NOT NULL,
	s5xx         BIGINT NOT NULL,
	duration_sum DOUBLE NOT NULL,
	buckets      LONGTEXT NOT NULL,
	PRIMARY KEY (owner, minute)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

--bun:split

CREATE TABLE app_jobs (
	id             BIGINT PRIMARY KEY AUTO_INCREMENT,
	app_id         BIGINT NOT NULL,
	command        LONGTEXT NOT NULL,
	schedule       LONGTEXT NOT NULL,
	enabled        BOOLEAN NOT NULL DEFAULT TRUE,
	last_run_at    DATETIME(6),
	last_exit_code BIGINT,
	last_output    LONGTEXT NOT NULL DEFAULT (''),
	created_at     DATETIME(6) NOT NULL,
	FOREIGN KEY (app_id) REFERENCES apps (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

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
