CREATE TABLE notification_channels (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	type         TEXT NOT NULL, -- email | discord | slack | telegram | webhook
	name         TEXT NOT NULL,
	config_enc   BLOB NOT NULL, -- JSON cifrado: incluye URLs con token, contraseñas…
	events       TEXT NOT NULL, -- JSON: lista de eventos suscritos
	enabled      BOOLEAN NOT NULL DEFAULT TRUE,
	last_error   TEXT NOT NULL DEFAULT '',
	last_sent_at DATETIME,
	created_at   DATETIME NOT NULL
);
