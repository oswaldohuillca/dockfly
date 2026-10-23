-- Ubicaciones personalizadas de un sitio: /api → un contenedor, el resto, el sitio.
ALTER TABLE sites ADD COLUMN locations TEXT NOT NULL DEFAULT '[]';
