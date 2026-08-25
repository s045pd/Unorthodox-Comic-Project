-- Per-user reading bookmarks. One bookmark per (user, book) — stores the
-- exact page (image index) within the chapter and a 0..1 fraction for the
-- precise scroll position inside that page, so "continue reading" lands
-- where the eye left off, not just at the chapter top.

CREATE TABLE bookmarks (
    user_id     INTEGER NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    book_id     TEXT    NOT NULL REFERENCES books(id)    ON DELETE CASCADE,
    episode_id  INTEGER NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
    image_idx   INTEGER NOT NULL DEFAULT 0,    -- which page (image) in the chapter
    scroll_pct  REAL    NOT NULL DEFAULT 0,    -- 0..1 fraction within that image
    total_pages INTEGER NOT NULL DEFAULT 0,    -- snapshot for "p/total" display
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (user_id, book_id)
);

CREATE INDEX idx_bookmarks_user_recent ON bookmarks(user_id, updated_at DESC);
CREATE INDEX idx_bookmarks_episode ON bookmarks(episode_id);
