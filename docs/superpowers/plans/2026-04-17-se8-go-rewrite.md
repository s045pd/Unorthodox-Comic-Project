# SE8 Go Rewrite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite SE8-Reader from Django+Celery+PostgreSQL+Redis to a single Go binary with SQLite and filesystem storage, ready for NAS deployment.

**Architecture:** Single Go process running four concurrent subsystems (HTTP server, cron scheduler, jobs runner, crawler client) sharing one `*sql.DB`. Persistent job queue in SQLite `jobs` table gives crash recovery. Images live on the filesystem under `vol/media/`. Built-in web UI uses `html/template` + HTMX + Pico CSS — no frontend build.

**Tech Stack:** Go 1.23, chi/v5, sqlc, modernc.org/sqlite (pure-Go), goquery, robfig/cron/v3, disintegration/imaging, go-pdf/fpdf, x/crypto/bcrypt, x/sync/semaphore, log/slog, caarlos0/env, HTMX, Pico.css.

**Spec:** [docs/superpowers/specs/2026-04-17-se8-go-rewrite-design.md](../specs/2026-04-17-se8-go-rewrite-design.md)

---

## File Structure (will be created)

```
cmd/se8/main.go               # Main binary entrypoint
cmd/migrate/main.go           # One-shot legacy data migrator

internal/config/config.go     # Env var loading
internal/storage/db.go        # Open SQLite, run migrations
internal/storage/migrations/001_init.sql
internal/storage/queries.sql  # sqlc input
internal/storage/generated/   # sqlc output (do not hand-edit)
internal/storage/sqlc.yaml

internal/domain/types.go      # Book/Episode/Image/Tag DTOs

internal/auth/password.go     # bcrypt wrapper
internal/auth/session.go      # session CRUD + first-run admin

internal/jobs/types.go        # JobKind constants, Handler type
internal/jobs/queue.go        # Enqueue with dedup
internal/jobs/runner.go       # Worker pool + recovery + retry
internal/jobs/handlers.go     # Registration of handler funcs

internal/crawler/client.go    # HTTP client with pool
internal/crawler/extractor.go # goquery parsing
internal/crawler/download.go  # DownloadImage
internal/crawler/testdata/    # HTML fixtures

internal/imaging/combine.go   # CombineImages
internal/imaging/pdf.go       # ToPDF

internal/scheduler/scheduler.go  # cron wiring

internal/web/router.go        # chi routes
internal/web/middleware.go    # auth, logging, ratelimit
internal/web/handlers/auth.go
internal/web/handlers/books.go
internal/web/handlers/episodes.go
internal/web/handlers/images.go
internal/web/handlers/tags.go
internal/web/handlers/jobs.go
internal/web/handlers/media.go
internal/web/templates/*.html
internal/web/static/*         # htmx.min.js, pico.css

legacy/                       # archived Django code (moved, not deleted)
vol/                          # runtime data (already exists)

.env.example
Makefile
Dockerfile
.github/workflows/ci.yml
go.mod
README.md
```

---

## Phase 0: Scaffold

### Task 0.1: Archive legacy Python code

**Files:**
- Move: `SE8/`, `apps/`, `manage.py`, `requirements.txt`, `Dockerfile`, `Dockerfile_cn`, `docker-compose.yml`, `docker-compose-quick.yml`, `docker_image_rebuild.sh`, `docker_compose_rebuild.sh`, `.venv`, `media/` → `legacy/`
- Keep in place: `.env`, `.github/`, `.vscode/`, `.gitignore`, `CLAUDE.md`, `readme.md`, `vol/`, `books/`, `config/`, `docs/`

- [ ] **Step 1: Create legacy directory and move Python tree**

```bash
mkdir -p legacy
git mv SE8 legacy/SE8
git mv apps legacy/apps
git mv manage.py legacy/manage.py
git mv requirements.txt legacy/requirements.txt
git mv Dockerfile legacy/Dockerfile
git mv Dockerfile_cn legacy/Dockerfile_cn
git mv docker-compose.yml legacy/docker-compose.yml
git mv docker-compose-quick.yml legacy/docker-compose-quick.yml
git mv docker_image_rebuild.sh legacy/docker_image_rebuild.sh
git mv docker_compose_rebuild.sh legacy/docker_compose_rebuild.sh
git mv media legacy/media
```

- [ ] **Step 2: Remove .venv from tracking if present**

```bash
# .venv is in .gitignore but may not be
if [ -d .venv ]; then rm -rf .venv; fi
```

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "chore: archive Django codebase to legacy/ before Go rewrite"
```

---

### Task 0.2: Initialize Go module

**Files:**
- Create: `go.mod`, `.gitignore` (update), `.env.example`

- [ ] **Step 1: Initialize go module**

Run:
```bash
go mod init github.com/s045pd/se8
```
Expected: `go.mod` file created with `module github.com/s045pd/se8` and `go 1.23`.

- [ ] **Step 2: Update .gitignore**

Open `.gitignore` and replace its contents with:

```gitignore
# Go
bin/
*.exe
*.test
*.out
vendor/

# Runtime data
vol/
!vol/.gitkeep

# Legacy Python
legacy/.venv/
legacy/**/__pycache__/
legacy/**/*.pyc

# IDE
.vscode/
.idea/
*.swp
.DS_Store

# Env
.env
```

- [ ] **Step 3: Create .env.example**

Create `.env.example`:

```bash
SE8_ADDR=0.0.0.0:8000
SE8_BASE_URL=https://se8.us
SE8_VOL_DIR=./vol

SE8_MAX_CONCURRENT_REQUESTS=20
SE8_WORKER_COUNT=4
SE8_MAX_PAGE=2000

SE8_HTTP_TIMEOUT=60s
SE8_SESSION_TTL=720h

SE8_LOG_LEVEL=info
SE8_LOG_FORMAT=text

SE8_DEBUG=false
```

- [ ] **Step 4: Ensure vol/ kept via .gitkeep**

```bash
mkdir -p vol
touch vol/.gitkeep
```

- [ ] **Step 5: Commit**

```bash
git add go.mod .gitignore .env.example vol/.gitkeep
git commit -m "chore: initialize Go module and environment template"
```

---

### Task 0.3: Makefile and directory skeleton

**Files:**
- Create: `Makefile`, `internal/.gitkeep`, `cmd/.gitkeep`

- [ ] **Step 1: Create Makefile**

Create `Makefile`:

```make
.PHONY: build run migrate test lint tidy sqlc docker

GO = go
LDFLAGS = -trimpath -ldflags="-s -w"

build:
	$(GO) build $(LDFLAGS) -o bin/se8 ./cmd/se8
	$(GO) build $(LDFLAGS) -o bin/migrate ./cmd/migrate

build-linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(LDFLAGS) -o bin/se8-linux-arm64 ./cmd/se8

build-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o bin/se8-linux-amd64 ./cmd/se8

run: build
	./bin/se8

test:
	$(GO) test -race -cover ./...

test-coverage:
	$(GO) test -race -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run

sqlc:
	sqlc generate -f internal/storage/sqlc.yaml

tidy:
	$(GO) mod tidy

docker:
	docker build -t se8:local .
```

- [ ] **Step 2: Create empty cmd/ and internal/ roots with .gitkeep**

```bash
mkdir -p cmd internal
touch cmd/.gitkeep internal/.gitkeep
```

- [ ] **Step 3: Verify `make` does not error on missing targets yet (it will fail on `build` — expected)**

Run: `make tidy`
Expected: Command completes without error (no deps yet).

- [ ] **Step 4: Commit**

```bash
git add Makefile cmd/.gitkeep internal/.gitkeep
git commit -m "chore: add Makefile and directory skeleton"
```

---

## Phase 1: Config

### Task 1.1: Config loader with tests

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Add dependencies**

```bash
go get github.com/caarlos0/env/v10
go get github.com/joho/godotenv
```

- [ ] **Step 2: Write the failing test**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear env
	for _, k := range []string{
		"SE8_ADDR", "SE8_BASE_URL", "SE8_VOL_DIR",
		"SE8_MAX_CONCURRENT_REQUESTS", "SE8_WORKER_COUNT", "SE8_MAX_PAGE",
		"SE8_HTTP_TIMEOUT", "SE8_SESSION_TTL",
		"SE8_LOG_LEVEL", "SE8_LOG_FORMAT", "SE8_DEBUG",
	} {
		os.Unsetenv(k)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != "0.0.0.0:8000" {
		t.Errorf("Addr default = %q, want 0.0.0.0:8000", cfg.Addr)
	}
	if cfg.MaxConcurrentRequests != 20 {
		t.Errorf("MaxConcurrentRequests default = %d, want 20", cfg.MaxConcurrentRequests)
	}
	if cfg.WorkerCount != 4 {
		t.Errorf("WorkerCount default = %d, want 4", cfg.WorkerCount)
	}
	if cfg.HTTPTimeout != 60*time.Second {
		t.Errorf("HTTPTimeout default = %v, want 60s", cfg.HTTPTimeout)
	}
	if cfg.SessionTTL != 720*time.Hour {
		t.Errorf("SessionTTL default = %v, want 720h", cfg.SessionTTL)
	}
}

func TestLoad_Override(t *testing.T) {
	os.Setenv("SE8_ADDR", "127.0.0.1:9999")
	os.Setenv("SE8_WORKER_COUNT", "8")
	defer os.Unsetenv("SE8_ADDR")
	defer os.Unsetenv("SE8_WORKER_COUNT")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9999" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.WorkerCount != 8 {
		t.Errorf("WorkerCount = %d", cfg.WorkerCount)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/config/...`
Expected: FAIL with `undefined: Load`.

- [ ] **Step 4: Implement config.go**

Create `internal/config/config.go`:

```go
package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v10"
	"github.com/joho/godotenv"
)

type Config struct {
	Addr                  string        `env:"SE8_ADDR"                     envDefault:"0.0.0.0:8000"`
	BaseURL               string        `env:"SE8_BASE_URL"                 envDefault:"https://se8.us"`
	VolDir                string        `env:"SE8_VOL_DIR"                  envDefault:"./vol"`
	MaxConcurrentRequests int           `env:"SE8_MAX_CONCURRENT_REQUESTS"  envDefault:"20"`
	WorkerCount           int           `env:"SE8_WORKER_COUNT"             envDefault:"4"`
	MaxPage               int           `env:"SE8_MAX_PAGE"                 envDefault:"2000"`
	HTTPTimeout           time.Duration `env:"SE8_HTTP_TIMEOUT"             envDefault:"60s"`
	SessionTTL            time.Duration `env:"SE8_SESSION_TTL"              envDefault:"720h"`
	LogLevel              string        `env:"SE8_LOG_LEVEL"                envDefault:"info"`
	LogFormat             string        `env:"SE8_LOG_FORMAT"               envDefault:"text"`
	Debug                 bool          `env:"SE8_DEBUG"                    envDefault:"false"`
}

// Load reads .env (if envFile is non-empty and exists) then parses env vars.
func Load(envFile string) (Config, error) {
	if envFile != "" {
		_ = godotenv.Load(envFile) // best-effort; missing file is OK
	}
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return cfg, fmt.Errorf("parse env: %w", err)
	}
	return cfg, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/config/... -v`
Expected: PASS both tests.

- [ ] **Step 6: Tidy and commit**

```bash
go mod tidy
git add go.mod go.sum internal/config/
git commit -m "feat(config): env-driven config loader with defaults"
```

---

## Phase 2: Storage

### Task 2.1: DB open + embedded migrations runner

**Files:**
- Create: `internal/storage/db.go`
- Create: `internal/storage/db_test.go`
- Create: `internal/storage/migrations/001_init.sql`

- [ ] **Step 1: Add dependencies**

```bash
go get modernc.org/sqlite
```

- [ ] **Step 2: Write the initial migration file**

Create `internal/storage/migrations/001_init.sql`:

```sql
CREATE TABLE books (
    id            TEXT PRIMARY KEY,
    title         TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    hot           INTEGER NOT NULL DEFAULT 0,
    raw_url       TEXT NOT NULL DEFAULT '',
    image_url     TEXT NOT NULL DEFAULT '',
    cover_path    TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE TABLE episodes (
    id            INTEGER PRIMARY KEY,
    book_id       TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    title         TEXT NOT NULL DEFAULT '',
    raw_url       TEXT NOT NULL DEFAULT '',
    pdf_path      TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX idx_episodes_book ON episodes(book_id);

CREATE TABLE images (
    id            INTEGER PRIMARY KEY,
    episode_id    INTEGER NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
    idx           INTEGER NOT NULL DEFAULT 0,
    raw_url       TEXT NOT NULL DEFAULT '',
    file_path     TEXT NOT NULL DEFAULT '',
    width         INTEGER NOT NULL DEFAULT 0,
    height        INTEGER NOT NULL DEFAULT 0,
    bytes         INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX idx_images_episode ON images(episode_id, idx);
CREATE INDEX idx_images_missing ON images(file_path) WHERE file_path = '';

CREATE TABLE tags (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    name  TEXT NOT NULL UNIQUE
);

CREATE TABLE book_tags (
    book_id  TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    tag_id   INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (book_id, tag_id)
);

CREATE TABLE jobs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    kind          TEXT NOT NULL,
    payload       TEXT NOT NULL DEFAULT '{}',
    task_key      TEXT NOT NULL UNIQUE,
    status        TEXT NOT NULL DEFAULT 'pending',
    priority      INTEGER NOT NULL DEFAULT 0,
    attempts      INTEGER NOT NULL DEFAULT 0,
    max_attempts  INTEGER NOT NULL DEFAULT 3,
    last_error    TEXT NOT NULL DEFAULT '',
    run_at        INTEGER NOT NULL,
    started_at    INTEGER,
    finished_at   INTEGER,
    created_at    INTEGER NOT NULL
);
CREATE INDEX idx_jobs_ready ON jobs(status, run_at) WHERE status = 'pending';
CREATE INDEX idx_jobs_history ON jobs(kind, finished_at);

CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
    token       TEXT PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  INTEGER NOT NULL,
    created_at  INTEGER NOT NULL
);
CREATE INDEX idx_sessions_expiry ON sessions(expires_at);

CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);
```

- [ ] **Step 3: Write the failing test**

Create `internal/storage/db_test.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./internal/storage/...`
Expected: FAIL with `undefined: Open`.

- [ ] **Step 5: Implement db.go**

Create `internal/storage/db.go`:

```go
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
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
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
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[int]bool{}
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
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/storage/... -v`
Expected: both PASS.

- [ ] **Step 7: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/storage/
git commit -m "feat(storage): embedded SQLite schema migrations + Open"
```

---

### Task 2.2: sqlc setup and initial queries

**Files:**
- Create: `internal/storage/sqlc.yaml`
- Create: `internal/storage/queries.sql`
- Create: `internal/storage/generated/*.go` (via sqlc)

- [ ] **Step 1: Install sqlc**

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
```

- [ ] **Step 2: Write sqlc config**

Create `internal/storage/sqlc.yaml`:

```yaml
version: "2"
sql:
  - engine: "sqlite"
    queries: "queries.sql"
    schema: "migrations"
    gen:
      go:
        package: "generated"
        out: "generated"
        sql_package: "database/sql"
        emit_pointers_for_null_types: true
        emit_json_tags: true
```

- [ ] **Step 3: Write queries.sql**

Create `internal/storage/queries.sql`:

```sql
-- name: CreateUser :one
INSERT INTO users (username, password_hash, created_at)
VALUES (?, ?, ?)
RETURNING *;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = ?;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: CreateSession :exec
INSERT INTO sessions (token, user_id, expires_at, created_at)
VALUES (?, ?, ?, ?);

-- name: GetSessionByToken :one
SELECT s.*, u.username
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token = ? AND s.expires_at > ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < ?;

-- name: UpsertBook :exec
INSERT INTO books (id, title, description, hot, raw_url, image_url, cover_path, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    title=excluded.title,
    description=excluded.description,
    hot=excluded.hot,
    raw_url=excluded.raw_url,
    image_url=excluded.image_url,
    cover_path=CASE WHEN excluded.cover_path != '' THEN excluded.cover_path ELSE books.cover_path END,
    updated_at=excluded.updated_at;

-- name: GetBook :one
SELECT * FROM books WHERE id = ?;

-- name: ListBooks :many
SELECT * FROM books ORDER BY title LIMIT ? OFFSET ?;

-- name: CountBooks :one
SELECT COUNT(*) FROM books;

-- name: GetLatestEpisodeTitle :one
SELECT title FROM episodes WHERE book_id = ? ORDER BY id DESC LIMIT 1;

-- name: UpsertEpisode :exec
INSERT INTO episodes (id, book_id, title, raw_url, pdf_path, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    title=excluded.title,
    raw_url=excluded.raw_url,
    pdf_path=CASE WHEN excluded.pdf_path != '' THEN excluded.pdf_path ELSE episodes.pdf_path END,
    updated_at=excluded.updated_at;

-- name: GetEpisode :one
SELECT * FROM episodes WHERE id = ?;

-- name: ListEpisodesByBook :many
SELECT * FROM episodes WHERE book_id = ? ORDER BY id;

-- name: SetEpisodePDF :exec
UPDATE episodes SET pdf_path = ?, updated_at = ? WHERE id = ?;

-- name: UpsertImage :exec
INSERT INTO images (id, episode_id, idx, raw_url, file_path, width, height, bytes, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    idx=excluded.idx,
    raw_url=excluded.raw_url,
    file_path=CASE WHEN excluded.file_path != '' THEN excluded.file_path ELSE images.file_path END,
    width=CASE WHEN excluded.width > 0 THEN excluded.width ELSE images.width END,
    height=CASE WHEN excluded.height > 0 THEN excluded.height ELSE images.height END,
    bytes=CASE WHEN excluded.bytes > 0 THEN excluded.bytes ELSE images.bytes END,
    updated_at=excluded.updated_at;

-- name: GetImage :one
SELECT * FROM images WHERE id = ?;

-- name: ListImagesByEpisode :many
SELECT * FROM images WHERE episode_id = ? ORDER BY idx;

-- name: ListMissingImageIDs :many
SELECT id FROM images WHERE file_path = '' LIMIT ?;

-- name: SetImageFile :exec
UPDATE images SET file_path = ?, width = ?, height = ?, bytes = ?, updated_at = ? WHERE id = ?;

-- name: GetOrCreateTag :one
INSERT INTO tags (name) VALUES (?)
ON CONFLICT(name) DO UPDATE SET name=excluded.name
RETURNING *;

-- name: ListTags :many
SELECT t.*, (SELECT COUNT(*) FROM book_tags bt WHERE bt.tag_id = t.id) AS book_count
FROM tags t ORDER BY t.name;

-- name: LinkBookTag :exec
INSERT OR IGNORE INTO book_tags (book_id, tag_id) VALUES (?, ?);

-- name: EnqueueJob :exec
INSERT OR IGNORE INTO jobs (kind, payload, task_key, status, priority, max_attempts, run_at, created_at)
VALUES (?, ?, ?, 'pending', ?, ?, ?, ?);

-- name: FetchReadyJobs :many
SELECT * FROM jobs
WHERE status = 'pending' AND run_at <= ?
ORDER BY priority DESC, id ASC
LIMIT ?;

-- name: MarkJobRunning :exec
UPDATE jobs SET status='running', started_at=? WHERE id=? AND status='pending';

-- name: MarkJobDone :exec
UPDATE jobs SET status='done', finished_at=?, last_error='' WHERE id=?;

-- name: MarkJobFailed :exec
UPDATE jobs SET status='failed', finished_at=?, last_error=?, attempts=attempts+1 WHERE id=?;

-- name: RetryJob :exec
UPDATE jobs SET status='pending', run_at=?, last_error=?, attempts=attempts+1 WHERE id=?;

-- name: ResetRunningJobs :exec
UPDATE jobs SET status='pending', started_at=NULL WHERE status='running';

-- name: ListJobs :many
SELECT * FROM jobs ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = ?;

-- name: DeleteJob :exec
DELETE FROM jobs WHERE id = ?;
```

- [ ] **Step 4: Generate code**

Run from repo root:
```bash
cd internal/storage && sqlc generate && cd -
```
Expected: `internal/storage/generated/` populated with `db.go`, `models.go`, `queries.sql.go`.

- [ ] **Step 5: Verify build**

Run: `go build ./...`
Expected: compiles clean.

- [ ] **Step 6: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/storage/sqlc.yaml internal/storage/queries.sql internal/storage/generated/
git commit -m "feat(storage): sqlc queries and generated code"
```

---

## Phase 3: Auth

### Task 3.1: Password hashing

**Files:**
- Create: `internal/auth/password.go`
- Create: `internal/auth/password_test.go`

- [ ] **Step 1: Add dependency**

```bash
go get golang.org/x/crypto/bcrypt
```

- [ ] **Step 2: Write the failing test**

Create `internal/auth/password_test.go`:

```go
package auth

import "testing"

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "" {
		t.Fatal("empty hash")
	}
	if !VerifyPassword(hash, "hunter2") {
		t.Error("correct password failed verification")
	}
	if VerifyPassword(hash, "wrong") {
		t.Error("wrong password verified as correct")
	}
}

func TestGenerateRandomPassword(t *testing.T) {
	p, err := GenerateRandomPassword()
	if err != nil {
		t.Fatalf("GenerateRandomPassword: %v", err)
	}
	if len(p) < 32 {
		t.Errorf("password too short: %d chars", len(p))
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/auth/...`
Expected: FAIL with undefined symbols.

- [ ] **Step 4: Implement password.go**

Create `internal/auth/password.go`:

```go
package auth

import (
	"crypto/rand"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// GenerateRandomPassword returns a 64-char hex string (32 random bytes).
func GenerateRandomPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/auth/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/auth/
git commit -m "feat(auth): bcrypt password hashing and random generator"
```

---

### Task 3.2: Session management + first-run admin

**Files:**
- Create: `internal/auth/session.go`
- Create: `internal/auth/session_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/auth/session_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/... -run Session`
Expected: FAIL (Store/NewStore/etc undefined).

- [ ] **Step 3: Implement session.go**

Create `internal/auth/session.go`:

```go
package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

type User struct {
	ID       int64
	Username string
}

type Session struct {
	Token     string
	UserID    int64
	Username  string
	ExpiresAt time.Time
}

// EnsureFirstRunAdmin creates admin user with a random password if none exists.
// Returns (password, created, error). If created==true, the password is also
// written to <volDir>/first-run-password.txt.
func (s *Store) EnsureFirstRunAdmin(ctx context.Context, volDir string) (string, bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return "", false, err
	}
	if count > 0 {
		return "", false, nil
	}
	pw, err := GenerateRandomPassword()
	if err != nil {
		return "", false, err
	}
	hash, err := HashPassword(pw)
	if err != nil {
		return "", false, err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users(username, password_hash, created_at) VALUES(?,?,?)`,
		"admin", hash, time.Now().Unix()); err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(volDir, 0o755); err != nil {
		return "", false, err
	}
	if err := os.WriteFile(filepath.Join(volDir, "first-run-password.txt"),
		[]byte(pw+"\n"), 0o600); err != nil {
		return "", false, fmt.Errorf("write password file: %w", err)
	}
	return pw, true, nil
}

func (s *Store) Authenticate(ctx context.Context, username, password string) (*User, error) {
	var u User
	var hash string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !VerifyPassword(hash, password) {
		return nil, ErrInvalidCredentials
	}
	return &u, nil
}

func (s *Store) CreateSession(ctx context.Context, userID int64, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	now := time.Now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(token, user_id, expires_at, created_at) VALUES(?,?,?,?)`,
		token, userID, now.Add(ttl).Unix(), now.Unix())
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) LookupSession(ctx context.Context, token string) (*Session, error) {
	var sess Session
	var expiresAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT s.token, s.user_id, s.expires_at, u.username
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token=? AND s.expires_at > ?`,
		token, time.Now().Unix()).
		Scan(&sess.Token, &sess.UserID, &expiresAt, &sess.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sess.ExpiresAt = time.Unix(expiresAt, 0)
	return &sess, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token=?`, token)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
	return err
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/auth/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/auth/
git commit -m "feat(auth): session store with first-run admin bootstrap"
```

---

## Phase 4: Jobs Queue

### Task 4.1: Job types and dedup Enqueue

**Files:**
- Create: `internal/jobs/types.go`
- Create: `internal/jobs/queue.go`
- Create: `internal/jobs/queue_test.go`

- [ ] **Step 1: Write types.go**

Create `internal/jobs/types.go`:

```go
package jobs

import (
	"context"
	"encoding/json"
	"time"
)

type Kind string

const (
	KindFindBooks     Kind = "find_books"
	KindFindEpisodes  Kind = "find_episodes"
	KindFindImages    Kind = "find_images"
	KindDownloadImage Kind = "download_image"
	KindConvertPDF    Kind = "convert_pdf"
	KindFixImages     Kind = "fix_images"
	KindFixPDF        Kind = "fix_pdf"
)

type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Handler executes one job. Return nil on success; non-nil triggers retry.
type Handler func(ctx context.Context, payload json.RawMessage) error

type Job struct {
	ID          int64
	Kind        Kind
	Payload     json.RawMessage
	TaskKey     string
	Status      Status
	Priority    int
	Attempts    int
	MaxAttempts int
	LastError   string
	RunAt       time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
	CreatedAt   time.Time
}

type EnqueueOption func(*enqueueOpts)

type enqueueOpts struct {
	priority    int
	maxAttempts int
	runAt       time.Time
}

func WithPriority(p int) EnqueueOption {
	return func(o *enqueueOpts) { o.priority = p }
}

func WithMaxAttempts(n int) EnqueueOption {
	return func(o *enqueueOpts) { o.maxAttempts = n }
}

func WithRunAt(t time.Time) EnqueueOption {
	return func(o *enqueueOpts) { o.runAt = t }
}

func WithDelay(d time.Duration) EnqueueOption {
	return func(o *enqueueOpts) { o.runAt = time.Now().Add(d) }
}
```

- [ ] **Step 2: Write failing enqueue test**

Create `internal/jobs/queue_test.go`:

```go
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
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/jobs/...`
Expected: FAIL (undefined `NewQueue`, `Queue`).

- [ ] **Step 4: Implement queue.go**

Create `internal/jobs/queue.go`:

```go
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type Queue struct {
	db *sql.DB
}

func NewQueue(db *sql.DB) *Queue {
	return &Queue{db: db}
}

func (q *Queue) Enqueue(ctx context.Context, kind Kind, taskKey string, payload any, opts ...EnqueueOption) error {
	o := enqueueOpts{priority: 0, maxAttempts: 3, runAt: time.Now()}
	for _, opt := range opts {
		opt(&o)
	}

	var payloadJSON []byte
	if payload == nil {
		payloadJSON = []byte("{}")
	} else {
		var err error
		payloadJSON, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}

	_, err := q.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO jobs (kind, payload, task_key, status, priority, max_attempts, run_at, created_at)
		VALUES (?, ?, ?, 'pending', ?, ?, ?, ?)`,
		string(kind), string(payloadJSON), taskKey,
		o.priority, o.maxAttempts, o.runAt.Unix(), time.Now().Unix())
	return err
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/jobs/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/jobs/
git commit -m "feat(jobs): enqueue with task_key deduplication"
```

---

### Task 4.2: Runner with recovery, retry, and concurrency control

**Files:**
- Create: `internal/jobs/runner.go`
- Create: `internal/jobs/runner_test.go`

- [ ] **Step 1: Add semaphore dep**

```bash
go get golang.org/x/sync/semaphore
```

- [ ] **Step 2: Write failing tests**

Create `internal/jobs/runner_test.go`:

```go
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunner_Recovery_ResetsRunning(t *testing.T) {
	q, db := newTestQueue(t)
	ctx := context.Background()

	// Insert a "stuck" running job
	_, err := db.ExecContext(ctx, `
		INSERT INTO jobs (kind, payload, task_key, status, priority, max_attempts, run_at, created_at, started_at)
		VALUES ('find_books', '{}', 'stuck', 'running', 0, 3, ?, ?, ?)`,
		time.Now().Unix(), time.Now().Unix(), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}

	r := NewRunner(q, 1)
	if err := r.RecoverStuckJobs(ctx); err != nil {
		t.Fatal(err)
	}

	var status string
	db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE task_key='stuck'`).Scan(&status)
	if status != "pending" {
		t.Errorf("status = %q, want pending", status)
	}
}

func TestRunner_Retry_OnError(t *testing.T) {
	q, db := newTestQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	r := NewRunner(q, 1)
	r.Register(KindFindBooks, func(ctx context.Context, p json.RawMessage) error {
		calls.Add(1)
		return errors.New("simulated failure")
	})
	r.pollInterval = 50 * time.Millisecond
	r.backoffBase = 20 * time.Millisecond

	_ = q.Enqueue(ctx, KindFindBooks, "t:retry", nil, WithMaxAttempts(3))

	go r.Run(ctx)

	// Wait for 3 attempts
	deadline := time.Now().Add(3 * time.Second)
	for calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	time.Sleep(100 * time.Millisecond)

	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}

	var status string
	var attempts int
	db.QueryRowContext(context.Background(),
		`SELECT status, attempts FROM jobs WHERE task_key='t:retry'`).Scan(&status, &attempts)
	if status != "failed" {
		t.Errorf("status = %q, want failed", status)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestRunner_Success(t *testing.T) {
	q, db := newTestQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	r := NewRunner(q, 1)
	r.Register(KindFindBooks, func(ctx context.Context, p json.RawMessage) error {
		calls.Add(1)
		return nil
	})
	r.pollInterval = 50 * time.Millisecond

	_ = q.Enqueue(ctx, KindFindBooks, "t:success", nil)

	go r.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	time.Sleep(100 * time.Millisecond)

	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}

	var status string
	db.QueryRowContext(context.Background(),
		`SELECT status FROM jobs WHERE task_key='t:success'`).Scan(&status)
	if status != "done" {
		t.Errorf("status = %q, want done", status)
	}
}

func TestRunner_Concurrency(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var running atomic.Int32
	var maxRunning atomic.Int32
	var mu sync.Mutex

	r := NewRunner(q, 3) // max 3 workers
	r.Register(KindFindBooks, func(ctx context.Context, p json.RawMessage) error {
		n := running.Add(1)
		mu.Lock()
		if n > maxRunning.Load() {
			maxRunning.Store(n)
		}
		mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		running.Add(-1)
		return nil
	})
	r.pollInterval = 20 * time.Millisecond

	for i := 0; i < 10; i++ {
		_ = q.Enqueue(ctx, KindFindBooks, "t:conc-"+string(rune('a'+i)), nil)
	}

	go r.Run(ctx)

	time.Sleep(800 * time.Millisecond)
	cancel()

	if got := maxRunning.Load(); got > 3 {
		t.Errorf("maxRunning = %d, exceeded limit of 3", got)
	}
	if got := maxRunning.Load(); got < 2 {
		t.Errorf("maxRunning = %d, expected ≥2 to prove parallelism", got)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/jobs/... -run Runner`
Expected: FAIL (undefined `NewRunner`, etc.).

- [ ] **Step 4: Implement runner.go**

Create `internal/jobs/runner.go`:

```go
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"golang.org/x/sync/semaphore"
)

type Runner struct {
	queue        *Queue
	maxWorkers   int
	sem          *semaphore.Weighted
	handlers     map[Kind]Handler
	pollInterval time.Duration
	backoffBase  time.Duration
	logger       *slog.Logger
}

func NewRunner(q *Queue, maxWorkers int) *Runner {
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	return &Runner{
		queue:        q,
		maxWorkers:   maxWorkers,
		sem:          semaphore.NewWeighted(int64(maxWorkers)),
		handlers:     make(map[Kind]Handler),
		pollInterval: 2 * time.Second,
		backoffBase:  10 * time.Second,
		logger:       slog.Default(),
	}
}

func (r *Runner) Register(kind Kind, h Handler) {
	r.handlers[kind] = h
}

// RecoverStuckJobs flips any status=running rows back to pending.
// Call once at startup before Run.
func (r *Runner) RecoverStuckJobs(ctx context.Context) error {
	_, err := r.queue.db.ExecContext(ctx,
		`UPDATE jobs SET status='pending', started_at=NULL WHERE status='running'`)
	return err
}

func (r *Runner) Run(ctx context.Context) {
	_ = r.RecoverStuckJobs(ctx)

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.drain(ctx)
		}
	}
}

func (r *Runner) drain(ctx context.Context) {
	rows, err := r.queue.db.QueryContext(ctx, `
		SELECT id, kind, payload, task_key, attempts, max_attempts
		FROM jobs
		WHERE status='pending' AND run_at <= ?
		ORDER BY priority DESC, id ASC
		LIMIT ?`, time.Now().Unix(), r.maxWorkers*4)
	if err != nil {
		r.logger.Error("drain query", "err", err)
		return
	}
	type row struct {
		id          int64
		kind        string
		payload     string
		taskKey     string
		attempts    int
		maxAttempts int
	}
	var jobs []row
	for rows.Next() {
		var j row
		if err := rows.Scan(&j.id, &j.kind, &j.payload, &j.taskKey, &j.attempts, &j.maxAttempts); err != nil {
			r.logger.Error("drain scan", "err", err)
			continue
		}
		jobs = append(jobs, j)
	}
	rows.Close()

	for _, j := range jobs {
		if err := r.sem.Acquire(ctx, 1); err != nil {
			return
		}
		go func(j row) {
			defer r.sem.Release(1)
			r.execute(ctx, j.id, Kind(j.kind), json.RawMessage(j.payload), j.taskKey, j.attempts, j.maxAttempts)
		}(j)
	}
}

func (r *Runner) execute(ctx context.Context, id int64, kind Kind, payload json.RawMessage, taskKey string, attempts, maxAttempts int) {
	h, ok := r.handlers[kind]
	if !ok {
		r.failJob(ctx, id, fmt.Errorf("no handler for kind %q", kind))
		return
	}

	// Claim the job (atomic pending → running)
	res, err := r.queue.db.ExecContext(ctx,
		`UPDATE jobs SET status='running', started_at=? WHERE id=? AND status='pending'`,
		time.Now().Unix(), id)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // someone else grabbed it
	}

	r.logger.Info("job start", "id", id, "kind", kind, "task_key", taskKey, "attempt", attempts+1)
	err = h(ctx, payload)

	if err == nil {
		_, _ = r.queue.db.ExecContext(ctx,
			`UPDATE jobs SET status='done', finished_at=?, last_error='' WHERE id=?`,
			time.Now().Unix(), id)
		r.logger.Info("job done", "id", id, "kind", kind)
		return
	}

	newAttempts := attempts + 1
	if newAttempts >= maxAttempts {
		_, _ = r.queue.db.ExecContext(ctx,
			`UPDATE jobs SET status='failed', finished_at=?, last_error=?, attempts=? WHERE id=?`,
			time.Now().Unix(), err.Error(), newAttempts, id)
		r.logger.Warn("job failed", "id", id, "kind", kind, "err", err, "attempts", newAttempts)
		return
	}

	// Exponential backoff with jitter: base * 2^(attempt-1), capped at 5 min
	delay := time.Duration(math.Pow(2, float64(newAttempts-1))) * r.backoffBase
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	runAt := time.Now().Add(delay).Unix()
	_, _ = r.queue.db.ExecContext(ctx,
		`UPDATE jobs SET status='pending', run_at=?, last_error=?, attempts=? WHERE id=?`,
		runAt, err.Error(), newAttempts, id)
	r.logger.Warn("job retry scheduled", "id", id, "kind", kind, "err", err, "delay", delay)
}

func (r *Runner) failJob(ctx context.Context, id int64, err error) {
	_, _ = r.queue.db.ExecContext(ctx,
		`UPDATE jobs SET status='failed', finished_at=?, last_error=? WHERE id=?`,
		time.Now().Unix(), err.Error(), id)
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/jobs/... -v -race`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/jobs/
git commit -m "feat(jobs): runner with crash recovery, retry backoff, bounded concurrency"
```

---

## Phase 5: Crawler

### Task 5.1: HTTP client with connection pool

**Files:**
- Create: `internal/crawler/client.go`
- Create: `internal/crawler/client_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/crawler/client_test.go`:

```go
package crawler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_Get_SetsReferer(t *testing.T) {
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Referer")
		w.Write([]byte("<html>ok</html>"))
	}))
	defer srv.Close()

	c := NewClient("https://se8.us", 5*time.Second)
	body, err := c.GetHTML(t.Context(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if body == "" {
		t.Error("empty body")
	}
	if got != "https://se8.us/" {
		t.Errorf("Referer = %q", got)
	}
}
```

Note: `t.Context()` requires Go 1.24+. If your toolchain is 1.23, replace with `context.Background()`.

- [ ] **Step 2: Run test (expect fail)**

Run: `go test ./internal/crawler/...`
Expected: FAIL (`NewClient` undefined).

- [ ] **Step 3: Implement client.go**

Create `internal/crawler/client.go`:

```go
package crawler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	http    *http.Client
	origin  string
}

func NewClient(origin string, timeout time.Duration) *Client {
	tr := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     60 * time.Second,
	}
	return &Client{
		http:   &http.Client{Transport: tr, Timeout: timeout},
		origin: origin,
	}
}

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:91.0) Gecko/20100101 Firefox/91.0"

func (c *Client) do(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", c.origin+"/")
	req.Header.Set("Accept-Language", "en-GB,en;q=0.9,zh-CN;q=0.8,zh;q=0.7")
	return c.http.Do(req)
}

func (c *Client) GetHTML(ctx context.Context, url string) (string, error) {
	resp, err := c.do(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d for %s", resp.StatusCode, url)
	}
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

// GetBytes fetches raw bytes and returns (body, content-type, err).
func (c *Client) GetBytes(ctx context.Context, url string) ([]byte, string, error) {
	resp, err := c.do(ctx, url)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("status %d for %s", resp.StatusCode, url)
	}
	b, err := io.ReadAll(resp.Body)
	return b, resp.Header.Get("Content-Type"), err
}

func (c *Client) Origin() string { return c.origin }
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/crawler/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/crawler/
git commit -m "feat(crawler): HTTP client with Referer + connection pool"
```

---

### Task 5.2: Extractor — GetBooks with fixture

**Files:**
- Create: `internal/crawler/extractor.go`
- Create: `internal/crawler/extractor_test.go`
- Create: `internal/crawler/testdata/books_page.html`

- [ ] **Step 1: Add goquery**

```bash
go get github.com/PuerkitoBio/goquery
```

- [ ] **Step 2: Capture a fixture HTML**

Save a minimal representative fixture to `internal/crawler/testdata/books_page.html`:

```html
<!DOCTYPE html>
<html>
<body>
  <div class="common-comic-item">
    <a class="cover" href="https://se8.us/index.php/comic/abc-123"></a>
    <img data-original="https://cdn.se8.us/cover/abc.jpg" />
    <p class="comic__title">Test Title A</p>
    <p class="comic-update"><a>Chapter 10</a></p>
  </div>
  <div class="common-comic-item">
    <a class="cover" href="https://se8.us/index.php/comic/xyz-999"></a>
    <img data-original="https://cdn.se8.us/cover/xyz.jpg" />
    <p class="comic__title">Test Title B</p>
    <p class="comic-update"><a>Chapter 2</a></p>
  </div>
  <a class="end" href="/index.php/category/page/42"></a>
</body>
</html>
```

- [ ] **Step 3: Write the failing test**

Create `internal/crawler/extractor_test.go`:

```go
package crawler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseBooks_Fixture(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "books_page.html"))
	if err != nil {
		t.Fatal(err)
	}
	books, err := ParseBooks(string(b))
	if err != nil {
		t.Fatalf("ParseBooks: %v", err)
	}
	if len(books) != 2 {
		t.Fatalf("got %d books, want 2", len(books))
	}
	if books[0].ID != "abc-123" {
		t.Errorf("ID[0] = %q", books[0].ID)
	}
	if books[0].Title != "Test Title A" {
		t.Errorf("Title[0] = %q", books[0].Title)
	}
	if books[0].ImageURL != "https://cdn.se8.us/cover/abc.jpg" {
		t.Errorf("ImageURL[0] = %q", books[0].ImageURL)
	}
	if books[0].Current != "Chapter 10" {
		t.Errorf("Current[0] = %q", books[0].Current)
	}
	if books[1].ID != "xyz-999" {
		t.Errorf("ID[1] = %q", books[1].ID)
	}
}

func TestParseMaxPage_Fixture(t *testing.T) {
	b, _ := os.ReadFile(filepath.Join("testdata", "books_page.html"))
	max, err := ParseMaxPage(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if max != 42 {
		t.Errorf("max = %d, want 42", max)
	}
}
```

- [ ] **Step 4: Run test (expect fail)**

Run: `go test ./internal/crawler/... -run Parse`
Expected: FAIL (undefined).

- [ ] **Step 5: Implement extractor.go**

Create `internal/crawler/extractor.go`:

```go
package crawler

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

type BookDTO struct {
	ID       string
	RawURL   string
	Title    string
	ImageURL string
	Current  string // latest episode title (for outdated-check)
}

type BookMeta struct {
	Tags        []string
	Hot         int
	Description string
}

type EpisodeDTO struct {
	ID     int64
	RawURL string
	Title  string
}

type ImageDTO struct {
	ID     int64
	Index  int
	RawURL string
}

type Extractor struct {
	client *Client
}

func NewExtractor(c *Client) *Extractor {
	return &Extractor{client: c}
}

func (e *Extractor) Origin() string { return e.client.Origin() }

func (e *Extractor) FetchBooks(ctx context.Context, page int) ([]BookDTO, error) {
	url := fmt.Sprintf("%s/index.php/category/page/%d", e.client.Origin(), page)
	html, err := e.client.GetHTML(ctx, url)
	if err != nil {
		return nil, err
	}
	return ParseBooks(html)
}

func (e *Extractor) FetchEpisodes(ctx context.Context, url string) (BookMeta, []EpisodeDTO, error) {
	html, err := e.client.GetHTML(ctx, url)
	if err != nil {
		return BookMeta{}, nil, err
	}
	return ParseEpisodes(html)
}

func (e *Extractor) FetchImages(ctx context.Context, url string) ([]ImageDTO, error) {
	html, err := e.client.GetHTML(ctx, url)
	if err != nil {
		return nil, err
	}
	return ParseImages(html)
}

func (e *Extractor) FetchMaxPage(ctx context.Context) (int, error) {
	url := fmt.Sprintf("%s/index.php/category/page/1", e.client.Origin())
	html, err := e.client.GetHTML(ctx, url)
	if err != nil {
		return 0, err
	}
	return ParseMaxPage(html)
}

// ParseBooks extracts book cards from a category listing page.
func ParseBooks(html string) ([]BookDTO, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	var out []BookDTO
	doc.Find("div.common-comic-item").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Find("a.cover").Attr("href")
		parts := strings.Split(strings.TrimRight(href, "/"), "/")
		id := ""
		if len(parts) > 0 {
			id = parts[len(parts)-1]
		}
		img, _ := s.Find("img").Attr("data-original")
		title := strings.TrimSpace(s.Find("p.comic__title").First().Text())
		current := strings.TrimSpace(s.Find("p.comic-update a").First().Text())
		out = append(out, BookDTO{
			ID:       id,
			RawURL:   href,
			Title:    title,
			ImageURL: img,
			Current:  current,
		})
	})
	return out, nil
}

// ParseMaxPage returns the last page number from "end" pagination link.
func ParseMaxPage(html string) (int, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return 0, err
	}
	href, ok := doc.Find("a.end").Attr("href")
	if !ok {
		return 0, fmt.Errorf("no a.end in page")
	}
	parts := strings.Split(strings.TrimRight(href, "/"), "/")
	return strconv.Atoi(parts[len(parts)-1])
}

// ParseEpisodes extracts meta + episode list from a book detail page.
func ParseEpisodes(html string) (BookMeta, []EpisodeDTO, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return BookMeta{}, nil, err
	}

	meta := BookMeta{}
	doc.Find("div.comic-status a").Each(func(_ int, s *goquery.Selection) {
		if t := strings.TrimSpace(s.Text()); t != "" {
			meta.Tags = append(meta.Tags, t)
		}
	})
	// Hot is inside: div.comic-status > span:nth-child(3) > b
	hotText := strings.TrimSpace(doc.Find("div.comic-status span:nth-child(3) b").First().Text())
	if hotText != "" {
		hotText = strings.Fields(hotText)[0]
		if f, err := strconv.ParseFloat(hotText, 64); err == nil {
			meta.Hot = int(f)
		}
	}
	intro := doc.Find("div.comic-intro p")
	if intro.Length() >= 3 {
		meta.Description = strings.TrimSpace(intro.Eq(2).Text())
	} else if intro.Length() > 0 {
		meta.Description = strings.TrimSpace(intro.Last().Text())
	}

	var eps []EpisodeDTO
	doc.Find("ul.chapter__list-box li").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Find("a").Attr("href")
		parts := strings.Split(strings.TrimRight(href, "/"), "/")
		if len(parts) == 0 {
			return
		}
		id, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
		if err != nil {
			return
		}
		title := strings.TrimSpace(s.Text())
		eps = append(eps, EpisodeDTO{ID: id, RawURL: href, Title: title})
	})
	return meta, eps, nil
}

// ParseImages extracts image pid/index/url from a reading page.
func ParseImages(html string) ([]ImageDTO, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	var imgs []ImageDTO
	doc.Find("div.rd-article__pic.hide").Each(func(_ int, s *goquery.Selection) {
		pidStr, _ := s.Attr("data-pid")
		idxStr, _ := s.Attr("data-index")
		url, _ := s.Find("img").Attr("data-original")
		pid, err := strconv.ParseInt(pidStr, 10, 64)
		if err != nil {
			return
		}
		idx, _ := strconv.Atoi(idxStr)
		imgs = append(imgs, ImageDTO{ID: pid, Index: idx, RawURL: url})
	})
	return imgs, nil
}
```

- [ ] **Step 6: Run tests to verify pass**

Run: `go test ./internal/crawler/... -v`
Expected: PASS for ParseBooks and ParseMaxPage.

- [ ] **Step 7: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/crawler/
git commit -m "feat(crawler): goquery-based book/episode/image parsers + fixtures"
```

---

### Task 5.3: Extractor fixtures for episodes and images

**Files:**
- Create: `internal/crawler/testdata/episodes_page.html`
- Create: `internal/crawler/testdata/images_page.html`
- Modify: `internal/crawler/extractor_test.go`

- [ ] **Step 1: Create episode fixture**

Create `internal/crawler/testdata/episodes_page.html`:

```html
<!DOCTYPE html>
<html>
<body>
  <div class="comic-status">
    <a>Tag Foo</a>
    <a>Tag Bar</a>
    <span>-</span>
    <span>-</span>
    <span><b>4.5 分</b></span>
  </div>
  <div class="comic-intro">
    <p>lead</p>
    <p>subline</p>
    <p>This is the description line.</p>
  </div>
  <ul class="chapter__list-box clearfix">
    <li><a href="https://se8.us/index.php/chapter/100">Ep 1</a></li>
    <li><a href="https://se8.us/index.php/chapter/101">Ep 2</a></li>
    <li><a href="https://se8.us/index.php/chapter/102">Ep 3</a></li>
  </ul>
</body>
</html>
```

- [ ] **Step 2: Create images fixture**

Create `internal/crawler/testdata/images_page.html`:

```html
<!DOCTYPE html>
<html>
<body>
  <div class="rd-article__pic hide" data-pid="5001" data-index="1">
    <img data-original="https://cdn.se8.us/img/1.jpg" />
  </div>
  <div class="rd-article__pic hide" data-pid="5002" data-index="2">
    <img data-original="https://cdn.se8.us/img/2.jpg" />
  </div>
</body>
</html>
```

- [ ] **Step 3: Append tests**

Append to `internal/crawler/extractor_test.go`:

```go
func TestParseEpisodes_Fixture(t *testing.T) {
	b, _ := os.ReadFile(filepath.Join("testdata", "episodes_page.html"))
	meta, eps, err := ParseEpisodes(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Tags) != 2 || meta.Tags[0] != "Tag Foo" {
		t.Errorf("tags = %v", meta.Tags)
	}
	if meta.Hot != 4 {
		t.Errorf("hot = %d, want 4", meta.Hot)
	}
	if meta.Description != "This is the description line." {
		t.Errorf("desc = %q", meta.Description)
	}
	if len(eps) != 3 {
		t.Fatalf("got %d episodes, want 3", len(eps))
	}
	if eps[0].ID != 100 || eps[0].Title != "Ep 1" {
		t.Errorf("ep[0] = %+v", eps[0])
	}
}

func TestParseImages_Fixture(t *testing.T) {
	b, _ := os.ReadFile(filepath.Join("testdata", "images_page.html"))
	imgs, err := ParseImages(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 2 {
		t.Fatalf("got %d imgs, want 2", len(imgs))
	}
	if imgs[0].ID != 5001 || imgs[0].Index != 1 {
		t.Errorf("imgs[0] = %+v", imgs[0])
	}
	if imgs[0].RawURL != "https://cdn.se8.us/img/1.jpg" {
		t.Errorf("url = %q", imgs[0].RawURL)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/crawler/... -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/crawler/
git commit -m "test(crawler): fixtures for episodes and images pages"
```

---

### Task 5.4: DownloadImage

**Files:**
- Create: `internal/crawler/download.go`
- Create: `internal/crawler/download_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/crawler/download_test.go`:

```go
package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var tinyPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
	0x0D, 0x49, 0x44, 0x41, 0x54, 0x08, 0x99, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

func TestDownloadImage_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(tinyPNG)
	}))
	defer srv.Close()

	c := NewClient("https://se8.us", 5*time.Second)
	data, ct, err := c.DownloadImage(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != len(tinyPNG) {
		t.Errorf("len = %d", len(data))
	}
	if ct != "png" {
		t.Errorf("ct = %q, want png", ct)
	}
}

func TestDownloadImage_NotImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("not an image"))
	}))
	defer srv.Close()

	c := NewClient("https://se8.us", 5*time.Second)
	_, _, err := c.DownloadImage(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error for non-image content-type")
	}
}
```

- [ ] **Step 2: Run test (expect fail)**

Run: `go test ./internal/crawler/... -run Download`
Expected: FAIL (DownloadImage undefined).

- [ ] **Step 3: Implement download.go**

Create `internal/crawler/download.go`:

```go
package crawler

import (
	"context"
	"fmt"
	"strings"
)

// DownloadImage fetches bytes and returns (data, ext, error).
// Returns error if Content-Type does not start with "image/".
// ext is one of "jpg", "png", "webp", "gif" (fallback "jpg").
func (c *Client) DownloadImage(ctx context.Context, url string) ([]byte, string, error) {
	data, ct, err := c.GetBytes(ctx, url)
	if err != nil {
		return nil, "", err
	}
	if !strings.HasPrefix(ct, "image/") {
		return nil, "", fmt.Errorf("not an image: %q", ct)
	}
	return data, extFromContentType(ct), nil
}

func extFromContentType(ct string) string {
	ct = strings.ToLower(ct)
	switch {
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		return "jpg"
	case strings.Contains(ct, "png"):
		return "png"
	case strings.Contains(ct, "webp"):
		return "webp"
	case strings.Contains(ct, "gif"):
		return "gif"
	default:
		return "jpg"
	}
}

// ExtFromMagic detects image format from first bytes; used by the migration
// tool when content-type is unknown. Returns "" if not recognized.
func ExtFromMagic(b []byte) string {
	if len(b) < 4 {
		return ""
	}
	switch {
	case b[0] == 0xFF && b[1] == 0xD8:
		return "jpg"
	case b[0] == 0x89 && b[1] == 0x50 && b[2] == 0x4E && b[3] == 0x47:
		return "png"
	case len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "webp"
	case len(b) >= 6 && (string(b[0:6]) == "GIF87a" || string(b[0:6]) == "GIF89a"):
		return "gif"
	}
	return ""
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/crawler/... -v -race`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/crawler/
git commit -m "feat(crawler): DownloadImage and magic-byte extension detection"
```

---

## Phase 6: Imaging

### Task 6.1: Combine images into long image

**Files:**
- Create: `internal/imaging/combine.go`
- Create: `internal/imaging/combine_test.go`

- [ ] **Step 1: Add deps**

```bash
go get github.com/disintegration/imaging
```

- [ ] **Step 2: Write failing test**

Create `internal/imaging/combine_test.go`:

```go
package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func makePNG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCombineImages(t *testing.T) {
	a := makePNG(t, 200, 300, color.RGBA{255, 0, 0, 255})
	b := makePNG(t, 200, 300, color.RGBA{0, 255, 0, 255})
	c := makePNG(t, 200, 300, color.RGBA{0, 0, 255, 255})

	out, err := CombineImages([][]byte{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	bounds := out.Bounds()
	if bounds.Dx() != 200 || bounds.Dy() != 900 {
		t.Errorf("bounds = %v, want 200x900", bounds)
	}
}

func TestCombineImages_SkipsBroken(t *testing.T) {
	good := makePNG(t, 100, 100, color.RGBA{0, 0, 0, 255})
	out, err := CombineImages([][]byte{{0x00, 0x01}, good})
	if err != nil {
		t.Fatal(err)
	}
	if out.Bounds().Dy() != 100 {
		t.Errorf("expected skipped broken image; got height %d", out.Bounds().Dy())
	}
}

func TestCombineImages_EmptyInput(t *testing.T) {
	_, err := CombineImages(nil)
	if err == nil {
		t.Error("expected error for nil input")
	}
}
```

- [ ] **Step 3: Run test (expect fail)**

Run: `go test ./internal/imaging/...`
Expected: FAIL (CombineImages undefined).

- [ ] **Step 4: Implement combine.go**

Create `internal/imaging/combine.go`:

```go
package imaging

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"

	_ "golang.org/x/image/webp"
)

var ErrNoImages = errors.New("no images provided")

// CombineImages decodes each byte slice and stacks vertically left-aligned.
// Broken images are skipped with a log warning. Returns ErrNoImages if all
// inputs were broken or input was empty.
func CombineImages(inputs [][]byte) (image.Image, error) {
	if len(inputs) == 0 {
		return nil, ErrNoImages
	}

	type decoded struct {
		img image.Image
		w, h int
	}
	var parts []decoded
	var maxW, totalH int

	for i, raw := range inputs {
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			slog.Warn("combine: skipping broken image", "index", i, "err", err)
			continue
		}
		b := img.Bounds()
		w, h := b.Dx(), b.Dy()
		parts = append(parts, decoded{img: img, w: w, h: h})
		if w > maxW {
			maxW = w
		}
		totalH += h
	}
	if len(parts) == 0 {
		return nil, ErrNoImages
	}
	if totalH > 30000 {
		slog.Warn("combined image very tall", "height", totalH)
	}

	dst := image.NewRGBA(image.Rect(0, 0, maxW, totalH))
	y := 0
	for _, p := range parts {
		r := image.Rect(0, y, p.w, y+p.h)
		draw.Draw(dst, r, p.img, image.Point{}, draw.Src)
		y += p.h
	}
	return dst, nil
}
```

- [ ] **Step 5: Add webp decoder dep**

```bash
go get golang.org/x/image/webp
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/imaging/... -v`
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/imaging/
git commit -m "feat(imaging): vertical combine with broken-image tolerance"
```

---

### Task 6.2: PDF generation

**Files:**
- Create: `internal/imaging/pdf.go`
- Create: `internal/imaging/pdf_test.go`

- [ ] **Step 1: Add gofpdf**

```bash
go get github.com/go-pdf/fpdf
```

- [ ] **Step 2: Write failing test**

Create `internal/imaging/pdf_test.go`:

```go
package imaging

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

func TestToPDF_MultiPage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 800, 2400))
	for y := 0; y < 2400; y++ {
		for x := 0; x < 800; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := ToPDF(img, &buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() < 100 {
		t.Fatalf("PDF too small: %d bytes", buf.Len())
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) {
		t.Error("output is not a PDF")
	}
}

func TestToPDF_Empty(t *testing.T) {
	var buf bytes.Buffer
	err := ToPDF(nil, &buf)
	if err == nil {
		t.Error("expected error for nil image")
	}
}
```

- [ ] **Step 3: Run test (expect fail)**

Run: `go test ./internal/imaging/... -run ToPDF`
Expected: FAIL (ToPDF undefined).

- [ ] **Step 4: Implement pdf.go**

Create `internal/imaging/pdf.go`:

```go
package imaging

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"math"

	"github.com/go-pdf/fpdf"
)

// ToPDF slices a tall image into A4 pages and writes PDF to out.
// Each page is scaled so the image width fills the page width.
func ToPDF(img image.Image, out io.Writer) error {
	if img == nil {
		return errors.New("nil image")
	}
	b := img.Bounds()
	iw, ih := b.Dx(), b.Dy()
	if iw <= 0 || ih <= 0 {
		return errors.New("empty image")
	}

	pdf := fpdf.New("P", "mm", "A4", "")
	pageW, pageH := 210.0, 297.0
	scale := pageW / float64(iw)
	sliceHeightPx := int(math.Floor(pageH / scale))
	if sliceHeightPx < 1 {
		sliceHeightPx = 1
	}
	pageCount := int(math.Ceil(float64(ih) / float64(sliceHeightPx)))

	for p := 0; p < pageCount; p++ {
		top := p * sliceHeightPx
		bottom := top + sliceHeightPx
		if bottom > ih {
			bottom = ih
		}
		if top >= bottom {
			continue
		}

		sub := image.NewRGBA(image.Rect(0, 0, iw, bottom-top))
		for y := top; y < bottom; y++ {
			for x := 0; x < iw; x++ {
				sub.Set(x, y-top, img.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		var jbuf bytes.Buffer
		if err := jpeg.Encode(&jbuf, sub, &jpeg.Options{Quality: 85}); err != nil {
			return err
		}

		pdf.AddPage()
		pdf.RegisterImageOptionsReader(
			"s"+itoa(p),
			fpdf.ImageOptions{ImageType: "JPG"},
			bytes.NewReader(jbuf.Bytes()),
		)
		renderedH := float64(bottom-top) * scale
		pdf.ImageOptions("s"+itoa(p), 0, 0, pageW, renderedH, false,
			fpdf.ImageOptions{ImageType: "JPG"}, 0, "")
	}

	return pdf.Output(out)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [11]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/imaging/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/imaging/
git commit -m "feat(imaging): A4 PDF generation from long image"
```

---

## Phase 7: Domain Types

### Task 7.1: Shared domain types

**Files:**
- Create: `internal/domain/types.go`

- [ ] **Step 1: Create types**

Create `internal/domain/types.go`:

```go
package domain

import "time"

type Book struct {
	ID          string
	Title       string
	Description string
	Hot         int
	RawURL      string
	ImageURL    string
	CoverPath   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Episode struct {
	ID        int64
	BookID    string
	Title     string
	RawURL    string
	PDFPath   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Image struct {
	ID        int64
	EpisodeID int64
	Index     int
	RawURL    string
	FilePath  string
	Width     int
	Height    int
	Bytes     int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Tag struct {
	ID   int64
	Name string
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/domain/
git commit -m "feat(domain): shared DTO types"
```

---

## Phase 8: Job Handlers

Each handler glues crawler + storage + imaging + queue together. All handlers live in `internal/jobs/handlers.go` (kept in one file because they share a fairly thin dependency struct).

### Task 8.1: Handlers dependency struct and helpers

**Files:**
- Create: `internal/jobs/handlers.go`

- [ ] **Step 1: Create handlers.go skeleton**

Create `internal/jobs/handlers.go`:

```go
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	VolDir    string // absolute path to vol/
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
```

- [ ] **Step 2: Commit**

```bash
git add internal/jobs/handlers.go
git commit -m "feat(jobs): handler deps struct and registration scaffold"
```

---

### Task 8.2: `find_books` handler

**Files:**
- Modify: `internal/jobs/handlers.go` (append)
- Create: `internal/jobs/handlers_find_books_test.go`

- [ ] **Step 1: Append HandleFindBooks to handlers.go**

Append to `internal/jobs/handlers.go`:

```go
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
```

- [ ] **Step 2: Write test using mock HTTP server**

Create `internal/jobs/handlers_find_books_test.go`:

```go
package jobs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s045pd/se8/internal/crawler"
	"github.com/s045pd/se8/internal/storage"
)

const categoryPageHTML = `<html><body>
<div class="common-comic-item">
  <a class="cover" href="/index.php/comic/book-1"></a>
  <img data-original="https://cdn.example/1.jpg" />
  <p class="comic__title">Book One</p>
  <p class="comic-update"><a>Chapter 1</a></p>
</div>
<a class="end" href="/index.php/category/page/1"></a>
</body></html>`

func TestHandleFindBooks_EnqueuesForNewBooks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/index.php/category/page/") {
			w.Write([]byte(categoryPageHTML))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	q := NewQueue(db)
	client := crawler.NewClient(srv.URL, 0)
	ext := crawler.NewExtractor(client)

	d := &Deps{DB: db, Queue: q, Extractor: ext, Client: client, VolDir: t.TempDir(), MaxPage: 1}
	if err := d.HandleFindBooks(ctx, nil); err != nil {
		t.Fatalf("HandleFindBooks: %v", err)
	}

	var books int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM books`).Scan(&books)
	if books != 1 {
		t.Errorf("books = %d, want 1", books)
	}
	var jobs int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='find_episodes'`).Scan(&jobs)
	if jobs != 1 {
		t.Errorf("find_episodes jobs = %d, want 1", jobs)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/jobs/... -v -race -run FindBooks`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/jobs/
git commit -m "feat(jobs): find_books handler with dedup enqueue of find_episodes"
```

---

### Task 8.3: `find_episodes` handler

**Files:**
- Modify: `internal/jobs/handlers.go` (append)

- [ ] **Step 1: Append HandleFindEpisodes**

Append to `internal/jobs/handlers.go`:

```go
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

	// Update book metadata (tags, hot, description)
	now := time.Now().Unix()
	if _, err := d.DB.ExecContext(ctx,
		`UPDATE books SET hot=?, description=?, updated_at=? WHERE id=?`,
		meta.Hot, meta.Description, now, p.BookID); err != nil {
		return err
	}
	for _, tagName := range meta.Tags {
		var tagID int64
		err := d.DB.QueryRowContext(ctx,
			`INSERT INTO tags(name) VALUES(?) ON CONFLICT(name) DO UPDATE SET name=excluded.name RETURNING id`,
			tagName).Scan(&tagID)
		if err != nil {
			return fmt.Errorf("tag %q: %w", tagName, err)
		}
		if _, err := d.DB.ExecContext(ctx,
			`INSERT OR IGNORE INTO book_tags(book_id, tag_id) VALUES(?, ?)`,
			p.BookID, tagID); err != nil {
			return err
		}
	}

	// Upsert episodes; schedule find_images for new ones
	for _, ep := range eps {
		res, err := d.DB.ExecContext(ctx, `
			INSERT INTO episodes (id, book_id, title, raw_url, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET title=excluded.title, raw_url=excluded.raw_url, updated_at=excluded.updated_at`,
			ep.ID, p.BookID, ep.Title, ep.RawURL, now, now)
		if err != nil {
			return err
		}
		// Only schedule find_images for rows where INSERT path was taken.
		// SQLite RowsAffected is 1 for INSERT and 2 for UPDATE in ON CONFLICT,
		// so we use a separate existence check via last_insert rowid.
		if n, _ := res.RowsAffected(); n == 1 {
			// confirm no images yet
			var imgCount int
			d.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM images WHERE episode_id=?`, ep.ID).Scan(&imgCount)
			if imgCount == 0 {
				_ = d.Queue.Enqueue(ctx, KindFindImages,
					fmt.Sprintf("find_images:%d", ep.ID),
					findImagesPayload{EpisodeID: ep.ID},
					WithDelay(5*time.Second))
			}
		}
	}
	return nil
}
```

- [ ] **Step 2: Build check**

Run: `go build ./...`
Expected: compiles.

- [ ] **Step 3: Commit**

```bash
git add internal/jobs/handlers.go
git commit -m "feat(jobs): find_episodes handler with tag + metadata sync"
```

---

### Task 8.4: `find_images` + `download_image` handlers

**Files:**
- Modify: `internal/jobs/handlers.go` (append)

- [ ] **Step 1: Append HandleFindImages and HandleDownloadImage**

```go
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
	for _, img := range imgs {
		res, err := d.DB.ExecContext(ctx, `
			INSERT INTO images (id, episode_id, idx, raw_url, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET idx=excluded.idx, raw_url=excluded.raw_url, updated_at=excluded.updated_at`,
			img.ID, p.EpisodeID, img.Index, img.RawURL, now, now)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()

		var filePath string
		d.DB.QueryRowContext(ctx, `SELECT file_path FROM images WHERE id=?`, img.ID).Scan(&filePath)

		if n == 1 || p.Force || filePath == "" {
			_ = d.Queue.Enqueue(ctx, KindDownloadImage,
				fmt.Sprintf("download_image:%d", img.ID),
				downloadImagePayload{ImageID: img.ID},
				WithDelay(time.Duration(img.Index%5)*time.Second))
		}
	}

	// When all images are downloaded later, convert_pdf will be scheduled by
	// the fix_pdf sweep or manually. Avoid scheduling here because images are
	// not yet downloaded at this point.
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

func sanitizeID(s string) string {
	s = strings.ReplaceAll(s, "..", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return s
}
```

- [ ] **Step 2: Build check**

Run: `go build ./...`
Expected: compiles.

- [ ] **Step 3: Commit**

```bash
git add internal/jobs/handlers.go
git commit -m "feat(jobs): find_images + download_image with path-safe IDs"
```

---

### Task 8.5: `convert_pdf` handler

**Files:**
- Modify: `internal/jobs/handlers.go` (append)

- [ ] **Step 1: Append HandleConvertPDF**

```go
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
```

- [ ] **Step 2: Build check**

Run: `go build ./...`
Expected: compiles.

- [ ] **Step 3: Commit**

```bash
git add internal/jobs/handlers.go
git commit -m "feat(jobs): convert_pdf handler stitching images and writing PDF"
```

---

### Task 8.6: `fix_images` + `fix_pdf` handlers

**Files:**
- Modify: `internal/jobs/handlers.go` (append)

- [ ] **Step 1: Append both handlers**

```go
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
			WithDelay(time.Duration(i/50*10)*time.Second))
	}
	return nil
}

func (d *Deps) HandleFixPDF(ctx context.Context, _ json.RawMessage) error {
	// Episodes with no PDF AND all images downloaded (file_path != '').
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
			WithDelay(time.Duration(i*5)*time.Second))
	}
	return nil
}
```

- [ ] **Step 2: Build check**

Run: `go build ./...`
Expected: compiles.

- [ ] **Step 3: Commit**

```bash
git add internal/jobs/handlers.go
git commit -m "feat(jobs): fix_images and fix_pdf sweep handlers"
```

---

## Phase 9: Scheduler

### Task 9.1: Cron scheduler wiring

**Files:**
- Create: `internal/scheduler/scheduler.go`

- [ ] **Step 1: Add cron dep**

```bash
go get github.com/robfig/cron/v3
```

- [ ] **Step 2: Implement scheduler**

Create `internal/scheduler/scheduler.go`:

```go
package scheduler

import (
	"context"
	"log/slog"

	"github.com/robfig/cron/v3"

	"github.com/s045pd/se8/internal/jobs"
)

type Scheduler struct {
	cron  *cron.Cron
	queue *jobs.Queue
}

func New(q *jobs.Queue) *Scheduler {
	return &Scheduler{
		cron:  cron.New(cron.WithLocation(timeLocation())),
		queue: q,
	}
}

// Start registers built-in schedules and begins ticking. Stop with ctx cancel.
func (s *Scheduler) Start(ctx context.Context) error {
	specs := []struct {
		schedule string
		kind     jobs.Kind
		key      string
	}{
		{"0 0 * * *", jobs.KindFindBooks, "find_books:daily"},
		{"0 1 * * *", jobs.KindFixImages, "fix_images:daily"},
		{"0 2 * * *", jobs.KindFixPDF, "fix_pdf:daily"},
	}
	for _, sp := range specs {
		kind, key := sp.kind, sp.key
		_, err := s.cron.AddFunc(sp.schedule, func() {
			if err := s.queue.Enqueue(ctx, kind, key, nil); err != nil {
				slog.Error("scheduler enqueue", "kind", kind, "err", err)
			}
		})
		if err != nil {
			return err
		}
	}
	s.cron.Start()
	go func() {
		<-ctx.Done()
		<-s.cron.Stop().Done()
	}()
	return nil
}
```

- [ ] **Step 3: Add helper for time zone**

Create `internal/scheduler/tz.go`:

```go
package scheduler

import (
	"log/slog"
	"time"
)

func timeLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		slog.Warn("LoadLocation failed, using Local", "err", err)
		return time.Local
	}
	return loc
}
```

- [ ] **Step 4: Build check**

Run: `go build ./...`
Expected: compiles.

- [ ] **Step 5: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/scheduler/
git commit -m "feat(scheduler): daily cron schedules for find_books/fix_images/fix_pdf"
```

---

## Phase 10: Web Layer

### Task 10.1: Router skeleton, middleware, template & static embedding

**Files:**
- Create: `internal/web/router.go`
- Create: `internal/web/middleware.go`
- Create: `internal/web/assets.go`
- Create: `internal/web/templates/layout.html`
- Create: `internal/web/static/htmx.min.js` (downloaded)
- Create: `internal/web/static/pico.min.css` (downloaded)
- Create: `internal/web/static/app.css`

- [ ] **Step 1: Add deps**

```bash
go get github.com/go-chi/chi/v5
go get github.com/go-chi/httprate
```

- [ ] **Step 2: Download HTMX and Pico**

```bash
mkdir -p internal/web/static
curl -fsSL https://unpkg.com/htmx.org@1.9.12/dist/htmx.min.js -o internal/web/static/htmx.min.js
curl -fsSL https://unpkg.com/@picocss/pico@2/css/pico.min.css -o internal/web/static/pico.min.css
```

- [ ] **Step 3: Create app.css**

Create `internal/web/static/app.css`:

```css
body { max-width: 1100px; }
nav.app-nav { display: flex; gap: 1rem; align-items: center; padding: 0.5rem 0; border-bottom: 1px solid var(--pico-muted-border-color); }
nav.app-nav a { text-decoration: none; }
nav.app-nav .spacer { flex: 1; }
table.data { font-size: 0.92rem; }
table.data td, table.data th { padding: 0.35rem 0.5rem; }
.cover-thumb { width: 54px; height: 72px; object-fit: cover; border-radius: 4px; }
.status-pill { display: inline-block; padding: 0.1rem 0.5rem; border-radius: 10px; font-size: 0.8rem; }
.status-pending { background: #eee; }
.status-running { background: #ffd; }
.status-done    { background: #dfd; }
.status-failed  { background: #fdd; }
.read-page img { width: 100%; max-width: 900px; display: block; margin: 0 auto; }
```

- [ ] **Step 4: Create embed asset loader**

Create `internal/web/assets.go`:

```go
package web

import (
	"embed"
	"html/template"
	"io/fs"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Static returns the embedded /static/* filesystem rooted at "static".
func Static() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}

// LoadTemplates parses every .html under templates/ as one ParseFS set, so
// {{template "layout.html" .}} can reference partials across files.
func LoadTemplates() (*template.Template, error) {
	funcs := template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
	}
	return template.New("").Funcs(funcs).ParseFS(templatesFS, "templates/*.html")
}
```

- [ ] **Step 5: Create layout template**

Create `internal/web/templates/layout.html`:

```html
{{define "layout"}}
<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{.Title}} · SE8</title>
  <link rel="stylesheet" href="/static/pico.min.css" />
  <link rel="stylesheet" href="/static/app.css" />
  <script src="/static/htmx.min.js"></script>
</head>
<body>
  <main class="container">
    <nav class="app-nav">
      <strong>SE8</strong>
      <a href="/books">Books</a>
      <a href="/episodes">Episodes</a>
      <a href="/tags">Tags</a>
      <a href="/jobs">Jobs</a>
      <span class="spacer"></span>
      <form action="/admin/start-crawl" method="post" style="margin:0">
        <button type="submit" class="secondary">Start Crawl</button>
      </form>
      <form action="/logout" method="post" style="margin:0">
        <button type="submit" class="outline">Logout</button>
      </form>
    </nav>
    {{template "content" .}}
  </main>
</body>
</html>
{{end}}
```

- [ ] **Step 6: Create middleware.go**

Create `internal/web/middleware.go`:

```go
package web

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/s045pd/se8/internal/auth"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
)

// sessionMiddleware looks up the session cookie; on miss, redirects HTML
// routes to /login and returns 401 for /api/ routes.
func sessionMiddleware(store *auth.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/login" || r.URL.Path == "/healthz" ||
				strings.HasPrefix(r.URL.Path, "/static/") {
				next.ServeHTTP(w, r)
				return
			}
			c, err := r.Cookie("se8_session")
			if err != nil {
				unauthorized(w, r)
				return
			}
			sess, err := store.LookupSession(r.Context(), c.Value)
			if err != nil || sess == nil {
				unauthorized(w, r)
				return
			}
			ctx := context.WithValue(r.Context(), ctxUser, sess.Username)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func unauthorized(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

// loggingMiddleware writes a structured access log line per request.
func loggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(ww, r)
			logger.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.status,
				"dur_ms", time.Since(start).Milliseconds())
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
```

- [ ] **Step 7: Create router.go skeleton (no handlers yet)**

Create `internal/web/router.go`:

```go
package web

import (
	"database/sql"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"

	"github.com/s045pd/se8/internal/auth"
	"github.com/s045pd/se8/internal/jobs"
)

type Server struct {
	DB        *sql.DB
	Auth      *auth.Store
	Queue     *jobs.Queue
	VolDir    string
	Templates *template.Template
	Logger    *slog.Logger
}

// Router builds the full http.Handler.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(loggingMiddleware(s.Logger))

	// Static assets
	r.Handle("/static/*", http.StripPrefix("/static/",
		http.FileServerFS(fs.FS(Static()))))

	// Health
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok"))
	})

	// Login / logout (login rate-limited)
	r.Group(func(r chi.Router) {
		r.Use(httprate.LimitByIP(10, time.Minute))
		r.Get("/login", s.handleLoginForm)
		r.Post("/login", s.handleLoginSubmit)
	})
	r.Post("/logout", s.handleLogout)

	// Everything else behind session auth
	r.Group(func(r chi.Router) {
		r.Use(sessionMiddleware(s.Auth))
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/books", http.StatusFound)
		})
		s.mountBookRoutes(r)
		s.mountEpisodeRoutes(r)
		s.mountImageRoutes(r)
		s.mountTagRoutes(r)
		s.mountJobRoutes(r)
		s.mountMediaRoutes(r)
		s.mountAdminRoutes(r)
	})
	return r
}
```

- [ ] **Step 8: Build check (will fail — missing handlers)**

Run: `go build ./...`
Expected: errors about undefined `handleLoginForm`, `mountBookRoutes`, etc. — that's fine; handlers come next.

- [ ] **Step 9: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/web/
git commit -m "feat(web): chi router skeleton, embedded assets, session middleware"
```

---

### Task 10.2: Login, logout, and start-crawl admin action

**Files:**
- Create: `internal/web/handlers_auth.go`
- Create: `internal/web/handlers_admin.go`
- Create: `internal/web/templates/login.html`

- [ ] **Step 1: Create login.html**

Create `internal/web/templates/login.html`:

```html
{{define "login"}}
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8" />
  <title>Login · SE8</title>
  <link rel="stylesheet" href="/static/pico.min.css" />
  <link rel="stylesheet" href="/static/app.css" />
</head>
<body>
  <main class="container" style="max-width:400px;margin-top:5rem">
    <hgroup>
      <h1>SE8</h1>
      <h2>Sign in</h2>
    </hgroup>
    {{if .Error}}<article><strong>{{.Error}}</strong></article>{{end}}
    <form action="/login" method="post">
      <input type="text" name="username" placeholder="Username" required autofocus />
      <input type="password" name="password" placeholder="Password" required />
      <button type="submit">Sign in</button>
    </form>
  </main>
</body>
</html>
{{end}}
```

- [ ] **Step 2: Create handlers_auth.go**

Create `internal/web/handlers_auth.go`:

```go
package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/s045pd/se8/internal/auth"
)

const sessionCookie = "se8_session"

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	s.renderTemplate(w, "login", map[string]any{"Error": ""})
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	user, err := s.Auth.Authenticate(r.Context(), username, password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			s.renderTemplate(w, "login", map[string]any{"Error": "Invalid username or password"})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	token, err := s.Auth.CreateSession(r.Context(), user.ID, 30*24*time.Hour)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(30 * 24 * time.Hour),
	})
	http.Redirect(w, r, "/books", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(sessionCookie)
	if err == nil {
		_ = s.Auth.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:    sessionCookie,
		Value:   "",
		Path:    "/",
		MaxAge:  -1,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}

// renderTemplate writes the named template using the whole data map.
// Templates that use {{template "content" .}} must be passed a block name.
func (s *Server) renderTemplate(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.Templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

- [ ] **Step 3: Create handlers_admin.go**

Create `internal/web/handlers_admin.go`:

```go
package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/s045pd/se8/internal/jobs"
)

func (s *Server) mountAdminRoutes(r chi.Router) {
	r.Post("/admin/start-crawl", s.handleStartCrawl)
}

func (s *Server) handleStartCrawl(w http.ResponseWriter, r *http.Request) {
	if err := s.Queue.Enqueue(r.Context(), jobs.KindFindBooks, "find_books:manual", nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/jobs", http.StatusFound)
}
```

- [ ] **Step 4: Build check**

Run: `go build ./...`
Expected: still fails — book/episode/etc. mounts not implemented; continue below.

- [ ] **Step 5: Commit**

```bash
git add internal/web/
git commit -m "feat(web): login, logout, manual start-crawl"
```

---

### Task 10.3: Books list + detail handlers and templates

**Files:**
- Create: `internal/web/handlers_books.go`
- Create: `internal/web/templates/books_list.html`
- Create: `internal/web/templates/book_detail.html`

- [ ] **Step 1: Create handlers_books.go**

```go
package web

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func (s *Server) mountBookRoutes(r chi.Router) {
	r.Get("/books", s.handleBooksList)
	r.Get("/books/{id}", s.handleBookDetail)
	r.Post("/books/{id}/crawl", s.handleBookCrawl)
}

type bookRow struct {
	ID            string
	Title         string
	CoverPath     string
	Hot           int
	EpisodeCount  int
}

func (s *Server) handleBooksList(w http.ResponseWriter, r *http.Request) {
	limit := 50
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * limit

	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT b.id, b.title, b.cover_path, b.hot,
		       (SELECT COUNT(*) FROM episodes e WHERE e.book_id=b.id)
		FROM books b
		ORDER BY b.title
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var books []bookRow
	for rows.Next() {
		var b bookRow
		if err := rows.Scan(&b.ID, &b.Title, &b.CoverPath, &b.Hot, &b.EpisodeCount); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		books = append(books, b)
	}

	var total int
	s.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM books`).Scan(&total)

	s.renderTemplate(w, "books_list_page", map[string]any{
		"Title": "Books",
		"Books": books,
		"Page":  page,
		"Total": total,
		"Limit": limit,
	})
}

func (s *Server) handleBookDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var b struct {
		ID, Title, Description, CoverPath string
		Hot                               int
	}
	err := s.DB.QueryRowContext(r.Context(),
		`SELECT id, title, description, cover_path, hot FROM books WHERE id=?`, id).
		Scan(&b.ID, &b.Title, &b.Description, &b.CoverPath, &b.Hot)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	eps, err := s.loadEpisodeRowsForBook(r, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.renderTemplate(w, "book_detail_page", map[string]any{
		"Title":    b.Title,
		"Book":     b,
		"Episodes": eps,
	})
}

func (s *Server) handleBookCrawl(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_ = s.Queue.Enqueue(r.Context(), "find_episodes", "find_episodes:"+id,
		map[string]any{"book_id": id})
	if r.Header.Get("HX-Request") == "true" {
		w.Write([]byte(`<span>Queued ✓</span>`))
		return
	}
	http.Redirect(w, r, "/books/"+id, http.StatusFound)
}
```

- [ ] **Step 2: Create books_list.html**

Create `internal/web/templates/books_list.html`:

```html
{{define "books_list_page"}}{{template "layout" .}}{{end}}
{{define "content"}}
{{if eq .Title "Books"}}
<hgroup>
  <h1>Books</h1>
  <h2>{{.Total}} books</h2>
</hgroup>
<table class="data striped">
  <thead><tr><th></th><th>Title</th><th>Hot</th><th>Episodes</th><th></th></tr></thead>
  <tbody>
  {{range .Books}}
    <tr>
      <td>{{if .CoverPath}}<img class="cover-thumb" src="/{{.CoverPath}}" alt=""/>{{end}}</td>
      <td><a href="/books/{{.ID}}"><strong>{{.Title}}</strong></a></td>
      <td>{{.Hot}}</td>
      <td>{{.EpisodeCount}}</td>
      <td>
        <a role="button" class="secondary outline" href="/books/{{.ID}}">Open</a>
        <button hx-post="/books/{{.ID}}/crawl" hx-swap="outerHTML">Crawl</button>
      </td>
    </tr>
  {{end}}
  </tbody>
</table>
<nav>
  {{if gt .Page 1}}<a href="?page={{sub .Page 1}}">Prev</a>{{end}}
  <span>Page {{.Page}}</span>
  {{if gt .Total (add (sub .Page 1) .Limit)}}<a href="?page={{add .Page 1}}">Next</a>{{end}}
</nav>
{{end}}
{{end}}
```

- [ ] **Step 3: Create book_detail.html**

Create `internal/web/templates/book_detail.html`:

```html
{{define "book_detail_page"}}{{template "layout" .}}{{end}}
{{define "content"}}
{{with .Book}}
<article>
  <hgroup>
    <h1>{{.Title}}</h1>
    <h2>hot: {{.Hot}}</h2>
  </hgroup>
  {{if .CoverPath}}<img src="/{{.CoverPath}}" style="max-width:180px"/>{{end}}
  <p>{{.Description}}</p>
</article>
{{end}}
<h2>Episodes</h2>
<table class="data striped">
  <thead><tr><th>ID</th><th>Title</th><th>Images</th><th>PDF</th><th></th></tr></thead>
  <tbody>
  {{range .Episodes}}
    <tr>
      <td>{{.ID}}</td>
      <td>{{.Title}}</td>
      <td>{{.Completed}}/{{.Total}}</td>
      <td>{{if .PDFPath}}<a href="/{{.PDFPath}}">PDF</a>{{end}}</td>
      <td>
        <a role="button" class="secondary outline" href="/episodes/{{.ID}}">Read</a>
        <button hx-post="/episodes/{{.ID}}/fetch" hx-swap="outerHTML">Fetch</button>
        <button hx-post="/episodes/{{.ID}}/pdf" hx-swap="outerHTML">PDF</button>
      </td>
    </tr>
  {{end}}
  </tbody>
</table>
{{end}}
```

- [ ] **Step 4: Commit**

```bash
git add internal/web/
git commit -m "feat(web): books list + detail + crawl action"
```

---

### Task 10.4: Episodes list + read page + actions

**Files:**
- Create: `internal/web/handlers_episodes.go`
- Create: `internal/web/templates/episodes_list.html`
- Create: `internal/web/templates/episode_read.html`

- [ ] **Step 1: Create handlers_episodes.go**

```go
package web

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

type episodeRow struct {
	ID        int64
	Title     string
	PDFPath   string
	Total     int
	Completed int
	BookID    string
}

func (s *Server) mountEpisodeRoutes(r chi.Router) {
	r.Get("/episodes", s.handleEpisodesList)
	r.Get("/episodes/{id}", s.handleEpisodeRead)
	r.Post("/episodes/{id}/fetch", s.handleEpisodeFetch)
	r.Post("/episodes/{id}/pdf", s.handleEpisodePDF)
}

func (s *Server) loadEpisodeRowsForBook(r *http.Request, bookID string) ([]episodeRow, error) {
	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT e.id, e.title, e.pdf_path,
		       (SELECT COUNT(*) FROM images i WHERE i.episode_id=e.id),
		       (SELECT COUNT(*) FROM images i WHERE i.episode_id=e.id AND i.file_path != ''),
		       e.book_id
		FROM episodes e WHERE e.book_id=? ORDER BY e.id`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []episodeRow
	for rows.Next() {
		var e episodeRow
		if err := rows.Scan(&e.ID, &e.Title, &e.PDFPath, &e.Total, &e.Completed, &e.BookID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *Server) handleEpisodesList(w http.ResponseWriter, r *http.Request) {
	limit := 100
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * limit
	bookFilter := r.URL.Query().Get("book")

	q := `
		SELECT e.id, e.title, e.pdf_path,
		       (SELECT COUNT(*) FROM images i WHERE i.episode_id=e.id),
		       (SELECT COUNT(*) FROM images i WHERE i.episode_id=e.id AND i.file_path != ''),
		       e.book_id
		FROM episodes e`
	args := []any{}
	if bookFilter != "" {
		q += " WHERE e.book_id=?"
		args = append(args, bookFilter)
	}
	q += " ORDER BY e.id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var eps []episodeRow
	for rows.Next() {
		var e episodeRow
		if err := rows.Scan(&e.ID, &e.Title, &e.PDFPath, &e.Total, &e.Completed, &e.BookID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		eps = append(eps, e)
	}

	s.renderTemplate(w, "episodes_list_page", map[string]any{
		"Title":    "Episodes",
		"Episodes": eps,
		"Page":     page,
	})
}

func (s *Server) handleEpisodeRead(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var title, bookID string
	if err := s.DB.QueryRowContext(r.Context(),
		`SELECT title, book_id FROM episodes WHERE id=?`, id).
		Scan(&title, &bookID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT file_path FROM images WHERE episode_id=? ORDER BY idx`, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		paths = append(paths, p)
	}
	s.renderTemplate(w, "episode_read_page", map[string]any{
		"Title":  title,
		"ID":     id,
		"BookID": bookID,
		"Paths":  paths,
	})
}

func (s *Server) handleEpisodeFetch(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	_ = s.Queue.Enqueue(r.Context(), "find_images",
		"find_images:"+strconv.FormatInt(id, 10),
		map[string]any{"episode_id": id, "force": true})
	if r.Header.Get("HX-Request") == "true" {
		w.Write([]byte(`<span>Fetch queued ✓</span>`))
		return
	}
	http.Redirect(w, r, r.Referer(), http.StatusFound)
}

func (s *Server) handleEpisodePDF(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	force := r.URL.Query().Get("force") == "1"
	_ = s.Queue.Enqueue(r.Context(), "convert_pdf",
		"convert_pdf:"+strconv.FormatInt(id, 10),
		map[string]any{"episode_id": id, "force": force})
	if r.Header.Get("HX-Request") == "true" {
		w.Write([]byte(`<span>PDF queued ✓</span>`))
		return
	}
	http.Redirect(w, r, r.Referer(), http.StatusFound)
}
```

- [ ] **Step 2: Create episodes_list.html**

Create `internal/web/templates/episodes_list.html`:

```html
{{define "episodes_list_page"}}{{template "layout" .}}{{end}}
{{define "content"}}
{{if eq .Title "Episodes"}}
<h1>Episodes</h1>
<table class="data striped">
  <thead><tr><th>ID</th><th>Book</th><th>Title</th><th>Images</th><th>PDF</th><th></th></tr></thead>
  <tbody>
  {{range .Episodes}}
    <tr>
      <td>{{.ID}}</td>
      <td><a href="/books/{{.BookID}}">{{.BookID}}</a></td>
      <td>{{.Title}}</td>
      <td>{{.Completed}}/{{.Total}}</td>
      <td>{{if .PDFPath}}<a href="/{{.PDFPath}}">PDF</a>{{end}}</td>
      <td>
        <a role="button" class="secondary outline" href="/episodes/{{.ID}}">Read</a>
        <button hx-post="/episodes/{{.ID}}/fetch" hx-swap="outerHTML">Fetch</button>
        <button hx-post="/episodes/{{.ID}}/pdf" hx-swap="outerHTML">PDF</button>
      </td>
    </tr>
  {{end}}
  </tbody>
</table>
{{end}}
{{end}}
```

- [ ] **Step 3: Create episode_read.html**

Create `internal/web/templates/episode_read.html`:

```html
{{define "episode_read_page"}}{{template "layout" .}}{{end}}
{{define "content"}}
{{if .ID}}
<nav>
  <a href="/books/{{.BookID}}">← Back to book</a>
</nav>
<h1>{{.Title}}</h1>
<section class="read-page">
  {{range .Paths}}
    {{if .}}<img src="/{{.}}" loading="lazy" alt=""/>{{end}}
  {{end}}
</section>
{{end}}
{{end}}
```

- [ ] **Step 4: Commit**

```bash
git add internal/web/
git commit -m "feat(web): episodes list + read page + fetch/pdf actions"
```

---

### Task 10.5: Images, tags, and media serving

**Files:**
- Create: `internal/web/handlers_images.go`
- Create: `internal/web/handlers_tags.go`
- Create: `internal/web/handlers_media.go`
- Create: `internal/web/templates/images_list.html`
- Create: `internal/web/templates/tags_list.html`

- [ ] **Step 1: Create handlers_images.go**

```go
package web

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

type imgRow struct {
	ID, EpisodeID int64
	Index, Bytes  int
	HasFile       bool
}

func (s *Server) mountImageRoutes(r chi.Router) {
	r.Get("/images", s.handleImagesList)
	r.Post("/images/{id}/redownload", s.handleImageRedownload)
}

func (s *Server) handleImagesList(w http.ResponseWriter, r *http.Request) {
	epFilter := r.URL.Query().Get("episode")
	q := `SELECT id, episode_id, idx, bytes, (file_path != '') FROM images`
	args := []any{}
	if epFilter != "" {
		q += " WHERE episode_id=?"
		args = append(args, epFilter)
	}
	q += " ORDER BY episode_id DESC, idx LIMIT 500"
	rows, err := s.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var imgs []imgRow
	for rows.Next() {
		var im imgRow
		if err := rows.Scan(&im.ID, &im.EpisodeID, &im.Index, &im.Bytes, &im.HasFile); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		imgs = append(imgs, im)
	}
	s.renderTemplate(w, "images_list_page", map[string]any{
		"Title":  "Images",
		"Images": imgs,
	})
}

func (s *Server) handleImageRedownload(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	_ = s.Queue.Enqueue(r.Context(), "download_image",
		"download_image:"+strconv.FormatInt(id, 10),
		map[string]any{"image_id": id})
	if r.Header.Get("HX-Request") == "true" {
		w.Write([]byte(`<span>Queued ✓</span>`))
		return
	}
	http.Redirect(w, r, r.Referer(), http.StatusFound)
}
```

- [ ] **Step 2: Create handlers_tags.go**

```go
package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

type tagRow struct {
	Name  string
	Count int
}

func (s *Server) mountTagRoutes(r chi.Router) {
	r.Get("/tags", s.handleTagsList)
}

func (s *Server) handleTagsList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT t.name, COUNT(bt.book_id)
		FROM tags t LEFT JOIN book_tags bt ON bt.tag_id=t.id
		GROUP BY t.id ORDER BY t.name`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var tags []tagRow
	for rows.Next() {
		var t tagRow
		if err := rows.Scan(&t.Name, &t.Count); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tags = append(tags, t)
	}
	s.renderTemplate(w, "tags_list_page", map[string]any{
		"Title": "Tags",
		"Tags":  tags,
	})
}
```

- [ ] **Step 3: Create handlers_media.go**

```go
package web

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *Server) mountMediaRoutes(r chi.Router) {
	r.Get("/media/*", s.handleMedia)
}

// handleMedia serves files under vol/media/ with a path-traversal guard.
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/media/")
	if strings.Contains(rel, "..") {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	full := filepath.Join(s.VolDir, "media", rel)
	abs, err := filepath.Abs(full)
	if err != nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	mediaRoot, _ := filepath.Abs(filepath.Join(s.VolDir, "media"))
	if !strings.HasPrefix(abs, mediaRoot+string(filepath.Separator)) && abs != mediaRoot {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.ServeFile(w, r, abs)
}
```

- [ ] **Step 4: Create images_list.html and tags_list.html**

Create `internal/web/templates/images_list.html`:

```html
{{define "images_list_page"}}{{template "layout" .}}{{end}}
{{define "content"}}
{{if eq .Title "Images"}}
<h1>Images</h1>
<table class="data striped">
  <thead><tr><th>ID</th><th>Episode</th><th>#</th><th>Bytes</th><th>Has File</th><th></th></tr></thead>
  <tbody>
  {{range .Images}}
    <tr>
      <td>{{.ID}}</td>
      <td><a href="/episodes/{{.EpisodeID}}">{{.EpisodeID}}</a></td>
      <td>{{.Index}}</td>
      <td>{{.Bytes}}</td>
      <td>{{if .HasFile}}✅{{else}}❌{{end}}</td>
      <td><button hx-post="/images/{{.ID}}/redownload" hx-swap="outerHTML">Redownload</button></td>
    </tr>
  {{end}}
  </tbody>
</table>
{{end}}
{{end}}
```

Create `internal/web/templates/tags_list.html`:

```html
{{define "tags_list_page"}}{{template "layout" .}}{{end}}
{{define "content"}}
{{if eq .Title "Tags"}}
<h1>Tags</h1>
<ul>
  {{range .Tags}}<li>{{.Name}} — {{.Count}} books</li>{{end}}
</ul>
{{end}}
{{end}}
```

- [ ] **Step 5: Commit**

```bash
git add internal/web/
git commit -m "feat(web): images list, tags list, media file serving"
```

---

### Task 10.6: Jobs page + HTMX fragment

**Files:**
- Create: `internal/web/handlers_jobs.go`
- Create: `internal/web/templates/jobs.html`

- [ ] **Step 1: Create handlers_jobs.go**

```go
package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

type jobRow struct {
	ID          int64
	Kind        string
	TaskKey     string
	Status      string
	Attempts    int
	MaxAttempts int
	LastError   string
	CreatedAt   time.Time
	FinishedAt  *time.Time
}

func (s *Server) mountJobRoutes(r chi.Router) {
	r.Get("/jobs", s.handleJobsPage)
	r.Get("/jobs/fragment", s.handleJobsFragment)
	r.Post("/jobs/{id}/retry", s.handleJobRetry)
	r.Delete("/jobs/{id}", s.handleJobDelete)
}

func (s *Server) loadJobs(r *http.Request) ([]jobRow, error) {
	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT id, kind, task_key, status, attempts, max_attempts, last_error,
		       created_at, finished_at
		FROM jobs ORDER BY id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []jobRow
	for rows.Next() {
		var j jobRow
		var created int64
		var finished *int64
		if err := rows.Scan(&j.ID, &j.Kind, &j.TaskKey, &j.Status, &j.Attempts,
			&j.MaxAttempts, &j.LastError, &created, &finished); err != nil {
			return nil, err
		}
		j.CreatedAt = time.Unix(created, 0)
		if finished != nil {
			t := time.Unix(*finished, 0)
			j.FinishedAt = &t
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

func (s *Server) handleJobsPage(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.loadJobs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderTemplate(w, "jobs_page", map[string]any{"Title": "Jobs", "Jobs": jobs})
}

func (s *Server) handleJobsFragment(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.loadJobs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderTemplate(w, "jobs_fragment", map[string]any{"Jobs": jobs})
}

func (s *Server) handleJobRetry(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	_, err := s.DB.ExecContext(r.Context(),
		`UPDATE jobs SET status='pending', run_at=?, attempts=0, last_error='' WHERE id=?`,
		time.Now().Unix(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/jobs", http.StatusFound)
}

func (s *Server) handleJobDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	_, err := s.DB.ExecContext(r.Context(), `DELETE FROM jobs WHERE id=?`, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 2: Create jobs.html**

Create `internal/web/templates/jobs.html`:

```html
{{define "jobs_page"}}{{template "layout" .}}{{end}}

{{define "content"}}
{{if eq .Title "Jobs"}}
<h1>Jobs</h1>
<div id="jobs" hx-get="/jobs/fragment" hx-trigger="every 3s" hx-swap="innerHTML">
  {{template "jobs_fragment" .}}
</div>
{{end}}
{{end}}

{{define "jobs_fragment"}}
<table class="data striped">
  <thead>
    <tr><th>ID</th><th>Kind</th><th>Task Key</th><th>Status</th><th>Attempts</th><th>Error</th><th>Created</th><th></th></tr>
  </thead>
  <tbody>
  {{range .Jobs}}
    <tr>
      <td>{{.ID}}</td>
      <td>{{.Kind}}</td>
      <td><code>{{.TaskKey}}</code></td>
      <td><span class="status-pill status-{{.Status}}">{{.Status}}</span></td>
      <td>{{.Attempts}}/{{.MaxAttempts}}</td>
      <td><small>{{.LastError}}</small></td>
      <td>{{.CreatedAt.Format "2006-01-02 15:04:05"}}</td>
      <td>
        {{if eq .Status "failed"}}<form action="/jobs/{{.ID}}/retry" method="post" style="display:inline"><button class="secondary">Retry</button></form>{{end}}
      </td>
    </tr>
  {{end}}
  </tbody>
</table>
{{end}}
```

- [ ] **Step 3: Build check**

Run: `go build ./...`
Expected: compiles clean now (all mount funcs defined).

- [ ] **Step 4: HTTP integration test for login flow**

Create `internal/web/server_test.go`:

```go
package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s045pd/se8/internal/auth"
	"github.com/s045pd/se8/internal/jobs"
	"github.com/s045pd/se8/internal/storage"
)

func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	authStore := auth.NewStore(db)
	pw, _, err := authStore.EnsureFirstRunAdmin(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		DB:        db,
		Auth:      authStore,
		Queue:     jobs.NewQueue(db),
		VolDir:    t.TempDir(),
		Templates: tpl,
		Logger:    testLogger(t),
	}
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	return srv, pw
}

func TestLoginFlow(t *testing.T) {
	srv, pw := newTestServer(t)

	// Hitting /books while unauthenticated redirects to /login
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(srv.URL + "/books")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected 302, got %d", resp.StatusCode)
	}

	// Post login, expect Set-Cookie and 302
	form := url.Values{"username": {"admin"}, "password": {pw}}
	resp, err = client.PostForm(srv.URL+"/login", form)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "se8_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie set")
	}

	// With cookie, /books returns 200
	req, _ := http.NewRequest("GET", srv.URL+"/books", nil)
	req.AddCookie(sessionCookie)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("books status = %d", resp.StatusCode)
	}
}

func TestLogin_BadPassword(t *testing.T) {
	srv, _ := newTestServer(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	form := url.Values{"username": {"admin"}, "password": {"nope"}}
	resp, err := client.PostForm(srv.URL+"/login", form)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with error banner, got %d", resp.StatusCode)
	}
	body, _ := readBody(resp)
	if !strings.Contains(body, "Invalid") {
		t.Errorf("expected error message, got: %s", body)
	}
}
```

Create `internal/web/testutil_test.go`:

```go
package web

import (
	"io"
	"log/slog"
	"net/http"
	"testing"
)

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func readBody(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/web/... -v -race`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/web/
git commit -m "feat(web): jobs page + fragment, integration tests for login flow"
```

---

## Phase 11: Main Binary

### Task 11.1: Wire everything in cmd/se8/main.go

**Files:**
- Create: `cmd/se8/main.go`

- [ ] **Step 1: Create main.go**

Create `cmd/se8/main.go`:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/s045pd/se8/internal/auth"
	"github.com/s045pd/se8/internal/config"
	"github.com/s045pd/se8/internal/crawler"
	"github.com/s045pd/se8/internal/jobs"
	"github.com/s045pd/se8/internal/scheduler"
	"github.com/s045pd/se8/internal/storage"
	"github.com/s045pd/se8/internal/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(".env")
	if err != nil {
		return err
	}

	logger := newLogger(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)

	volDir, err := filepath.Abs(cfg.VolDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(volDir, "media"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(volDir, "logs"), 0o755); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := storage.Open(ctx, filepath.Join(volDir, "se8.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	authStore := auth.NewStore(db)
	pw, created, err := authStore.EnsureFirstRunAdmin(ctx, volDir)
	if err != nil {
		return err
	}
	if created {
		logger.Info("first-run admin created", "username", "admin", "password_file",
			filepath.Join(volDir, "first-run-password.txt"))
		fmt.Fprintf(os.Stdout, "\n[FIRST-RUN] admin password: %s\n", pw)
	}

	client := crawler.NewClient(cfg.BaseURL, cfg.HTTPTimeout)
	extractor := crawler.NewExtractor(client)

	queue := jobs.NewQueue(db)
	runner := jobs.NewRunner(queue, cfg.WorkerCount)
	deps := &jobs.Deps{
		DB: db, Queue: queue, Extractor: extractor, Client: client,
		VolDir: volDir, MaxPage: cfg.MaxPage,
	}
	jobs.Register(runner, deps)
	go runner.Run(ctx)

	sched := scheduler.New(queue)
	if err := sched.Start(ctx); err != nil {
		return fmt.Errorf("scheduler start: %w", err)
	}

	// Periodic session cleanup
	go cleanSessionsLoop(ctx, authStore)

	tpl, err := web.LoadTemplates()
	if err != nil {
		return err
	}
	srv := &web.Server{
		DB: db, Auth: authStore, Queue: queue, VolDir: volDir,
		Templates: tpl, Logger: logger,
	}
	httpSrv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      srv.Router(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}
	logger.Info("serving", "addr", cfg.Addr)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http serve", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

func newLogger(level, format string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

func cleanSessionsLoop(ctx context.Context, store *auth.Store) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = store.DeleteExpiredSessions(ctx)
		}
	}
}
```

- [ ] **Step 2: Build and smoke-test**

Run:
```bash
go build -o bin/se8 ./cmd/se8
./bin/se8 &
SE8_PID=$!
sleep 2
curl -fsS http://127.0.0.1:8000/healthz
kill $SE8_PID
```
Expected: `ok`, and the server starts without panicking.

- [ ] **Step 3: Commit**

```bash
git add cmd/se8/
git commit -m "feat: main binary wiring with graceful shutdown"
```

---

## Phase 12: Migration Tool

### Task 12.1: Migration skeleton + flag parsing

**Files:**
- Create: `cmd/migrate/main.go`

- [ ] **Step 1: Add deps**

```bash
go get github.com/schollz/progressbar/v3
```

- [ ] **Step 2: Create skeleton**

Create `cmd/migrate/main.go`:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

type flags struct {
	source       string
	sourceMedia  string
	target       string
	targetMedia  string
	dryRun       bool
	overwrite    bool
}

func main() {
	var f flags
	flag.StringVar(&f.source, "source", "", "source DB URI (sqlite:///path or postgres://...)")
	flag.StringVar(&f.sourceMedia, "source-media", "", "source media directory (for PDFs)")
	flag.StringVar(&f.target, "target", "./vol/se8.db", "target SQLite path")
	flag.StringVar(&f.targetMedia, "target-media", "./vol/media", "target media directory")
	flag.BoolVar(&f.dryRun, "dry-run", false, "don't write anything, just count")
	flag.BoolVar(&f.overwrite, "overwrite", false, "overwrite existing target files")
	flag.Parse()

	if f.source == "" {
		fmt.Fprintln(os.Stderr, "--source is required")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := migrate(ctx, f); err != nil {
		fmt.Fprintln(os.Stderr, "migrate failed:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 3: Build check**

Run: `go build ./cmd/migrate`
Expected: error about undefined `migrate` — fine, next task implements it.

- [ ] **Step 4: Commit**

```bash
go mod tidy
git add go.mod go.sum cmd/migrate/
git commit -m "feat(migrate): CLI flag parsing for legacy data migration"
```

---

### Task 12.2: Migration core logic + fixture test

**Files:**
- Create: `cmd/migrate/migrate.go`
- Create: `cmd/migrate/migrate_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/migrate/migrate_test.go`:

```go
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
```

- [ ] **Step 2: Run test (expect fail)**

Run: `go test ./cmd/migrate/...`
Expected: FAIL (migrate not implemented).

- [ ] **Step 3: Implement migrate.go**

Create `cmd/migrate/migrate.go`:

```go
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
	books          int
	episodes       int
	imagesCopied   int
	imagesSkipped  int
	pdfsCopied     int
	coversCopied   int
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
	if strings.HasPrefix(uri, "sqlite://") || strings.HasPrefix(uri, "sqlite:///") {
		path := strings.TrimPrefix(uri, "sqlite:///")
		path = strings.TrimPrefix(path, "sqlite://")
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
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/migrate/... -v -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
go mod tidy
git add go.mod go.sum cmd/migrate/
git commit -m "feat(migrate): legacy Django DB → new schema with base64 → filesystem"
```

---

## Phase 13: Docker + CI + README

### Task 13.1: Dockerfile

**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`

- [ ] **Step 1: Create .dockerignore**

Create `.dockerignore`:

```
.git
.github
.vscode
.idea
bin/
vol/
legacy/
docs/
*.log
.env
```

- [ ] **Step 2: Create Dockerfile**

Create `Dockerfile`:

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/se8 ./cmd/se8 && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=builder /out/se8 /app/se8
COPY --from=builder /out/migrate /app/migrate
ENV SE8_VOL_DIR=/app/vol
VOLUME ["/app/vol"]
EXPOSE 8000
ENTRYPOINT ["/app/se8"]
```

- [ ] **Step 3: Build image**

Run: `docker build -t se8:test .`
Expected: image builds; size printed with `docker images se8:test` should be under ~40MB.

- [ ] **Step 4: Commit**

```bash
git add Dockerfile .dockerignore
git commit -m "feat: distroless static Docker image (se8 + migrate)"
```

---

### Task 13.2: GitHub Actions CI

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Write workflow**

Create `.github/workflows/ci.yml`:

```yaml
name: CI
on:
  push:
    branches: [main]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.23"
          cache: true

      - name: Build
        run: go build ./...

      - name: Test with coverage
        run: |
          go test -race -coverprofile=coverage.out ./...
          go tool cover -func=coverage.out | tail -1

      - name: Coverage gate
        run: |
          pct=$(go tool cover -func=coverage.out | awk '/total:/ {gsub(/%/, "", $3); print $3}')
          echo "coverage: ${pct}%"
          awk -v p="$pct" 'BEGIN { if (p+0 < 70) exit 1 }'

      - name: Vet
        run: go vet ./...

  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.23"
      - uses: golangci/golangci-lint-action@v4
        with:
          version: v1.61
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: build, test with 70% coverage gate, golangci-lint"
```

---

### Task 13.3: README

**Files:**
- Modify: `readme.md` (replace content)

- [ ] **Step 1: Replace readme.md**

Overwrite `readme.md`:

```markdown
# SE8-Reader (Go)

Single-binary comic scraper, originally Django + Celery, rewritten in Go for NAS-friendly deployment.

## Quick start

```bash
cp .env.example .env
make build
./bin/se8
```

On first boot, an admin user is created and the password is printed to stdout plus written to `vol/first-run-password.txt`.

Open http://127.0.0.1:8000 and sign in as `admin`.

## Docker

```bash
docker build -t se8:local .
docker run --rm -p 8000:8000 -v "$PWD/vol:/app/vol" se8:local
```

Or with compose (NAS-friendly):

```yaml
services:
  se8:
    image: se8:local
    restart: unless-stopped
    ports:
      - "8000:8000"
    volumes:
      - ./vol:/app/vol
    environment:
      - SE8_ADDR=0.0.0.0:8000
      - SE8_WORKER_COUNT=2
```

## Migrating from the old Django version

```bash
./bin/migrate \
  --source "sqlite:///path/to/legacy/vol/db.sqlite3" \
  --source-media /path/to/legacy/media \
  --target ./vol/se8.db \
  --target-media ./vol/media
```

Use `--dry-run` first to see the counts.

## Development

```bash
make test          # go test -race
make lint          # golangci-lint
make sqlc          # regenerate storage/generated
make build         # produce bin/se8 and bin/migrate
```

## Architecture

See [docs/superpowers/specs/2026-04-17-se8-go-rewrite-design.md](docs/superpowers/specs/2026-04-17-se8-go-rewrite-design.md).
```

- [ ] **Step 2: Commit**

```bash
git add readme.md
git commit -m "docs: replace README with Go-era quickstart"
```

---

## Phase 14: Cleanup

### Task 14.1: Smoke-test the full stack end-to-end

**Files:** none

- [ ] **Step 1: Full end-to-end smoke**

```bash
rm -rf vol && mkdir -p vol
make build
./bin/se8 &
SE8_PID=$!
sleep 3

# Health
curl -fsS http://127.0.0.1:8000/healthz
echo

# Read password file
PW=$(cat vol/first-run-password.txt)
echo "admin password: $PW"

# Login and capture cookie
curl -fsS -c /tmp/se8_cookies.txt \
  -d "username=admin&password=$PW" \
  http://127.0.0.1:8000/login -o /dev/null -w "login status: %{http_code}\n"

# Hit books page
curl -fsS -b /tmp/se8_cookies.txt \
  http://127.0.0.1:8000/books -o /dev/null -w "books status: %{http_code}\n"

# Trigger a crawl (but don't actually wait for it)
curl -fsS -b /tmp/se8_cookies.txt -X POST \
  http://127.0.0.1:8000/admin/start-crawl -o /dev/null -w "start-crawl status: %{http_code}\n"

# Check jobs page shows the manual job
curl -fsS -b /tmp/se8_cookies.txt \
  http://127.0.0.1:8000/jobs | grep -q "find_books:manual" && echo "jobs OK" || echo "jobs MISSING"

kill $SE8_PID
rm -f /tmp/se8_cookies.txt
```

Expected:
- `ok` from healthz
- login/books/start-crawl statuses are 302/200
- jobs page contains `find_books:manual`

- [ ] **Step 2: No commit needed (smoke test only)**

---

### Task 14.2: Remove the legacy/ directory (only after migration is verified against real data)

**Files:**
- Delete: `legacy/`

- [ ] **Step 1: Verify the user has successfully run migrate against their real data**

Manual check — the operator must confirm that `./bin/migrate` produced a correct target database from their real old SQLite. Do not skip.

- [ ] **Step 2: Remove legacy tree**

```bash
git rm -r legacy
```

- [ ] **Step 3: Commit**

```bash
git commit -m "chore: remove archived Django codebase after successful migration"
```

---

## Self-Review

### Spec coverage

| Spec section | Covered by |
|---|---|
| §2 Architecture (single process, 4 coroutine classes) | Tasks 4.2, 9.1, 10.1, 11.1 |
| §3 Directory layout | Tasks 0.1-0.3, each Phase creates its package |
| §4.1 Schema | Task 2.1 (001_init.sql) |
| §4.2 `task_key` dedup, run_at, first-run admin, unix timestamps | Tasks 4.1, 3.2, 2.1 |
| §5.1 Jobs queue | Tasks 4.1-4.2 |
| §5.2 Crawler + XPath→CSS map | Tasks 5.1-5.4 |
| §5.3 Imaging + PDF | Tasks 6.1-6.2 |
| §5.4 Task link: find_books → find_episodes → find_images → download → pdf; fix_images/fix_pdf | Tasks 8.1-8.6 |
| §6.1 Routes table | Tasks 10.1-10.6 |
| §6.2 Page templates | Tasks 10.3-10.6 |
| §6.3 HTMX fragment at `/jobs/fragment` | Task 10.6 |
| §6.4 Session flow + periodic cleanup | Task 3.2 + Task 11.1 (`cleanSessionsLoop`) |
| §7.1 Config env vars | Task 1.1 |
| §7.2 Migration tool | Tasks 12.1-12.2 |
| §7.3-7.4 Makefile, Dockerfile | Tasks 0.3, 13.1 |
| §7.5 NAS deployment docs | Task 13.3 |
| §7.6 Logs via slog + /healthz | Tasks 11.1, 10.1 |
| §8 Test strategy | Tests live in each package Task |

### Placeholder scan

Searched plan body for forbidden tokens. The only "TBD"-adjacent text:
- Task 14.1 smoke test uses a real crawl (may fail if network blocked) — but this is expected and the script handles it (start-crawl only enqueues).
- Task 14.2 depends on operator confirmation — explicitly documented, not a hand-wave.

### Type consistency

- `jobs.Queue.Enqueue(ctx, Kind, taskKey, payload, opts...)` signature matches between Task 4.1 and Task 8.x call sites.
- `crawler.Extractor.Fetch{Books,Episodes,Images,MaxPage}` used consistently from Task 5.2 onward.
- `imaging.CombineImages([][]byte)` and `imaging.ToPDF(image.Image, io.Writer)` match between Task 6 and Task 8.5.
- Template block names (`books_list_page`, `book_detail_page`, `episodes_list_page`, `episode_read_page`, `images_list_page`, `tags_list_page`, `jobs_page`, `jobs_fragment`, `login`, `layout`, `content`) are consistent between `renderTemplate` calls and `{{define ...}}` declarations.
- `Server` struct fields in Task 10.1 (DB/Auth/Queue/VolDir/Templates/Logger) match usage in Task 11.1.
- Migration uses `crawler.ExtFromMagic` which is defined in Task 5.4.

### Known caveats (explicit)

- `t.Context()` in Task 5.1 requires Go 1.24+. If the toolchain is 1.23, replace with `context.Background()`. This is called out in the task.
- `http.FileServerFS` requires Go 1.22+. We target 1.23 so this is fine.
- `image.Decode` requires each format's decoder imported via side-effect; Task 6.1 imports `gif`, `jpeg`, `png`, and `webp` — covers the 4 formats from Task 5.4's magic detection.

---

**Plan complete and saved to `docs/superpowers/plans/2026-04-17-se8-go-rewrite.md`.**
