-- Listas de acceso: usuario/contraseña y/o IPs permitidas.
CREATE TABLE access_lists (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT NOT NULL,
	users      TEXT NOT NULL DEFAULT '[]', -- [{"username":"…","password_hash":"$2a$…"}]
	allow_ips  TEXT NOT NULL DEFAULT '[]', -- ["192.168.1.0/24", "203.0.113.7"]
	created_at DATETIME NOT NULL
);

--bun:split

ALTER TABLE proxy_hosts ADD COLUMN access_list_id INTEGER REFERENCES access_lists (id) ON DELETE SET NULL;

--bun:split

ALTER TABLE sites ADD COLUMN access_list_id INTEGER REFERENCES access_lists (id) ON DELETE SET NULL;

--bun:split

ALTER TABLE apps ADD COLUMN access_list_id INTEGER REFERENCES access_lists (id) ON DELETE SET NULL;
