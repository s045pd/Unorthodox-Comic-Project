package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens (or creates) a SQLite DB at path, enables WAL + foreign keys, and
// applies any pending embedded migrations.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	// busy_timeout 30s + WAL + synchronous=NORMAL is the standard high-concurrency
	// SQLite recipe. _txlock=immediate makes BEGIN take a write lock up-front so
	// the writer never deadlocks against itself across goroutines.
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(30000)&_txlock=immediate", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite WAL allows many concurrent readers + 1 writer. We size the pool
	// generously (16) so web SELECTs never queue behind the 8 worker writers
	// that hold connections during INSERT/UPDATE transactions. Writer-vs-writer
	// collisions are still bounded by busy_timeout=30s + _txlock=immediate set
	// in the DSN above; readers don't block writers under WAL.
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(16)
	db.SetConnMaxLifetime(0)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := runMigrations(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func runMigrations(ctx context.Context, db *sql.DB) error {
	// Check if schema_migrations table exists (created by migration 001 on first run).
	var tableExists int
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`,
	).Scan(&tableExists)

	// Load already-applied versions if the tracking table exists.
	applied := map[int]bool{}
	if tableExists > 0 {
		rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
		if err != nil {
			return fmt.Errorf("query schema_migrations: %w", err)
		}
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return err
			}
			applied[v] = true
		}
		rows.Close()
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	files := make([]fs.DirEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })

	for _, f := range files {
		name := f.Name()
		parts := strings.SplitN(name, "_", 2)
		version, err := strconv.Atoi(parts[0])
		if err != nil {
			return fmt.Errorf("invalid migration filename %q: %w", name, err)
		}
		if applied[version] {
			continue
		}
		body, err := fs.ReadFile(migrationsFS, "migrations/"+name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`,
			version, time.Now().Unix()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
