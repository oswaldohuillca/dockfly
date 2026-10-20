-- Tráfico por minuto de cada proxy host, sitio y app (desde las métricas de Traefik).
CREATE TABLE traffic_minutes (
	owner        TEXT NOT NULL,    -- "ph-1", "site-2", "app-3"…
	minute       INTEGER NOT NULL, -- minutos desde 1970 (UTC)
	requests     INTEGER NOT NULL,
	s4xx         INTEGER NOT NULL,
	s5xx         INTEGER NOT NULL,
	duration_sum REAL NOT NULL,    -- segundos
	buckets      TEXT NOT NULL,    -- histograma de latencia (JSON, acumulado)
	PRIMARY KEY (owner, minute)
) WITHOUT ROWID;

--bun:split

CREATE INDEX traffic_minutes_minute ON traffic_minutes (minute);
