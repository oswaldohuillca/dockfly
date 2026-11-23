-- Custom locations of a proxy host: /api → a different target.
ALTER TABLE proxy_hosts ADD COLUMN locations TEXT NOT NULL DEFAULT '[]';
