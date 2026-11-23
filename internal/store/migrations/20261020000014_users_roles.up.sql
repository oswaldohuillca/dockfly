-- Multiple users with roles. The ones that already existed are administrators.
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'admin';

--bun:split

-- Changes whenever the password changes: invalidates the other sessions.
ALTER TABLE users ADD COLUMN session_version INTEGER NOT NULL DEFAULT 0;
