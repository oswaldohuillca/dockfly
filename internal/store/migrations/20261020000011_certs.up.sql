-- DNS providers for Let's Encrypt's DNS-01 challenge (wildcard
-- certificates). Each one covers some zones; its credentials are encrypted.
CREATE TABLE dns_providers (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	provider   TEXT NOT NULL UNIQUE, -- lego's provider code: cloudflare, route53…
	zones      TEXT NOT NULL,        -- ["example.com"]
	env_enc    BLOB NOT NULL,        -- lego's environment variables, encrypted
	created_at DATETIME NOT NULL
);

--bun:split

-- Certificates uploaded by the user.
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
