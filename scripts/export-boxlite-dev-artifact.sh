#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
OUT_DIR=${1:-"$ROOT_DIR/build/boxlite"}
DOCKERFILE=${DOCKERFILE:-"$ROOT_DIR/Dockerfile"}
REGISTRY_MIRROR_VALUE=${REGISTRY_MIRROR:-docker.io}
HTTP_PROXY_VALUE=${HTTP_PROXY:-}
HTTPS_PROXY_VALUE=${HTTPS_PROXY:-}
ALL_PROXY_VALUE=${ALL_PROXY:-}
NO_PROXY_VALUE=${NO_PROXY:-${no_proxy:-}}

mkdir -p "$OUT_DIR"

if [ -s "$OUT_DIR/include/boxlite.h" ] &&
  [ -s "$OUT_DIR/lib/libboxlite.a" ] &&
  [ -s "$OUT_DIR/lib/libboxlite.so" ] &&
  [ -x "$OUT_DIR/runtime/boxlite-guest" ] &&
  [ -x "$OUT_DIR/runtime/boxlite-shim" ]; then
  echo "BoxLite dev artifacts already exist in $OUT_DIR"
  exit 0
fi

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
  --target boxlite-build \
  --build-arg "HTTP_PROXY=$HTTP_PROXY_VALUE" \
  --build-arg "HTTPS_PROXY=$HTTPS_PROXY_VALUE" \
  --build-arg "ALL_PROXY=$ALL_PROXY_VALUE" \
  --build-arg "NO_PROXY=$NO_PROXY_VALUE" \
  --build-arg "REGISTRY_MIRROR=$REGISTRY_MIRROR_VALUE" \
  "$ROOT_DIR"

image_id=$(tr -d "\n" <"$iidfile")
cid=$(docker create "$image_id")

rm -rf "$OUT_DIR/include" "$OUT_DIR/lib" "$OUT_DIR/runtime"
docker cp "$cid":/out/. "$OUT_DIR"
