package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/s045pd/se8/internal/crawler"
)

// Deps is the dependency bundle handlers need.
type Deps struct {
	DB        *sql.DB
	Queue     *Queue
	Extractor *crawler.Extractor
	Client    *crawler.Client
	VolDir    string
	MaxPage   int
}

// mediaPath returns an absolute path under vol/media for the given relative key.
func (d *Deps) mediaPath(rel string) string {
	return filepath.Join(d.VolDir, "media", rel)
}

// writeFile ensures parent dir exists, then writes bytes atomically.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type findEpisodesPayload struct {
	BookID string `json:"book_id"`
}
type findImagesPayload struct {
	EpisodeID int64 `json:"episode_id"`
	Force     bool  `json:"force,omitempty"`
}
type downloadImagePayload struct {
	ImageID int64 `json:"image_id"`
}
type convertPDFPayload struct {
	EpisodeID int64 `json:"episode_id"`
	Force     bool  `json:"force,omitempty"`
}

// HandleFindBooks walks the category pages and upserts books; for each new
// or outdated book it enqueues a find_episodes job.
func (d *Deps) HandleFindBooks(ctx context.Context, _ json.RawMessage) error {
	maxPage, err := d.Extractor.FetchMaxPage(ctx)
	if err != nil {
		return fmt.Errorf("max page: %w", err)
	}
	if maxPage <= 0 || maxPage > d.MaxPage {
		maxPage = d.MaxPage
	}

	for page := 1; page <= maxPage; page++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		books, err := d.Extractor.FetchBooks(ctx, page)
		if err != nil {
			return fmt.Errorf("page %d: %w", page, err)
		}
		if len(books) == 0 {
			break
		}

		for _, b := range books {
			now := time.Now().Unix()
			if _, err := d.DB.ExecContext(ctx, `
				INSERT INTO books (id, title, raw_url, image_url, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET
					title=excluded.title,
					raw_url=excluded.raw_url,
					image_url=excluded.image_url,
					updated_at=excluded.updated_at`,
				b.ID, b.Title, b.RawURL, b.ImageURL, now, now); err != nil {
				return fmt.Errorf("upsert book %s: %w", b.ID, err)
			}

			// Check outdated: no episodes yet OR last episode title != current
			var lastTitle sql.NullString
			_ = d.DB.QueryRowContext(ctx,
				`SELECT title FROM episodes WHERE book_id=? ORDER BY id DESC LIMIT 1`,
				b.ID).Scan(&lastTitle)

			outdated := !lastTitle.Valid || lastTitle.String != b.Current
			if outdated {
				_ = d.Queue.Enqueue(ctx, KindFindEpisodes,
					fmt.Sprintf("find_episodes:%s", b.ID),
					findEpisodesPayload{BookID: b.ID},
					WithDelay(time.Duration(5+len(b.ID)%5)*time.Second),
				)
			}
		}
	}
	return nil
}
