package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpen_RunsMigrations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	db, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Verify a known table exists
	var name string
	err = db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='books'",
	).Scan(&name)
	if err != nil {
		t.Fatalf("books table not created: %v", err)
	}
	if name != "books" {
		t.Errorf("got table %q, want books", name)
	}

	// Verify migration recorded
	var version int
	err = db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version)
	if err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if version != 1 {
		t.Errorf("migration version = %d, want 1", version)
	}
}

func TestOpen_Idempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	db1, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	db1.Close()

	db2, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close()
	// Should not error when migrations are re-run.
}
