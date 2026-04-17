-- name: CreateUser :one
INSERT INTO users (username, password_hash, created_at)
VALUES (?, ?, ?)
RETURNING *;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = ?;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: CreateSession :exec
INSERT INTO sessions (token, user_id, expires_at, created_at)
VALUES (?, ?, ?, ?);

-- name: GetSessionByToken :one
SELECT s.*, u.username
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token = ? AND s.expires_at > ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < ?;

-- name: UpsertBook :exec
INSERT INTO books (id, title, description, hot, raw_url, image_url, cover_path, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    title=excluded.title,
    description=excluded.description,
    hot=excluded.hot,
    raw_url=excluded.raw_url,
    image_url=excluded.image_url,
    cover_path=CASE WHEN excluded.cover_path != '' THEN excluded.cover_path ELSE books.cover_path END,
    updated_at=excluded.updated_at;

-- name: GetBook :one
SELECT * FROM books WHERE id = ?;

-- name: ListBooks :many
SELECT * FROM books ORDER BY title LIMIT ? OFFSET ?;

-- name: CountBooks :one
SELECT COUNT(*) FROM books;

-- name: GetLatestEpisodeTitle :one
SELECT title FROM episodes WHERE book_id = ? ORDER BY id DESC LIMIT 1;

-- name: UpsertEpisode :exec
INSERT INTO episodes (id, book_id, title, raw_url, pdf_path, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    title=excluded.title,
    raw_url=excluded.raw_url,
    pdf_path=CASE WHEN excluded.pdf_path != '' THEN excluded.pdf_path ELSE episodes.pdf_path END,
    updated_at=excluded.updated_at;

-- name: GetEpisode :one
SELECT * FROM episodes WHERE id = ?;

-- name: ListEpisodesByBook :many
SELECT * FROM episodes WHERE book_id = ? ORDER BY id;

-- name: SetEpisodePDF :exec
UPDATE episodes SET pdf_path = ?, updated_at = ? WHERE id = ?;

-- name: UpsertImage :exec
INSERT INTO images (id, episode_id, idx, raw_url, file_path, width, height, bytes, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    idx=excluded.idx,
    raw_url=excluded.raw_url,
    file_path=CASE WHEN excluded.file_path != '' THEN excluded.file_path ELSE images.file_path END,
    width=CASE WHEN excluded.width > 0 THEN excluded.width ELSE images.width END,
    height=CASE WHEN excluded.height > 0 THEN excluded.height ELSE images.height END,
    bytes=CASE WHEN excluded.bytes > 0 THEN excluded.bytes ELSE images.bytes END,
    updated_at=excluded.updated_at;

-- name: GetImage :one
SELECT * FROM images WHERE id = ?;

-- name: ListImagesByEpisode :many
SELECT * FROM images WHERE episode_id = ? ORDER BY idx;

-- name: ListMissingImageIDs :many
SELECT id FROM images WHERE file_path = '' LIMIT ?;

-- name: SetImageFile :exec
UPDATE images SET file_path = ?, width = ?, height = ?, bytes = ?, updated_at = ? WHERE id = ?;

-- name: GetOrCreateTag :one
INSERT INTO tags (name) VALUES (?)
ON CONFLICT(name) DO UPDATE SET name=excluded.name
RETURNING *;

-- name: ListTags :many
SELECT t.*, (SELECT COUNT(*) FROM book_tags bt WHERE bt.tag_id = t.id) AS book_count
FROM tags t ORDER BY t.name;

-- name: LinkBookTag :exec
INSERT OR IGNORE INTO book_tags (book_id, tag_id) VALUES (?, ?);

-- name: EnqueueJob :exec
INSERT OR IGNORE INTO jobs (kind, payload, task_key, status, priority, max_attempts, run_at, created_at)
VALUES (?, ?, ?, 'pending', ?, ?, ?, ?);

-- name: FetchReadyJobs :many
SELECT * FROM jobs
WHERE status = 'pending' AND run_at <= ?
ORDER BY priority DESC, id ASC
LIMIT ?;

-- name: MarkJobRunning :exec
UPDATE jobs SET status='running', started_at=? WHERE id=? AND status='pending';

-- name: MarkJobDone :exec
UPDATE jobs SET status='done', finished_at=?, last_error='' WHERE id=?;

-- name: MarkJobFailed :exec
UPDATE jobs SET status='failed', finished_at=?, last_error=?, attempts=attempts+1 WHERE id=?;

-- name: RetryJob :exec
UPDATE jobs SET status='pending', run_at=?, last_error=?, attempts=attempts+1 WHERE id=?;

-- name: ResetRunningJobs :exec
UPDATE jobs SET status='pending', started_at=NULL WHERE status='running';

-- name: ListJobs :many
SELECT * FROM jobs ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = ?;

-- name: DeleteJob :exec
DELETE FROM jobs WHERE id = ?;
