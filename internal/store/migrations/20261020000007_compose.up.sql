-- Apps from a docker-compose.yml: the file and the service that receives the
-- web traffic.
ALTER TABLE apps ADD COLUMN compose_file TEXT NOT NULL DEFAULT '';

--bun:split

ALTER TABLE apps ADD COLUMN compose_service TEXT NOT NULL DEFAULT '';
