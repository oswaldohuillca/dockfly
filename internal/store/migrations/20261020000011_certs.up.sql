-- Proveedores DNS para el desafío DNS-01 de Let's Encrypt (certificados
-- wildcard). Cada uno cubre unas zonas; sus credenciales van cifradas.
CREATE TABLE dns_providers (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	provider   TEXT NOT NULL UNIQUE, -- código de lego: cloudflare, route53…
	zones      TEXT NOT NULL,        -- ["ejemplo.com"]
	env_enc    BLOB NOT NULL,        -- variables de entorno de lego, cifradas
	created_at DATETIME NOT NULL
);

--bun:split

-- Certificados subidos por el usuario.
CREATE TABLE certificates (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT NOT NULL,
	domains    TEXT NOT NULL,
	issuer     TEXT NOT NULL DEFAULT '',
	not_after  DATETIME NOT NULL,
	cert_pem   TEXT NOT NULL,
	key_enc    BLOB NOT NULL,
	created_at DATETIME NOT NULL
);
