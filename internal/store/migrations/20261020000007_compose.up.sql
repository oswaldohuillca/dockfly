-- Apps desde un docker-compose.yml: el archivo y el servicio que recibe el
-- tráfico web.
ALTER TABLE apps ADD COLUMN compose_file TEXT NOT NULL DEFAULT '';

--bun:split

ALTER TABLE apps ADD COLUMN compose_service TEXT NOT NULL DEFAULT '';
