#!/usr/bin/env bash
# se8 → Portainer one-shot deploy.
#
# Cross-compiles the Go binaries, tars them with the Dockerfile, calls
# Portainer's HTTP API to build the image and recreate the container, then
# verifies /healthz. All knobs come from env vars so this works on any NAS
# with Portainer — see scripts/.env.deploy.example for the full list.
#
# Usage:
#   scripts/deploy.sh                 # full build + deploy
#   scripts/deploy.sh --skip-build    # reuse existing deploy/se8 binaries
#   scripts/deploy.sh --no-cache=0    # let docker reuse layers (faster)
#
# Required env vars: PORTAINER_USER, PORTAINER_PASS
# Everything else has defaults that match the SE8 NAS deployment.

set -euo pipefail

# ── Resolve paths ──────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
DEPLOY_DIR="${ROOT_DIR}/deploy"
TARBALL="${ROOT_DIR}/se8-build.tar.gz"

# Pull a .env.deploy override if present (gitignored, for secrets).
if [[ -f "${ROOT_DIR}/.env.deploy" ]]; then
  # shellcheck disable=SC1090,SC1091
  set -a; . "${ROOT_DIR}/.env.deploy"; set +a
fi

# ── Config (overridable) ───────────────────────────────────────────────────
PORTAINER_URL="${PORTAINER_URL:-http://192.168.13.202:9000}"
PORTAINER_ENDPOINT="${PORTAINER_ENDPOINT:-2}"
PORTAINER_USER="${PORTAINER_USER:-}"
PORTAINER_PASS="${PORTAINER_PASS:-}"

IMAGE="${IMAGE:-se8:latest}"
CONTAINER="${CONTAINER:-se8}"
HOST_PORT="${HOST_PORT:-8765}"
CONTAINER_PORT="${CONTAINER_PORT:-8000}"
VOL_HOST_PATH="${VOL_HOST_PATH:-/volume1/docker/se8/vol}"
WORKER_COUNT="${WORKER_COUNT:-8}"
TZ_VAL="${TZ_VAL:-Asia/Shanghai}"
DNS1="${DNS1:-8.8.8.8}"
DNS2="${DNS2:-1.1.1.1}"
NOCACHE="${NOCACHE:-1}"  # 1 = --no-cache=true, 0 = reuse layers

if [[ -z "${HEALTHCHECK_URL:-}" ]]; then
  # Strip scheme, then everything from the first ':' (Portainer port) to get just the host.
  _HOST="${PORTAINER_URL#*://}"
  _HOST="${_HOST%%:*}"
  HEALTHCHECK_URL="http://${_HOST}:${HOST_PORT}/healthz"
fi

# ── Flags ──────────────────────────────────────────────────────────────────
SKIP_BUILD=0
for arg in "$@"; do
  case "$arg" in
    --skip-build) SKIP_BUILD=1 ;;
    --no-cache=0) NOCACHE=0 ;;
    --no-cache=1) NOCACHE=1 ;;
    -h|--help)
      sed -n '2,15p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *) echo "unknown flag: $arg" >&2; exit 2 ;;
  esac
done

# ── Helpers ────────────────────────────────────────────────────────────────
red()    { printf '\033[31m%s\033[0m\n' "$*"; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }
blue()   { printf '\033[34m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
step()   { blue "▶ $*"; }
die()    { red "✗ $*" >&2; exit 1; }

require() {
  command -v "$1" >/dev/null 2>&1 || die "missing dependency: $1"
}

cleanup() { rm -f "${TARBALL}"; }
trap cleanup EXIT

# ── Pre-flight ─────────────────────────────────────────────────────────────
require curl
require tar
require jq
[[ "${SKIP_BUILD}" -eq 1 ]] || require go
[[ -n "${PORTAINER_USER}" ]] || die "PORTAINER_USER not set (export it or add to .env.deploy)"
[[ -n "${PORTAINER_PASS}" ]] || die "PORTAINER_PASS not set"
[[ -f "${DEPLOY_DIR}/Dockerfile" ]] || die "missing ${DEPLOY_DIR}/Dockerfile"

# ── 1. Cross-compile ───────────────────────────────────────────────────────
if [[ "${SKIP_BUILD}" -eq 0 ]]; then
  step "compiling linux/amd64 binaries → ${DEPLOY_DIR}/"
  cd "${ROOT_DIR}"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath -ldflags="-s -w" \
    -o "${DEPLOY_DIR}/se8" ./cmd/se8
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath -ldflags="-s -w" \
    -o "${DEPLOY_DIR}/migrate" ./cmd/migrate
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath -ldflags="-s -w" \
    -o "${DEPLOY_DIR}/recompress" ./cmd/recompress
  green "✓ binaries built ($(du -sh "${DEPLOY_DIR}/se8" | awk '{print $1}'))"
else
  yellow "↷ --skip-build: reusing existing ${DEPLOY_DIR}/se8"
  [[ -f "${DEPLOY_DIR}/se8" ]] || die "no se8 binary in ${DEPLOY_DIR}/"
fi

# ── 2. Tar build context ───────────────────────────────────────────────────
step "packing build context → ${TARBALL}"
tar -czf "${TARBALL}" -C "${DEPLOY_DIR}" Dockerfile se8 migrate recompress
green "✓ tarball $(du -sh "${TARBALL}" | awk '{print $1}')"

# ── 3. Auth to Portainer ───────────────────────────────────────────────────
step "auth → ${PORTAINER_URL}"
JWT=$(curl -sf -X POST "${PORTAINER_URL}/api/auth" \
  -H 'Content-Type: application/json' \
  -d "$(jq -n --arg u "${PORTAINER_USER}" --arg p "${PORTAINER_PASS}" \
        '{Username:$u, Password:$p}')" \
  | jq -r '.jwt // empty')
[[ -n "${JWT}" ]] || die "auth failed (check PORTAINER_USER / PORTAINER_PASS)"
AUTH_HEADER="Authorization: Bearer ${JWT}"

# ── 4. Build image ─────────────────────────────────────────────────────────
NOCACHE_PARAM="true"
[[ "${NOCACHE}" -eq 0 ]] && NOCACHE_PARAM="false"

step "building ${IMAGE} on endpoint ${PORTAINER_ENDPOINT} (nocache=${NOCACHE_PARAM})"
BUILD_LOG=$(curl -sf -X POST \
  "${PORTAINER_URL}/api/endpoints/${PORTAINER_ENDPOINT}/docker/build?t=${IMAGE}&dockerfile=Dockerfile&nocache=${NOCACHE_PARAM}" \
  -H "${AUTH_HEADER}" \
  -H 'Content-Type: application/x-tar' \
  --data-binary "@${TARBALL}")

# Build streams JSON lines; surface any "errorDetail".
if echo "${BUILD_LOG}" | jq -es 'map(select(.errorDetail)) | length > 0' >/dev/null 2>&1; then
  if echo "${BUILD_LOG}" | jq -es 'map(select(.errorDetail))[-1] | true' >/dev/null 2>&1 \
     && [[ "$(echo "${BUILD_LOG}" | jq -es 'map(select(.errorDetail)) | length')" -gt 0 ]]; then
    red "✗ build error:"
    echo "${BUILD_LOG}" | jq -rs 'map(select(.errorDetail)) | .[].errorDetail.message' >&2
    exit 1
  fi
fi
green "✓ image built"

# ── 5. Stop + remove old container (if present) ───────────────────────────
EXISTING=$(curl -sf -X GET \
  "${PORTAINER_URL}/api/endpoints/${PORTAINER_ENDPOINT}/docker/containers/json?all=true" \
  -H "${AUTH_HEADER}" \
  | jq -r --arg n "/${CONTAINER}" '.[] | select(.Names | index($n)) | .Id' \
  | head -n1)

if [[ -n "${EXISTING}" ]]; then
  step "removing old container ${EXISTING:0:12}"
  curl -sf -X DELETE \
    "${PORTAINER_URL}/api/endpoints/${PORTAINER_ENDPOINT}/docker/containers/${EXISTING}?force=true" \
    -H "${AUTH_HEADER}" >/dev/null
  green "✓ removed"
else
  yellow "↷ no existing container named ${CONTAINER}"
fi

# ── 6. Create new container ────────────────────────────────────────────────
step "creating ${CONTAINER}"
CREATE_PAYLOAD=$(jq -n \
  --arg img    "${IMAGE}" \
  --arg cport  "${CONTAINER_PORT}/tcp" \
  --arg hport  "${HOST_PORT}" \
  --arg vol    "${VOL_HOST_PATH}" \
  --arg dns1   "${DNS1}" \
  --arg dns2   "${DNS2}" \
  --arg addr   "SE8_ADDR=0.0.0.0:${CONTAINER_PORT}" \
  --arg wc     "SE8_WORKER_COUNT=${WORKER_COUNT}" \
  --arg vd     "SE8_VOL_DIR=/app/vol" \
  --arg tz     "TZ=${TZ_VAL}" \
  '{
    Image: $img,
    Env: [$addr, $wc, $vd, $tz],
    ExposedPorts: { ($cport): {} },
    HostConfig: {
      Binds: ["\($vol):/app/vol"],
      PortBindings: { ($cport): [{ HostPort: $hport }] },
      RestartPolicy: { Name: "unless-stopped" },
      Dns: [$dns1, $dns2]
    }
  }')

NEW_ID=$(curl -sf -X POST \
  "${PORTAINER_URL}/api/endpoints/${PORTAINER_ENDPOINT}/docker/containers/create?name=${CONTAINER}" \
  -H "${AUTH_HEADER}" \
  -H 'Content-Type: application/json' \
  -d "${CREATE_PAYLOAD}" \
  | jq -r '.Id // empty')
[[ -n "${NEW_ID}" ]] || die "create returned no Id"
green "✓ created ${NEW_ID:0:12}"

# ── 7. Start ───────────────────────────────────────────────────────────────
step "starting ${NEW_ID:0:12}"
curl -sf -X POST \
  "${PORTAINER_URL}/api/endpoints/${PORTAINER_ENDPOINT}/docker/containers/${NEW_ID}/start" \
  -H "${AUTH_HEADER}" >/dev/null
green "✓ started"

# ── 8. Health check ────────────────────────────────────────────────────────
step "waiting for ${HEALTHCHECK_URL}"
for i in 1 2 3 4 5 6 7 8 9 10; do
  if curl -sf -m 2 "${HEALTHCHECK_URL}" >/dev/null 2>&1; then
    green "✓ ${CONTAINER} healthy after ${i}s"
    echo
    green "🚀 deploy complete: http://${PORTAINER_URL#http://}:${HOST_PORT}/  →  forwarding to ${IMAGE}"
    exit 0
  fi
  sleep 1
done

red "✗ health check timed out after 10s — check container logs in Portainer"
exit 1
