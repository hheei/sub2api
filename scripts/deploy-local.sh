#!/usr/bin/env bash
# 本地交叉编译 + 本地构建镜像发布，不依赖 GitHub Actions / 任何 registry。
#
#   ./scripts/deploy-local.sh                 # 默认：镜像经 ssh 直接送上 tencent-kr
#   MODE=registry ./scripts/deploy-local.sh   # 备选：推 GHCR（需要 write:packages scope）
#   SKIP_GIT=1 ./scripts/deploy-local.sh      # 不动 git（不打 tag / 不 push）
#
# 版本号来自 backend/cmd/server/VERSION，发版前先 bump。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

VERSION="$(tr -d '[:space:]' < backend/cmd/server/VERSION)"
COMMIT="$(git rev-parse --short HEAD)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
IMAGE="${IMAGE:-ghcr.io/hheei/sub2api}"
TAG="v${VERSION}"
MODE="${MODE:-ssh}"
HOST="${HOST:-tencent-kr}"
REMOTE_DIR="${REMOTE_DIR:-/home/ubuntu/sub2api}"

echo "==> Local release ${TAG} (commit ${COMMIT}, mode ${MODE})"

if [[ -n "$(git status --porcelain)" ]]; then
  echo "WARNING: working tree is dirty; the image is built from it as-is." >&2
fi

# Dockerfile 内部完成前端构建与 CGO_ENABLED=0 交叉编译；本机与服务端同为
# x86_64 时不需要 buildx / QEMU。镜像名保持与 compose 里的 image 一致。
echo "==> docker build"
docker build \
  --build-arg VERSION="${VERSION}" \
  --build-arg COMMIT="${COMMIT}" \
  --build-arg DATE="${DATE}" \
  -t "${IMAGE}:${VERSION}" \
  -t "${IMAGE}:latest" \
  .

# 镜像不含前端源码/构建缓存，只保留 dist 打进的二进制。

if [[ "${MODE}" == "registry" ]]; then
  if ! grep -q '"ghcr.io"' "${HOME}/.docker/config.json" 2>/dev/null; then
    echo "==> docker login ghcr.io"
    gh auth token | docker login ghcr.io -u "$(gh api user --jq .login)" --password-stdin
  fi
  echo "==> docker push"
  docker push "${IMAGE}:${VERSION}"
  docker push "${IMAGE}:latest"
  echo "==> deploy via registry pull"
  ssh "${HOST}" "cd ${REMOTE_DIR} && docker compose pull sub2api && docker compose up -d sub2api"
else
  # ssh 模式：镜像直接过管道，服务端不需要凭据，也不受 registry 限流影响。
  echo "==> shipping image over ssh (this transfers the compressed image)"
  docker save "${IMAGE}:${VERSION}" "${IMAGE}:latest" \
    | gzip -1 \
    | ssh "${HOST}" 'gunzip | docker load'
  echo "==> recreate container without pulling"
  ssh "${HOST}" "cd ${REMOTE_DIR} && docker compose up -d --pull never sub2api"
fi

if [[ "${SKIP_GIT:-0}" != "1" ]]; then
  echo "==> git tag & push (SKIP_GIT=1 to skip)"
  git tag -fa "${TAG}" -m "release: ${TAG}"
  git push origin "$(git rev-parse --abbrev-ref HEAD)" --force-with-lease
  git push origin "${TAG}" --force
fi

echo "==> verify"
sleep 6
ssh "${HOST}" 'curl -fsSL http://127.0.0.1:8080/health'
echo
ssh "${HOST}" 'docker ps --format "{{.Names}}|{{.Status}}" | grep sub2api; docker image inspect ghcr.io/hheei/sub2api:latest --format "created={{.Created}}"'
echo "==> Deployment complete: ${TAG}"
