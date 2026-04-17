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
