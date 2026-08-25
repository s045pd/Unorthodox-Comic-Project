-- Multi-user support: each user has a role (admin / user) and active flag.
-- The first existing user (always created via EnsureFirstRunAdmin) is promoted
-- to admin. New users created via /admin/users default to role='user'.

ALTER TABLE users ADD COLUMN role   TEXT    NOT NULL DEFAULT 'user';
ALTER TABLE users ADD COLUMN active INTEGER NOT NULL DEFAULT 1;

UPDATE users SET role = 'admin' WHERE id = (SELECT MIN(id) FROM users);

CREATE INDEX IF NOT EXISTS idx_users_active ON users(active);
