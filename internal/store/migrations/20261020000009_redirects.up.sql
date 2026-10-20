CREATE TABLE redirect_hosts (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	domains       TEXT NOT NULL,
	target        TEXT NOT NULL, -- https://nuevo.com o https://nuevo.com/landing
	permanent     BOOLEAN NOT NULL DEFAULT TRUE,
	preserve_path BOOLEAN NOT NULL DEFAULT TRUE,
	ssl           BOOLEAN NOT NULL DEFAULT FALSE,
	enabled       BOOLEAN NOT NULL DEFAULT TRUE,
	created_at    DATETIME NOT NULL,
	updated_at    DATETIME NOT NULL
);
