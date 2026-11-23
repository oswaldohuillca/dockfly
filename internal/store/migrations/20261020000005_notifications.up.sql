CREATE TABLE notification_channels (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	type         TEXT NOT NULL, -- email | discord | slack | telegram | webhook
	name         TEXT NOT NULL,
	config_enc   BLOB NOT NULL, -- encrypted JSON: includes URLs with tokens, passwords…
	events       TEXT NOT NULL, -- JSON: list of subscribed events
	enabled      BOOLEAN NOT NULL DEFAULT TRUE,
	last_error   TEXT NOT NULL DEFAULT '',
	last_sent_at DATETIME,
	created_at   DATETIME NOT NULL
);
