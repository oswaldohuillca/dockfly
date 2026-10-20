CREATE TABLE streams (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	protocol     TEXT NOT NULL, -- tcp | udp
	listen_port  INTEGER NOT NULL,
	forward_host TEXT NOT NULL,
	forward_port INTEGER NOT NULL,
	enabled      BOOLEAN NOT NULL DEFAULT TRUE,
	created_at   DATETIME NOT NULL,
	UNIQUE (protocol, listen_port)
);
