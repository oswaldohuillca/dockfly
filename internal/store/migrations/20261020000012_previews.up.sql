-- Previews por rama: los despliegues de ramas que no son la de producción.
ALTER TABLE sites ADD COLUMN previews BOOLEAN NOT NULL DEFAULT FALSE;

--bun:split

ALTER TABLE deployments ADD COLUMN preview TEXT NOT NULL DEFAULT ''; -- '' = producción

--bun:split

ALTER TABLE builds ADD COLUMN preview TEXT NOT NULL DEFAULT '';

--bun:split

CREATE INDEX deployments_preview ON deployments (site_id, preview);
