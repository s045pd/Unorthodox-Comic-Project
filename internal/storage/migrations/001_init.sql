CREATE TABLE books (
    id            TEXT PRIMARY KEY,
    title         TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    hot           INTEGER NOT NULL DEFAULT 0,
    raw_url       TEXT NOT NULL DEFAULT '',
    image_url     TEXT NOT NULL DEFAULT '',
    cover_path    TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE TABLE episodes (
    id            INTEGER PRIMARY KEY,
    book_id       TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    title         TEXT NOT NULL DEFAULT '',
    raw_url       TEXT NOT NULL DEFAULT '',
    pdf_path      TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX idx_episodes_book ON episodes(book_id);

CREATE TABLE images (
    id            INTEGER PRIMARY KEY,
    episode_id    INTEGER NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
    idx           INTEGER NOT NULL DEFAULT 0,
    raw_url       TEXT NOT NULL DEFAULT '',
    file_path     TEXT NOT NULL DEFAULT '',
    width         INTEGER NOT NULL DEFAULT 0,
    height        INTEGER NOT NULL DEFAULT 0,
    bytes         INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX idx_images_episode ON images(episode_id, idx);
CREATE INDEX idx_images_missing ON images(file_path) WHERE file_path = '';

CREATE TABLE tags (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    name  TEXT NOT NULL UNIQUE
);

CREATE TABLE book_tags (
    book_id  TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    tag_id   INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (book_id, tag_id)
);

CREATE TABLE jobs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    kind          TEXT NOT NULL,
    payload       TEXT NOT NULL DEFAULT '{}',
    task_key      TEXT NOT NULL UNIQUE,
    status        TEXT NOT NULL DEFAULT 'pending',
    priority      INTEGER NOT NULL DEFAULT 0,
    attempts      INTEGER NOT NULL DEFAULT 0,
    max_attempts  INTEGER NOT NULL DEFAULT 3,
    last_error    TEXT NOT NULL DEFAULT '',
    run_at        INTEGER NOT NULL,
    started_at    INTEGER,
    finished_at   INTEGER,
    created_at    INTEGER NOT NULL
);
CREATE INDEX idx_jobs_ready ON jobs(status, run_at) WHERE status = 'pending';
CREATE INDEX idx_jobs_history ON jobs(kind, finished_at);

CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
    token       TEXT PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  INTEGER NOT NULL,
    created_at  INTEGER NOT NULL
);
CREATE INDEX idx_sessions_expiry ON sessions(expires_at);

CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);
