-- Per-branch previews: the deployments of branches other than production.
ALTER TABLE sites ADD COLUMN previews BOOLEAN NOT NULL DEFAULT FALSE;

--bun:split

ALTER TABLE deployments ADD COLUMN preview TEXT NOT NULL DEFAULT ''; -- '' = production

--bun:split

ALTER TABLE builds ADD COLUMN preview TEXT NOT NULL DEFAULT '';

--bun:split

CREATE INDEX deployments_preview ON deployments (site_id, preview);
