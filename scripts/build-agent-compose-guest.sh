#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
GUEST_IMAGE_DIR=${GUEST_IMAGE_DIR:-$ROOT_DIR/guest-images}
GUEST_IMAGE_DOCKERFILE=${GUEST_IMAGE_DOCKERFILE:-$GUEST_IMAGE_DIR/Dockerfile.agent-compose-guest}
REGISTRY_MIRROR=${REGISTRY_MIRROR:-docker.io}
PYPI_INDEX_URL=${PYPI_INDEX_URL:-https://mirrors.tuna.tsinghua.edu.cn/pypi/web/simple}
PYPI_TRUSTED_HOST=${PYPI_TRUSTED_HOST:-mirrors.tuna.tsinghua.edu.cn}
IMAGE_TAG=${IMAGE_TAG:-agent-compose-guest:latest}

docker build \
  --build-arg REGISTRY_MIRROR="$REGISTRY_MIRROR" \
  --build-arg PYPI_INDEX_URL="$PYPI_INDEX_URL" \
  --build-arg PYPI_TRUSTED_HOST="$PYPI_TRUSTED_HOST" \
  -f "$GUEST_IMAGE_DOCKERFILE" \
  -t "$IMAGE_TAG" \
  "$ROOT_DIR"

echo "Built guest image: $IMAGE_TAG"
