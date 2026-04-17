package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// buildSourceFixture creates a Django-shaped SQLite with a few rows.
func buildSourceFixture(t *testing.T) (srcDB, srcMedia string) {
	t.Helper()
	dir := t.TempDir()
	srcDB = filepath.Join(dir, "src.db")
	srcMedia = filepath.Join(dir, "media")
	if err := os.MkdirAll(filepath.Join(srcMedia, "pdfs"), 0o755); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", srcDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mustExec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("exec: %v", err)
		}
	}

	mustExec(`CREATE TABLE apps_book (
		id TEXT PRIMARY KEY, title TEXT, description TEXT, hot INTEGER,
		raw_url TEXT, image_url TEXT, image TEXT)`)
	mustExec(`CREATE TABLE apps_episode (
		id INTEGER PRIMARY KEY, book_id TEXT, title TEXT, raw_url TEXT, pdf TEXT)`)
	mustExec(`CREATE TABLE apps_image (
		id INTEGER PRIMARY KEY, episode_id INTEGER, "index" INTEGER, image TEXT, raw_url TEXT)`)

	// 1x1 red PNG
	red := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE, 0x00, 0x00, 0x00,
		0x0C, 0x49, 0x44, 0x41, 0x54, 0x08, 0x99, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
		0x00, 0x00, 0x03, 0x00, 0x01, 0x5B, 0x4E, 0x68, 0xE4, 0x00, 0x00, 0x00,
		0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
	}
	b64 := base64.StdEncoding.EncodeToString(red)

	mustExec(`INSERT INTO apps_book VALUES (?,?,?,?,?,?,?)`,
		"bk-1", "Title", "desc", 5, "http://raw", "http://img", b64)
	mustExec(`INSERT INTO apps_episode VALUES (?,?,?,?,?)`,
		100, "bk-1", "Ep 1", "http://ep", "pdfs/100.pdf")
	mustExec(`INSERT INTO apps_image VALUES (?,?,?,?,?)`, 5001, 100, 1, b64, "http://img1")
	mustExec(`INSERT INTO apps_image VALUES (?,?,?,?,?)`, 5002, 100, 2, b64, "http://img2")

	// Dummy PDF file
	if err := os.WriteFile(filepath.Join(srcMedia, "pdfs", "100.pdf"),
		[]byte("%PDF-1.4\n%dummy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return srcDB, srcMedia
}

func TestMigrate_DryRun(t *testing.T) {
	srcDB, srcMedia := buildSourceFixture(t)
	targetDir := t.TempDir()
	f := flags{
		source:      "sqlite://" + srcDB,
		sourceMedia: srcMedia,
		target:      filepath.Join(targetDir, "target.db"),
		targetMedia: filepath.Join(targetDir, "media"),
		dryRun:      true,
	}
	if err := migrate(context.Background(), f); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Nothing written
	if _, err := os.Stat(f.target); err == nil {
		t.Error("target DB should not exist in dry-run")
	}
	if _, err := os.Stat(filepath.Join(f.targetMedia, "books")); err == nil {
		t.Error("target media should not exist in dry-run")
	}
}

func TestMigrate_FullRun(t *testing.T) {
	srcDB, srcMedia := buildSourceFixture(t)
	targetDir := t.TempDir()
	f := flags{
		source:      "sqlite://" + srcDB,
		sourceMedia: srcMedia,
		target:      filepath.Join(targetDir, "target.db"),
		targetMedia: filepath.Join(targetDir, "media"),
	}
	if err := migrate(context.Background(), f); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Verify image files written
	imgPath := filepath.Join(f.targetMedia, "books", "bk-1", "100", "001.png")
	if _, err := os.Stat(imgPath); err != nil {
		t.Errorf("image not written: %v", err)
	}
	// Verify DB rows
	tdb, err := sql.Open("sqlite", f.target)
	if err != nil {
		t.Fatal(err)
	}
	defer tdb.Close()
	var bookCount, epCount, imgCount int
	tdb.QueryRow(`SELECT COUNT(*) FROM books`).Scan(&bookCount)
	tdb.QueryRow(`SELECT COUNT(*) FROM episodes`).Scan(&epCount)
	tdb.QueryRow(`SELECT COUNT(*) FROM images`).Scan(&imgCount)
	if bookCount != 1 || epCount != 1 || imgCount != 2 {
		t.Errorf("counts: books=%d eps=%d imgs=%d", bookCount, epCount, imgCount)
	}
	// Verify PDF copied
	if _, err := os.Stat(filepath.Join(f.targetMedia, "pdfs", "100.pdf")); err != nil {
		t.Errorf("pdf not copied: %v", err)
	}
}
