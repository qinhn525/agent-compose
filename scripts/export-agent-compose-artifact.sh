#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
OUT_PATH=${1:-"$ROOT_DIR/build/agent-compose"}
DOCKERFILE=${DOCKERFILE:-"$ROOT_DIR/Dockerfile"}
VERSION_VALUE=${VERSION:-$(git -C "$ROOT_DIR" describe --always --tags --long 2>/dev/null || git -C "$ROOT_DIR" rev-parse --short=12 HEAD 2>/dev/null || echo 'unknown')}
REGISTRY_MIRROR_VALUE=${REGISTRY_MIRROR:-docker.io}
GOPROXY_VALUE=${GOPROXY:-https://goproxy.cn,direct}
HTTP_PROXY_VALUE=${HTTP_PROXY:-}
HTTPS_PROXY_VALUE=${HTTPS_PROXY:-}
ALL_PROXY_VALUE=${ALL_PROXY:-}

mkdir -p "$(dirname -- "$OUT_PATH")"

iidfile=$(mktemp)
cid=""
cleanup() {
  rm -f "$iidfile"
  if [ -n "${cid:-}" ]; then
    docker rm -f "$cid" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

docker build \
  --iidfile "$iidfile" \
  -f "$DOCKERFILE" \
  --target agent-compose-artifact \
  --build-arg "VERSION=$VERSION_VALUE" \
  --build-arg "HTTP_PROXY=$HTTP_PROXY_VALUE" \
  --build-arg "HTTPS_PROXY=$HTTPS_PROXY_VALUE" \
  --build-arg "ALL_PROXY=$ALL_PROXY_VALUE" \
  --build-arg "REGISTRY_MIRROR=$REGISTRY_MIRROR_VALUE" \
  --build-arg "GOPROXY=$GOPROXY_VALUE" \
  "$ROOT_DIR"

image_id=$(tr -d "\n" <"$iidfile")
cid=$(docker create "$image_id" /out/agent-compose)

docker cp "$cid":/out/agent-compose "$OUT_PATH"
chmod +x "$OUT_PATH"
