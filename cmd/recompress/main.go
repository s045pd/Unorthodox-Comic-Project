// Command recompress walks vol/media/ and re-encodes any PNG file as JPEG q=82.
// Updates the SQLite `images.file_path` and `images.bytes` columns to match.
// Safe to re-run; skips files that are already .jpg.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/s045pd/se8/internal/storage"
)

func main() {
	var (
		volDir  = flag.String("vol", "./vol", "vol directory (must contain se8.db and media/)")
		quality = flag.Int("quality", 82, "JPEG quality 1-100")
		dryRun  = flag.Bool("dry-run", false, "scan and report only, don't write")
	)
	flag.Parse()

	ctx := context.Background()
	dbPath := filepath.Join(*volDir, "se8.db")
	mediaDir := filepath.Join(*volDir, "media")

	db, err := storage.Open(ctx, dbPath)
	if err != nil {
		fail("open db: %v", err)
	}
	defer db.Close()

	var (
		scanned, converted, savedBytes int64
		failed                         int64
	)

	err = filepath.WalkDir(mediaDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".png") {
			return nil
		}
		scanned++

		info, _ := d.Info()
		oldSize := info.Size()

		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("read fail", "path", path, "err", err)
			failed++
			return nil
		}

		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			slog.Warn("decode fail", "path", path, "err", err)
			failed++
			return nil
		}
		bounds := img.Bounds()
		flat := image.NewRGBA(bounds)
		draw.Draw(flat, bounds, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
		draw.Draw(flat, bounds, img, bounds.Min, draw.Over)

		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, flat, &jpeg.Options{Quality: *quality}); err != nil {
			slog.Warn("encode fail", "path", path, "err", err)
			failed++
			return nil
		}
		newSize := int64(buf.Len())
		if newSize >= oldSize {
			// No saving — keep original PNG.
			return nil
		}

		newPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".jpg"

		if *dryRun {
			fmt.Printf("DRY  %s  %d → %d bytes (-%.0f%%)\n", path, oldSize, newSize, float64(oldSize-newSize)*100/float64(oldSize))
			converted++
			savedBytes += oldSize - newSize
			return nil
		}

		if err := os.WriteFile(newPath, buf.Bytes(), 0o644); err != nil {
			slog.Warn("write fail", "path", newPath, "err", err)
			failed++
			return nil
		}
		// Update DB to point at new path.
		oldRel, _ := filepath.Rel(*volDir, path)
		newRel, _ := filepath.Rel(*volDir, newPath)
		oldRel = filepath.ToSlash(oldRel)
		newRel = filepath.ToSlash(newRel)
		if err := updateImageRow(ctx, db, oldRel, newRel, int(newSize)); err != nil {
			slog.Warn("db update fail", "old", oldRel, "err", err)
			// don't increment failed — file still got written
		}
		// Remove the old PNG.
		_ = os.Remove(path)

		converted++
		savedBytes += oldSize - newSize
		fmt.Printf("OK   %s  %d → %d (-%.0f%%)\n", oldRel, oldSize, newSize, float64(oldSize-newSize)*100/float64(oldSize))
		return nil
	})
	if err != nil {
		fail("walk: %v", err)
	}

	fmt.Printf("\n=== summary ===\n")
	fmt.Printf("PNGs scanned:   %d\n", scanned)
	fmt.Printf("converted:      %d\n", converted)
	fmt.Printf("failed:         %d\n", failed)
	fmt.Printf("saved bytes:    %d (%.1f MB)\n", savedBytes, float64(savedBytes)/1024/1024)
	if *dryRun {
		fmt.Println("(dry-run — nothing actually written)")
	}
}

func updateImageRow(ctx context.Context, db *sql.DB, oldRel, newRel string, newBytes int) error {
	_, err := db.ExecContext(ctx,
		`UPDATE images SET file_path=?, bytes=? WHERE file_path=?`,
		newRel, newBytes, oldRel)
	return err
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fatal: "+format+"\n", args...)
	os.Exit(1)
}
