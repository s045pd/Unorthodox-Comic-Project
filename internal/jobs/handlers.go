package jobs

import (
	"database/sql"
	"os"
	"path/filepath"

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
