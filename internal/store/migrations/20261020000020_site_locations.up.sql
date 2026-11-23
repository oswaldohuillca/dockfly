-- Custom locations of a site: /api → a container, everything else → the site.
ALTER TABLE sites ADD COLUMN locations TEXT NOT NULL DEFAULT '[]';
