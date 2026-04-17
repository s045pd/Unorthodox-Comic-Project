package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/s045pd/se8/internal/crawler"
	"github.com/s045pd/se8/internal/storage"
)

type stats struct {
	books         int
	episodes      int
	imagesCopied  int
	imagesSkipped int
	pdfsCopied    int
	coversCopied  int
}

func migrate(ctx context.Context, f flags) error {
	src, err := openSource(f.source)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	if f.dryRun {
		s, err := walkCounts(ctx, src)
		if err != nil {
			return err
		}
		slog.Info("DRY RUN — no writes performed", "stats", s)
		return nil
	}

	if err := os.MkdirAll(f.targetMedia, 0o755); err != nil {
		return err
	}
	tdb, err := storage.Open(ctx, f.target)
	if err != nil {
		return fmt.Errorf("open target: %w", err)
	}
	defer tdb.Close()

	var s stats
	if err := migrateBooks(ctx, src, tdb, f, &s); err != nil {
		return err
	}
	if err := migrateEpisodes(ctx, src, tdb, f, &s); err != nil {
		return err
	}
	if err := migrateImages(ctx, src, tdb, f, &s); err != nil {
		return err
	}

	slog.Info("migration complete",
		"books", s.books, "episodes", s.episodes,
		"images_copied", s.imagesCopied, "images_skipped", s.imagesSkipped,
		"pdfs_copied", s.pdfsCopied, "covers_copied", s.coversCopied)
	return nil
}

func openSource(uri string) (*sql.DB, error) {
	if strings.HasPrefix(uri, "sqlite:///") {
		// Three-slash form: sqlite:///absolute/path
		path := strings.TrimPrefix(uri, "sqlite://")
		return sql.Open("sqlite", path)
	}
	if strings.HasPrefix(uri, "sqlite://") {
		// Two-slash form: sqlite://relative/path
		path := strings.TrimPrefix(uri, "sqlite://")
		return sql.Open("sqlite", path)
	}
	if strings.HasPrefix(uri, "postgres://") {
		return sql.Open("postgres", uri)
	}
	return nil, fmt.Errorf("unsupported source URI: %s", uri)
}

func walkCounts(ctx context.Context, src *sql.DB) (stats, error) {
	var s stats
	src.QueryRowContext(ctx, `SELECT COUNT(*) FROM apps_book`).Scan(&s.books)
	src.QueryRowContext(ctx, `SELECT COUNT(*) FROM apps_episode`).Scan(&s.episodes)
	src.QueryRowContext(ctx, `SELECT COUNT(*) FROM apps_image WHERE image != ''`).Scan(&s.imagesCopied)
	return s, nil
}

func migrateBooks(ctx context.Context, src, tdb *sql.DB, f flags, s *stats) error {
	rows, err := src.QueryContext(ctx,
		`SELECT id, title, description, hot, raw_url, image_url, image FROM apps_book`)
	if err != nil {
		return err
	}
	defer rows.Close()

	tx, err := tdb.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	n := 0
	for rows.Next() {
		var id, title, desc, raw, imgURL, imgB64 string
		var hot int
		if err := rows.Scan(&id, &title, &desc, &hot, &raw, &imgURL, &imgB64); err != nil {
			return err
		}
		coverPath := ""
		if imgB64 != "" {
			data, err := base64.StdEncoding.DecodeString(imgB64)
			if err != nil {
				slog.Warn("bad base64 cover", "book", id, "err", err)
			} else {
				ext := crawler.ExtFromMagic(data)
				if ext == "" {
					ext = "jpg"
				}
				rel := filepath.Join("covers", id+"."+ext)
				full := filepath.Join(f.targetMedia, rel)
				if err := writeFileSafe(full, data, f.overwrite); err != nil {
					return err
				}
				coverPath = filepath.ToSlash(filepath.Join("media", rel))
				s.coversCopied++
			}
		}
		now := time.Now().Unix()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO books (id, title, description, hot, raw_url, image_url, cover_path, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				title=excluded.title, description=excluded.description, hot=excluded.hot,
				raw_url=excluded.raw_url, image_url=excluded.image_url,
				cover_path=CASE WHEN excluded.cover_path != '' THEN excluded.cover_path ELSE books.cover_path END,
				updated_at=excluded.updated_at`,
			id, title, desc, hot, raw, imgURL, coverPath, now, now); err != nil {
			return err
		}
		s.books++
		n++
		if n%500 == 0 {
			if err := tx.Commit(); err != nil {
				return err
			}
			tx, err = tdb.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func migrateEpisodes(ctx context.Context, src, tdb *sql.DB, f flags, s *stats) error {
	rows, err := src.QueryContext(ctx,
		`SELECT id, book_id, title, raw_url, pdf FROM apps_episode`)
	if err != nil {
		return err
	}
	defer rows.Close()

	tx, err := tdb.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	n := 0
	for rows.Next() {
		var id int64
		var bookID, title, raw, pdf string
		if err := rows.Scan(&id, &bookID, &title, &raw, &pdf); err != nil {
			return err
		}
		pdfPath := ""
		if pdf != "" {
			srcPDF := filepath.Join(f.sourceMedia, pdf)
			rel := filepath.Join("pdfs", fmt.Sprintf("%d.pdf", id))
			dstPDF := filepath.Join(f.targetMedia, rel)
			if _, err := os.Stat(srcPDF); err == nil {
				if err := copyFileSafe(srcPDF, dstPDF, f.overwrite); err != nil {
					return err
				}
				pdfPath = filepath.ToSlash(filepath.Join("media", rel))
				s.pdfsCopied++
			}
		}
		now := time.Now().Unix()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO episodes (id, book_id, title, raw_url, pdf_path, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				title=excluded.title, raw_url=excluded.raw_url,
				pdf_path=CASE WHEN excluded.pdf_path != '' THEN excluded.pdf_path ELSE episodes.pdf_path END,
				updated_at=excluded.updated_at`,
			id, bookID, title, raw, pdfPath, now, now); err != nil {
			return err
		}
		s.episodes++
		n++
		if n%500 == 0 {
			if err := tx.Commit(); err != nil {
				return err
			}
			tx, err = tdb.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func migrateImages(ctx context.Context, src, tdb *sql.DB, f flags, s *stats) error {
	rows, err := src.QueryContext(ctx,
		`SELECT id, episode_id, "index", image, raw_url FROM apps_image`)
	if err != nil {
		return err
	}
	defer rows.Close()

	tx, err := tdb.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	n := 0
	for rows.Next() {
		var id, episodeID int64
		var idx int
		var b64, raw string
		if err := rows.Scan(&id, &episodeID, &idx, &b64, &raw); err != nil {
			return err
		}

		// Find the book_id for the episode
		var bookID string
		if err := tx.QueryRowContext(ctx,
			`SELECT book_id FROM episodes WHERE id=?`, episodeID).Scan(&bookID); err != nil {
			s.imagesSkipped++
			continue
		}

		filePath := ""
		width, height, bytes := 0, 0, 0
		if b64 != "" {
			data, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				slog.Warn("bad base64 image", "id", id, "err", err)
				s.imagesSkipped++
			} else {
				ext := crawler.ExtFromMagic(data)
				if ext == "" {
					ext = "jpg"
				}
				rel := filepath.Join("books", bookID, fmt.Sprintf("%d", episodeID),
					fmt.Sprintf("%03d.%s", idx, ext))
				full := filepath.Join(f.targetMedia, rel)
				if err := writeFileSafe(full, data, f.overwrite); err != nil {
					return err
				}
				filePath = filepath.ToSlash(filepath.Join("media", rel))
				bytes = len(data)
				s.imagesCopied++
			}
		} else {
			s.imagesSkipped++
		}

		now := time.Now().Unix()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO images (id, episode_id, idx, raw_url, file_path, width, height, bytes, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET idx=excluded.idx, raw_url=excluded.raw_url,
				file_path=CASE WHEN excluded.file_path != '' THEN excluded.file_path ELSE images.file_path END,
				updated_at=excluded.updated_at`,
			id, episodeID, idx, raw, filePath, width, height, bytes, now, now); err != nil {
			return err
		}
		n++
		if n%500 == 0 {
			if err := tx.Commit(); err != nil {
				return err
			}
			tx, err = tdb.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func writeFileSafe(path string, data []byte, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func copyFileSafe(src, dst string, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(dst); err == nil {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
