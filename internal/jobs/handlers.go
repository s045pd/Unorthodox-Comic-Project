package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/s045pd/se8/internal/crawler"
	"github.com/s045pd/se8/internal/imaging"
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

	// Per-page failures are isolated: one bad page shouldn't kill the whole sweep.
	// Same for per-book DB errors. Return error only if EVERY page failed.
	var pageOK, pageErr, bookOK, bookErr int
	for page := 1; page <= maxPage; page++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		books, err := d.Extractor.FetchBooks(ctx, page)
		if err != nil {
			pageErr++
			slog.Warn("find_books: page failed, continuing", "page", page, "err", err)
			continue
		}
		pageOK++
		if len(books) == 0 {
			break
		}

		for _, b := range books {
			now := time.Now().Unix()
			// Hot from listing card overrides only if non-zero, so we never
			// overwrite a richer value previously sourced from the detail page.
			if _, err := d.DB.ExecContext(ctx, `
				INSERT INTO books (id, title, raw_url, image_url, hot, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET
					title=excluded.title,
					raw_url=excluded.raw_url,
					image_url=excluded.image_url,
					hot=CASE WHEN excluded.hot > 0 THEN excluded.hot ELSE books.hot END,
					updated_at=excluded.updated_at`,
				b.ID, b.Title, b.RawURL, b.ImageURL, b.Hot, now, now); err != nil {
				bookErr++
				slog.Warn("find_books: book upsert failed, continuing",
					"id", b.ID, "err", err)
				continue
			}
			bookOK++

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
					WithPriority(1),
					WithDelay(time.Duration(5+len(b.ID)%5)*time.Second),
				)
			}
		}
	}
	slog.Info("find_books done",
		"max_page", maxPage,
		"pages_ok", pageOK, "pages_failed", pageErr,
		"books_ok", bookOK, "books_failed", bookErr)
	if pageOK == 0 {
		return fmt.Errorf("all %d pages failed (e.g. %d errors)", maxPage, pageErr)
	}
	return nil
}

// Register wires every handler onto runner r using deps d.
func Register(r *Runner, d *Deps) {
	r.Register(KindFindBooks, d.HandleFindBooks)
	r.Register(KindFindEpisodes, d.HandleFindEpisodes)
	r.Register(KindFindImages, d.HandleFindImages)
	r.Register(KindDownloadImage, d.HandleDownloadImage)
	r.Register(KindConvertPDF, d.HandleConvertPDF)
	r.Register(KindFixImages, d.HandleFixImages)
	r.Register(KindFixPDF, d.HandleFixPDF)
}

func (d *Deps) HandleFindEpisodes(ctx context.Context, payload json.RawMessage) error {
	var p findEpisodesPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	if p.BookID == "" {
		return errors.New("missing book_id")
	}

	var rawURL string
	if err := d.DB.QueryRowContext(ctx,
		`SELECT raw_url FROM books WHERE id=?`, p.BookID).Scan(&rawURL); err != nil {
		return fmt.Errorf("book lookup: %w", err)
	}

	meta, eps, err := d.Extractor.FetchEpisodes(ctx, rawURL)
	if err != nil {
		return err
	}

	// Update book metadata
	now := time.Now().Unix()
	if _, err := d.DB.ExecContext(ctx,
		`UPDATE books SET hot=?, description=?, updated_at=? WHERE id=?`,
		meta.Hot, meta.Description, now, p.BookID); err != nil {
		return err
	}
	// Tag upserts are best-effort: a single bad tag shouldn't kill the whole task.
	for _, tagName := range meta.Tags {
		var tagID int64
		err := d.DB.QueryRowContext(ctx,
			`INSERT INTO tags(name) VALUES(?) ON CONFLICT(name) DO UPDATE SET name=excluded.name RETURNING id`,
			tagName).Scan(&tagID)
		if err != nil {
			slog.Warn("find_episodes: tag upsert failed, continuing",
				"book", p.BookID, "tag", tagName, "err", err)
			continue
		}
		if _, err := d.DB.ExecContext(ctx,
			`INSERT OR IGNORE INTO book_tags(book_id, tag_id) VALUES(?, ?)`,
			p.BookID, tagID); err != nil {
			slog.Warn("find_episodes: link book_tag failed, continuing",
				"book", p.BookID, "tag", tagName, "err", err)
		}
	}

	// Upsert episodes; per-episode failure is isolated.
	var epOK, epErr int
	for _, ep := range eps {
		_, err := d.DB.ExecContext(ctx, `
			INSERT INTO episodes (id, book_id, title, raw_url, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET title=excluded.title, raw_url=excluded.raw_url, updated_at=excluded.updated_at`,
			ep.ID, p.BookID, ep.Title, ep.RawURL, now, now)
		if err != nil {
			epErr++
			slog.Warn("find_episodes: episode upsert failed, continuing",
				"book", p.BookID, "ep", ep.ID, "err", err)
			continue
		}
		epOK++
		var imgCount int
		d.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM images WHERE episode_id=?`, ep.ID).Scan(&imgCount)
		if imgCount == 0 {
			_ = d.Queue.Enqueue(ctx, KindFindImages,
				fmt.Sprintf("find_images:%d", ep.ID),
				findImagesPayload{EpisodeID: ep.ID},
				WithPriority(2),
				WithDelay(5*time.Second))
		}
	}
	if epOK == 0 && epErr > 0 {
		return fmt.Errorf("all %d episodes failed to upsert", epErr)
	}
	return nil
}

func (d *Deps) HandleFindImages(ctx context.Context, payload json.RawMessage) error {
	var p findImagesPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}
	if p.EpisodeID == 0 {
		return errors.New("missing episode_id")
	}

	var rawURL string
	if err := d.DB.QueryRowContext(ctx,
		`SELECT raw_url FROM episodes WHERE id=?`, p.EpisodeID).Scan(&rawURL); err != nil {
		return err
	}
	imgs, err := d.Extractor.FetchImages(ctx, rawURL)
	if err != nil {
		return err
	}

	now := time.Now().Unix()
	var imgOK, imgErr int
	for _, img := range imgs {
		_, err := d.DB.ExecContext(ctx, `
			INSERT INTO images (id, episode_id, idx, raw_url, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET idx=excluded.idx, raw_url=excluded.raw_url, updated_at=excluded.updated_at`,
			img.ID, p.EpisodeID, img.Index, img.RawURL, now, now)
		if err != nil {
			imgErr++
			slog.Warn("find_images: image upsert failed, continuing",
				"ep", p.EpisodeID, "img", img.ID, "idx", img.Index, "err", err)
			continue
		}
		imgOK++

		var filePath string
		d.DB.QueryRowContext(ctx, `SELECT file_path FROM images WHERE id=?`, img.ID).Scan(&filePath)

		if p.Force || filePath == "" {
			_ = d.Queue.Enqueue(ctx, KindDownloadImage,
				fmt.Sprintf("download_image:%d", img.ID),
				downloadImagePayload{ImageID: img.ID},
				WithPriority(5),
				WithDelay(time.Duration(img.Index%5)*time.Second))
		}
	}
	if imgOK == 0 && imgErr > 0 {
		return fmt.Errorf("all %d images failed to upsert", imgErr)
	}
	return nil
}

func (d *Deps) HandleDownloadImage(ctx context.Context, payload json.RawMessage) error {
	var p downloadImagePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}

	var rawURL string
	var episodeID int64
	var index int
	var bookID string
	err := d.DB.QueryRowContext(ctx, `
		SELECT i.raw_url, i.episode_id, i.idx, e.book_id
		FROM images i JOIN episodes e ON e.id = i.episode_id
		WHERE i.id=?`, p.ImageID).
		Scan(&rawURL, &episodeID, &index, &bookID)
	if err != nil {
		return fmt.Errorf("image lookup: %w", err)
	}
	rawURL = strings.TrimSpace(rawURL) // self-heal stale rows with trailing whitespace
	if rawURL == "" {
		return errors.New("image has no raw_url")
	}

	data, ext, err := d.Client.DownloadImage(ctx, rawURL)
	if err != nil {
		return err
	}
	if detected := crawler.ExtFromMagic(data); detected != "" {
		ext = detected
	}

	// PNG → JPEG q=82 transcode (PNG manga pages are wastefully large; JPEG is
	// usually 5-10x smaller with imperceptible quality loss for screen-tone art).
	if ext == "png" {
		if jpegBytes, ok := transcodePNGtoJPEG(data, 82); ok {
			data = jpegBytes
			ext = "jpg"
		}
	}

	rel := filepath.Join("books",
		sanitizeID(bookID),
		fmt.Sprintf("%d", episodeID),
		fmt.Sprintf("%03d.%s", index, ext))
	full := d.mediaPath(rel)
	if err := writeFile(full, data); err != nil {
		return fmt.Errorf("write image: %w", err)
	}

	if _, err := d.DB.ExecContext(ctx, `
		UPDATE images SET file_path=?, bytes=?, updated_at=? WHERE id=?`,
		filepath.ToSlash(filepath.Join("media", rel)), len(data), time.Now().Unix(), p.ImageID); err != nil {
		return err
	}
	return nil
}

// transcodePNGtoJPEG decodes a PNG and re-encodes as JPEG at the given quality.
// Returns (encoded, true) on success, (nil, false) on any decode/encode failure
// (caller should keep original bytes in that case).
func transcodePNGtoJPEG(pngData []byte, quality int) ([]byte, bool) {
	img, _, err := image.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, false
	}
	// JPEG cannot encode RGBA with alpha; flatten onto white background.
	bounds := img.Bounds()
	flat := image.NewRGBA(bounds)
	draw.Draw(flat, bounds, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(flat, bounds, img, bounds.Min, draw.Over)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flat, &jpeg.Options{Quality: quality}); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

func sanitizeID(s string) string {
	s = strings.ReplaceAll(s, "..", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return s
}

func (d *Deps) HandleConvertPDF(ctx context.Context, payload json.RawMessage) error {
	var p convertPDFPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}

	var title, pdfPath string
	if err := d.DB.QueryRowContext(ctx,
		`SELECT title, pdf_path FROM episodes WHERE id=?`, p.EpisodeID).
		Scan(&title, &pdfPath); err != nil {
		return err
	}
	if pdfPath != "" && !p.Force {
		return nil
	}

	rows, err := d.DB.QueryContext(ctx,
		`SELECT file_path FROM images WHERE episode_id=? ORDER BY idx`, p.EpisodeID)
	if err != nil {
		return err
	}
	var files []string
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			rows.Close()
			return err
		}
		if fp != "" {
			files = append(files, fp)
		}
	}
	rows.Close()
	if len(files) == 0 {
		return errors.New("no images downloaded yet")
	}

	var imgBytes [][]byte
	for _, rel := range files {
		b, err := os.ReadFile(filepath.Join(d.VolDir, rel))
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		imgBytes = append(imgBytes, b)
	}

	combined, err := imaging.CombineImages(imgBytes)
	if err != nil {
		return err
	}

	relPDF := filepath.Join("pdfs", fmt.Sprintf("%d.pdf", p.EpisodeID))
	full := d.mediaPath(relPDF)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := os.Create(full)
	if err != nil {
		return err
	}
	if err := imaging.ToPDF(combined, f); err != nil {
		f.Close()
		return err
	}
	f.Close()

	_, err = d.DB.ExecContext(ctx,
		`UPDATE episodes SET pdf_path=?, updated_at=? WHERE id=?`,
		filepath.ToSlash(filepath.Join("media", relPDF)), time.Now().Unix(), p.EpisodeID)
	return err
}

func (d *Deps) HandleFixImages(ctx context.Context, _ json.RawMessage) error {
	rows, err := d.DB.QueryContext(ctx,
		`SELECT id FROM images WHERE file_path='' LIMIT 500`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()

	for i, id := range ids {
		_ = d.Queue.Enqueue(ctx, KindDownloadImage,
			fmt.Sprintf("download_image:%d", id),
			downloadImagePayload{ImageID: id},
			WithPriority(5),
			WithDelay(time.Duration(i/50*10)*time.Second))
	}
	return nil
}

func (d *Deps) HandleFixPDF(ctx context.Context, _ json.RawMessage) error {
	rows, err := d.DB.QueryContext(ctx, `
		SELECT e.id
		FROM episodes e
		WHERE (e.pdf_path = '' OR e.pdf_path IS NULL)
		  AND EXISTS (SELECT 1 FROM images i WHERE i.episode_id = e.id)
		  AND NOT EXISTS (SELECT 1 FROM images i WHERE i.episode_id = e.id AND i.file_path = '')
		LIMIT 100`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()

	for i, id := range ids {
		_ = d.Queue.Enqueue(ctx, KindConvertPDF,
			fmt.Sprintf("convert_pdf:%d", id),
			convertPDFPayload{EpisodeID: id},
			WithPriority(8),
			WithDelay(time.Duration(i*5)*time.Second))
	}
	return nil
}
