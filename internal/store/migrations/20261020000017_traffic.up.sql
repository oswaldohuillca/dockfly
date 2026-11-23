-- Per-minute traffic of each proxy host, site and app (from Traefik's metrics).
CREATE TABLE traffic_minutes (
	owner        TEXT NOT NULL,    -- "ph-1", "site-2", "app-3"…
	minute       INTEGER NOT NULL, -- minutes since 1970 (UTC)
	requests     INTEGER NOT NULL,
	s4xx         INTEGER NOT NULL,
	s5xx         INTEGER NOT NULL,
	duration_sum REAL NOT NULL,    -- seconds
	buckets      TEXT NOT NULL,    -- latency histogram (JSON, cumulative)
	PRIMARY KEY (owner, minute)
) WITHOUT ROWID;

--bun:split

CREATE INDEX traffic_minutes_minute ON traffic_minutes (minute);
