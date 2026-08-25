package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Bookmark records a user's last reading position within a book.
//
// Position has three layers of granularity:
//   - EpisodeID: which chapter
//   - ImageIdx:  which page (image) within that chapter
//   - ScrollPct: 0..1 fraction of pixels scrolled within that image
//
// "Continue reading" should restore exactly where the user left off, including
// mid-image scroll, so they don't have to hunt for the panel they were on.
type Bookmark struct {
	UserID     int64
	BookID     string
	BookTitle  string // joined from books for display convenience
	EpisodeID  int64
	EpTitle    string // joined from episodes for display
	ImageIdx   int
	ScrollPct  float64
	TotalPages int
	UpdatedAt  time.Time
}

// UpsertBookmark writes (or replaces) the user's bookmark for a book.
// Per (user, book) primary key — we always overwrite the previous spot.
func (s *Store) UpsertBookmark(ctx context.Context, b Bookmark) error {
	if b.UserID == 0 || b.BookID == "" || b.EpisodeID == 0 {
		return errors.New("bookmark missing required fields")
	}
	if b.ScrollPct < 0 {
		b.ScrollPct = 0
	}
	if b.ScrollPct > 1 {
		b.ScrollPct = 1
	}
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO bookmarks (user_id, book_id, episode_id, image_idx, scroll_pct, total_pages, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, book_id) DO UPDATE SET
			episode_id=excluded.episode_id,
			image_idx=excluded.image_idx,
			scroll_pct=excluded.scroll_pct,
			total_pages=excluded.total_pages,
			updated_at=excluded.updated_at`,
		b.UserID, b.BookID, b.EpisodeID, b.ImageIdx, b.ScrollPct, b.TotalPages, now)
	return err
}

// GetBookmark returns the user's bookmark for a single book, or nil if none.
func (s *Store) GetBookmark(ctx context.Context, userID int64, bookID string) (*Bookmark, error) {
	var bm Bookmark
	var updated int64
	err := s.db.QueryRowContext(ctx, `
		SELECT bm.user_id, bm.book_id, b.title, bm.episode_id, COALESCE(e.title,''),
		       bm.image_idx, bm.scroll_pct, bm.total_pages, bm.updated_at
		FROM bookmarks bm
		LEFT JOIN books b ON b.id = bm.book_id
		LEFT JOIN episodes e ON e.id = bm.episode_id
		WHERE bm.user_id=? AND bm.book_id=?`,
		userID, bookID).
		Scan(&bm.UserID, &bm.BookID, &bm.BookTitle, &bm.EpisodeID, &bm.EpTitle,
			&bm.ImageIdx, &bm.ScrollPct, &bm.TotalPages, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	bm.UpdatedAt = time.Unix(updated, 0)
	return &bm, nil
}

// ListBookmarks returns the user's bookmarks ordered by recency.
func (s *Store) ListBookmarks(ctx context.Context, userID int64, limit int) ([]Bookmark, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT bm.user_id, bm.book_id, COALESCE(b.title,''), bm.episode_id,
		       COALESCE(e.title,''), bm.image_idx, bm.scroll_pct, bm.total_pages,
		       bm.updated_at
		FROM bookmarks bm
		LEFT JOIN books b ON b.id = bm.book_id
		LEFT JOIN episodes e ON e.id = bm.episode_id
		WHERE bm.user_id=?
		ORDER BY bm.updated_at DESC
		LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bookmark
	for rows.Next() {
		var bm Bookmark
		var updated int64
		if err := rows.Scan(&bm.UserID, &bm.BookID, &bm.BookTitle, &bm.EpisodeID,
			&bm.EpTitle, &bm.ImageIdx, &bm.ScrollPct, &bm.TotalPages, &updated); err != nil {
			return nil, err
		}
		bm.UpdatedAt = time.Unix(updated, 0)
		out = append(out, bm)
	}
	return out, rows.Err()
}

// DeleteBookmark drops a single bookmark.
func (s *Store) DeleteBookmark(ctx context.Context, userID int64, bookID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM bookmarks WHERE user_id=? AND book_id=?`, userID, bookID)
	return err
}
