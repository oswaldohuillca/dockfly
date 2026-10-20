-- Varios usuarios con roles. Los que ya existían son administradores.
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'admin';

--bun:split

-- Cambia al cambiar la contraseña: invalida las demás sesiones.
ALTER TABLE users ADD COLUMN session_version INTEGER NOT NULL DEFAULT 0;
