-- Deploy hook: a secret URL per site that triggers a build (for a CMS, a cron…).
ALTER TABLE sites ADD COLUMN deploy_hook TEXT NOT NULL DEFAULT '';
