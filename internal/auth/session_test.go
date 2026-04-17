package auth

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/s045pd/se8/internal/storage"
)

func newTestDB(t *testing.T) (ctx context.Context, store *Store) {
	t.Helper()
	ctx = context.Background()
	path := filepath.Join(t.TempDir(), "t.db")
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return ctx, NewStore(db)
}

func TestEnsureFirstRunAdmin_CreatesAdmin(t *testing.T) {
	ctx, store := newTestDB(t)
	dir := t.TempDir()

	pw, created, err := store.EnsureFirstRunAdmin(ctx, dir)
	if err != nil {
		t.Fatalf("EnsureFirstRunAdmin: %v", err)
	}
	if !created {
		t.Fatal("expected created=true on first run")
	}
	if pw == "" {
		t.Fatal("empty password returned")
	}

	b, err := os.ReadFile(filepath.Join(dir, "first-run-password.txt"))
	if err != nil {
		t.Fatalf("password file not written: %v", err)
	}
	if string(b) == "" {
		t.Fatal("password file empty")
	}

	// Second call should be no-op (already exists).
	_, created2, err := store.EnsureFirstRunAdmin(ctx, dir)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if created2 {
		t.Error("expected created=false on second run")
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx, store := newTestDB(t)
	_, _, err := store.EnsureFirstRunAdmin(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	user, err := store.Authenticate(ctx, "admin", "wrong-pw")
	if err == nil || user != nil {
		t.Fatalf("Authenticate with wrong pw should fail, got user=%v err=%v", user, err)
	}

	// Reset user with known password
	hash, _ := HashPassword("goodpw")
	_, err = store.db.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE username='admin'`, hash)
	if err != nil {
		t.Fatal(err)
	}

	user, err = store.Authenticate(ctx, "admin", "goodpw")
	if err != nil || user == nil {
		t.Fatalf("Authenticate: user=%v err=%v", user, err)
	}

	token, err := store.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if len(token) < 32 {
		t.Errorf("short token: %s", token)
	}

	got, err := store.LookupSession(ctx, token)
	if err != nil || got == nil {
		t.Fatalf("LookupSession: %v %v", got, err)
	}
	if got.Username != "admin" {
		t.Errorf("username = %s", got.Username)
	}

	if err := store.DeleteSession(ctx, token); err != nil {
		t.Fatal(err)
	}
	got, _ = store.LookupSession(ctx, token)
	if got != nil {
		t.Error("session still present after delete")
	}
}
