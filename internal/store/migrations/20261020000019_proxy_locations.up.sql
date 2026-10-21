-- Ubicaciones personalizadas de un proxy host: /api → otro destino.
ALTER TABLE proxy_hosts ADD COLUMN locations TEXT NOT NULL DEFAULT '[]';
