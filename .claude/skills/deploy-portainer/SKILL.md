---
name: deploy-portainer
description: Use when the user asks to deploy/redeploy/push/ship SE8 to the NAS, Portainer, or production — runs scripts/deploy.sh which cross-compiles the Go binaries, builds the image via Portainer's HTTP API, recreates the container with the canonical bind/env/DNS config, and verifies /healthz.
---

# Quick deploy → Portainer

End-to-end one-shot for getting a fresh `se8` build onto the Synology NAS that
hosts this project. The flow has been hand-run dozens of times in this repo —
this skill codifies it so it's reliable and minimal-typing.

## What the script does

`scripts/deploy.sh` runs eight sequential steps. Any failure aborts cleanly
(`set -euo pipefail`) and the temp tarball is removed by the EXIT trap.

1. **Cross-compile** `cmd/se8`, `cmd/migrate`, `cmd/recompress` to
   `deploy/{se8,migrate,recompress}` with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`,
   `-trimpath -ldflags="-s -w"`. Skip with `--skip-build` to reuse existing binaries.
2. **Tar** `deploy/{Dockerfile,se8,migrate,recompress}` into `se8-build.tar.gz`.
3. **Auth** `POST /api/auth` → JWT (using `PORTAINER_USER` / `PORTAINER_PASS`).
4. **Build** `POST /api/endpoints/${ENDPOINT}/docker/build` with the tar as
   `application/x-tar`. Surfaces `errorDetail.message` lines from the streamed
   build log if any layer fails.
5. **Remove old container** if one with the same name exists (`DELETE …?force=true`).
6. **Create** the new container with the canonical config:
   - `Binds: ["${VOL_HOST_PATH}:/app/vol"]`
   - `PortBindings: { "${CONTAINER_PORT}/tcp": [{HostPort: "${HOST_PORT}"}] }`
   - `RestartPolicy: unless-stopped`
   - `Dns: [8.8.8.8, 1.1.1.1]` *(necessary — the NAS bridge network can't reach the LAN DNS)*
   - `Env: [SE8_ADDR=0.0.0.0:8000, SE8_WORKER_COUNT=8, SE8_VOL_DIR=/app/vol, TZ=Asia/Shanghai]`
7. **Start** `POST /containers/${id}/start`.
8. **Health-check** `GET /healthz` once per second, up to 10s, before declaring success.

## When to use

Trigger this skill when the user asks for any of:

- "部署到 portainer" / "推到 nas" / "deploy" / "redeploy" / "ship it"
- "重启服务 / 重新部署"
- After any change to `cmd/se8/**`, `internal/**`, `deploy/Dockerfile`, or
  embedded migrations / templates / static assets.

Do NOT use this skill for:

- Local dev iteration — `go run ./cmd/se8` is faster.
- Schema-only changes that need to be applied without a redeploy — those go
  through the embedded migration runner in `internal/storage/db.go`.

## Procedure

1. **Confirm credentials are wired up.**
   - First-time per machine: `cp scripts/.env.deploy.example .env.deploy` and
     fill in `PORTAINER_USER` / `PORTAINER_PASS`. The file is `.gitignore`d.
   - Otherwise check that `.env.deploy` already exists at the repo root.

2. **Run the deploy.**
   ```bash
   bash scripts/deploy.sh
   ```
   Common variants:
   ```bash
   bash scripts/deploy.sh --skip-build    # binaries already compiled
   bash scripts/deploy.sh --no-cache=0    # let Docker reuse layers (fast)
   ```

3. **On success** the script prints:
   ```
   🚀 deploy complete: http://<nas>:8765/  →  forwarding to se8:latest
   ```

4. **On failure**, look for the red `✗` line. The most common ones in this repo:
   - `auth failed` → wrong creds in `.env.deploy`
   - `build error: ... no such file or directory` → forgot to `go build`; drop `--skip-build`
   - `health check timed out` → check container logs in Portainer; usually a
     migration error or `SE8_VOL_DIR` permission issue on `/app/vol`

## Defaults baked into deploy.sh

These match the actual NAS deployment so the script works with zero config
beyond `PORTAINER_USER` / `PORTAINER_PASS`:

| var | default |
|---|---|
| `PORTAINER_URL` | `http://192.168.13.202:9000` |
| `PORTAINER_ENDPOINT` | `2` |
| `IMAGE` | `se8:latest` |
| `CONTAINER` | `se8` |
| `HOST_PORT` | `8765` |
| `CONTAINER_PORT` | `8000` |
| `VOL_HOST_PATH` | `/volume1/docker/se8/vol` |
| `WORKER_COUNT` | `8` |
| `TZ_VAL` | `Asia/Shanghai` |
| `DNS1`, `DNS2` | `8.8.8.8`, `1.1.1.1` |

Override any of them via env or via `.env.deploy` (auto-loaded). See
`scripts/.env.deploy.example` for the full reference.

## Files this skill touches

- `scripts/deploy.sh` — the executable.
- `scripts/.env.deploy.example` — credential template.
- `.env.deploy` — local secrets (gitignored, you create it).
- `deploy/Dockerfile` — already exists, used as build context.
- `deploy/{se8,migrate,recompress}` — produced by step 1.
- `se8-build.tar.gz` — temp, removed on EXIT.

## Why not docker-compose / portainer stacks?

Tried it. Portainer stacks don't accept a tar build context over the API the
same way `/docker/build` does, and the NAS doesn't have outbound access to
push to a registry. This script is the shortest path that works given the
network constraints (GFW blocks `gcr.io`, the bridge network can't reach the
LAN DNS resolver, etc.).
