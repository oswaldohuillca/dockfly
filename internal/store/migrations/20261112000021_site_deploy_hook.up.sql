-- Deploy hook: una URL secreta por sitio que lanza un build (para un CMS, un cron…).
ALTER TABLE sites ADD COLUMN deploy_hook TEXT NOT NULL DEFAULT '';
