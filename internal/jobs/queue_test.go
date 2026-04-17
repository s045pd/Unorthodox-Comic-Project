package jobs

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/s045pd/se8/internal/storage"
)

func newTestQueue(t *testing.T) (*Queue, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs.db")
	db, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewQueue(db), db
}

func TestEnqueue_Dedup(t *testing.T) {
	q, db := newTestQueue(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := q.Enqueue(ctx, KindFindBooks, "find_books:daily", nil); err != nil {
			t.Fatalf("enqueue #%d: %v", i, err)
		}
	}

	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE task_key='find_books:daily'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

func TestEnqueue_DifferentKeys(t *testing.T) {
	q, db := newTestQueue(t)
	ctx := context.Background()

	_ = q.Enqueue(ctx, KindFindEpisodes, "find_episodes:book-a", map[string]any{"book_id": "a"})
	_ = q.Enqueue(ctx, KindFindEpisodes, "find_episodes:book-b", map[string]any{"book_id": "b"})

	var count int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs`).Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 rows, got %d", count)
	}
}
