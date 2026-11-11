#!/usr/bin/env bash
# 带验证与自动回滚的安全部署（比 deploy-local.sh 严格，推荐日常使用）。
#
#   ./scripts/deploy-safe.sh                     # 构建 → 隔离冒烟 → 回滚演练 → 上线 → 生产验证
#   STOP_AFTER=build ./scripts/deploy-safe.sh    # 只构建并传镜像
#   STOP_AFTER=smoke   ./scripts/deploy-safe.sh  # 构建 → 冒烟
#   STOP_AFTER=rollback ./scripts/deploy-safe.sh # 构建 → 冒烟 → 回滚演练（不碰生产）
#   SKIP_ROLLBACK_DRILL=1 ./scripts/deploy-safe.sh
#
# 设计要点：
#   * 镜像先在本地按目标架构构建，再经 ssh 送到服务器，服务端无需 registry 凭据。
#   * 上线前以同一镜像起一套隔离的 postgres/redis 全栈，验证健康检查、鉴权路由与迁移。
#   * 故意用坏镜像演练回滚，确认"坏版本起不来 → 旧镜像能还原并被验证"这条路径可用。
#   * 正式上线套 ERR/INT/TERM trap：任何失败都会把 :latest 指回旧镜像并重新验证。
#   * 旧镜像以上线前的 ID 打 rollback-<version>-<时间戳> 标签保留，直到验证通过。
#
# 注意：脚本里的密钥每次随机生成，只用于一次性隔离环境，不写入任何持久位置。
set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

VERSION="$(tr -d '[:space:]' < backend/cmd/server/VERSION)"
COMMIT="$(git rev-parse --short HEAD)"
# 用提交时间而不是当前时间，保证同一提交重复构建能命中层缓存。
DATE="$(git log -1 --format=%cI)"
IMAGE="${IMAGE:-ghcr.io/hheei/sub2api}"
HOST="${HOST:-tencent-kr}"
REMOTE_DIR="${REMOTE_DIR:-/home/ubuntu/sub2api}"
REMOTE_COMPOSE="${REMOTE_COMPOSE:-${REMOTE_DIR}/docker-compose.yml}"
SMOKE_PORT="${SMOKE_PORT:-18080}"
ROLLBACK_PORT="${ROLLBACK_PORT:-18081}"
STOP_AFTER="${STOP_AFTER:-deploy}"
LOG_DIR="${REPO_ROOT}/.deploy/logs"
LOG_FILE="${LOG_DIR}/deploy-${VERSION}-$(date -u +%Y%m%dT%H%M%SZ).log"

mkdir -p "${LOG_DIR}"
# 全量输出同时进日志文件，终端只保留阶段摘要。
exec > >(tee -a "${LOG_FILE}") 2>&1

SMOKE_DIR="/tmp/sub2api-smoke-${VERSION}"
ROLLBACK_DIR="/tmp/sub2api-rollback-smoke-${VERSION}"

log() { printf '\n==> %s\n' "$*"; }
die() {
  printf '\nERROR: %s\n' "$*" >&2
  if [[ "${DEPLOY_STARTED:-0}" == "1" ]] && declare -F rollback >/dev/null; then
    rollback 1
  fi
  exit 1
}

ssh_do() { ssh -o BatchMode=yes "${HOST}" "$@"; }

http_code() {
  local url="$1"
  shift
  ssh_do "curl -sS -o /dev/null -w '%{http_code}' '${url}' $*"
}

check_route() {
  local expected="$1" code="$2" label="$3"
  if [[ " ${expected} " != *" ${code} "* ]]; then
    die "${label}: expected HTTP ${expected}, got ${code}"
  fi
  printf '  ok  %-42s HTTP %s\n' "${label}" "${code}"
}

cleanup_remote() {
  local dir="$1" project="$2"
  ssh_do "docker compose -p '${project}' -f '${dir}/compose.yaml' down -v --remove-orphans" || true
  ssh_do "rm -rf '${dir}'" || true
}

log "release ${VERSION} (commit ${COMMIT}) -> ${HOST}"
printf '  image: %s\n  log:   %s\n' "${IMAGE}:${VERSION}" "${LOG_FILE}"

if [[ -n "$(git status --porcelain)" ]]; then
  printf '  WARNING: 工作区不干净，镜像按当前内容构建\n' >&2
fi

# ---------------------------------------------------------------------------
# 1. 按目标架构构建（--network=host：构建容器要能用到宿主的代理）
# ---------------------------------------------------------------------------
log "build ${IMAGE}:${VERSION}"
docker build \
  --progress=plain \
  --network=host \
  --build-arg VERSION="${VERSION}" \
  --build-arg COMMIT="${COMMIT}" \
  --build-arg DATE="${DATE}" \
  -t "${IMAGE}:${VERSION}" \
  "${REPO_ROOT}"

# ---------------------------------------------------------------------------
# 1b. 先把镜像送到服务器（只 docker load，不动 :latest，不算上线）
# ---------------------------------------------------------------------------
log "ship ${IMAGE}:${VERSION} to ${HOST}"
docker save "${IMAGE}:${VERSION}" | gzip -1 | ssh_do 'gunzip | docker load'

if [[ "${STOP_AFTER}" == "build" ]]; then
  printf '\n==> stop after build\n'
  exit 0
fi

# ---------------------------------------------------------------------------
# 2. 隔离全栈冒烟测试（生产同款镜像 + 全新 postgres/redis）
# ---------------------------------------------------------------------------
if [[ "${SKIP_SMOKE:-0}" == "1" ]]; then
  log "skip smoke test (SKIP_SMOKE=1)"
else
  log "smoke test on ${HOST}:${SMOKE_PORT}"
  ssh_do "rm -rf '${SMOKE_DIR}' && mkdir -p '${SMOKE_DIR}'"

  # 一次性密钥：每次运行随机生成，仅存在于这次隔离环境里。
  SMOKE_DB_PASS="$(openssl rand -hex 16)"
  SMOKE_ADMIN_PASS="$(openssl rand -hex 16)"
  SMOKE_JWT_SECRET="$(openssl rand -hex 32)"
  SMOKE_TOTP_KEY="$(openssl rand -hex 32)"

  cat <<YAML | ssh_do "cat > '${SMOKE_DIR}/compose.yaml'"
name: sub2api-smoke
services:
  app:
    image: ${IMAGE}:${VERSION}
    container_name: sub2api-smoke-app
    ports:
      - "127.0.0.1:${SMOKE_PORT}:8080"
    environment:
      AUTO_SETUP: "true"
      SERVER_HOST: "0.0.0.0"
      SERVER_PORT: "8080"
      SERVER_MODE: "release"
      RUN_MODE: "standard"
      DATABASE_HOST: "postgres"
      DATABASE_PORT: "5432"
      DATABASE_USER: "sub2api"
      DATABASE_PASSWORD: "${SMOKE_DB_PASS}"
      DATABASE_DBNAME: "sub2api"
      DATABASE_SSLMODE: "disable"
      REDIS_HOST: "redis"
      REDIS_PORT: "6379"
      REDIS_DB: "0"
      ADMIN_EMAIL: "smoke@example.com"
      ADMIN_PASSWORD: "${SMOKE_ADMIN_PASS}"
      JWT_SECRET: "${SMOKE_JWT_SECRET}"
      TOTP_ENCRYPTION_KEY: "${SMOKE_TOTP_KEY}"
      TZ: "UTC"
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "wget", "-q", "-T", "5", "-O", "/dev/null", "http://localhost:8080/health"]
      interval: 5s
      timeout: 5s
      retries: 30
      start_period: 20s
  postgres:
    image: postgres:18-alpine
    environment:
      POSTGRES_USER: "sub2api"
      POSTGRES_PASSWORD: "${SMOKE_DB_PASS}"
      POSTGRES_DB: "sub2api"
      PGDATA: "/var/lib/postgresql/data"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U sub2api -d sub2api"]
      interval: 5s
      timeout: 5s
      retries: 20
  redis:
    image: redis:8-alpine
    volumes:
      - redisdata:/data
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 5s
      retries: 20
volumes:
  pgdata:
  redisdata:
YAML

  if ! ssh_do "docker compose -p sub2api-smoke -f '${SMOKE_DIR}/compose.yaml' up -d --wait --wait-timeout 180"; then
    ssh_do "docker compose -p sub2api-smoke -f '${SMOKE_DIR}/compose.yaml' ps || true; docker compose -p sub2api-smoke -f '${SMOKE_DIR}/compose.yaml' logs --no-color --tail 120 app || true"
    cleanup_remote "${SMOKE_DIR}" sub2api-smoke
    die "isolated smoke stack did not become healthy"
  fi

  code="$(ssh_do "curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:${SMOKE_PORT}/health")"
  check_route "200" "${code}" "GET /health"
  code="$(http_code "http://127.0.0.1:${SMOKE_PORT}/api/v1/auth/login" "-X POST -H 'Content-Type: application/json' -d '{}'")"
  check_route "400 401" "${code}" "POST /api/v1/auth/login (invalid body)"
  code="$(http_code "http://127.0.0.1:${SMOKE_PORT}/api/v1/auth/me")"
  check_route "401" "${code}" "GET /api/v1/auth/me (no token)"
  code="$(http_code "http://127.0.0.1:${SMOKE_PORT}/v1/models")"
  check_route "401" "${code}" "GET /v1/models (no key)"

  migrations="$(ssh_do "docker compose -p sub2api-smoke -f '${SMOKE_DIR}/compose.yaml' exec -T postgres psql -U sub2api -d sub2api -Atc 'select count(*) from schema_migrations'")"
  [[ "${migrations}" =~ ^[0-9]+$ ]] || die "could not read schema_migrations count: ${migrations}"
  (( migrations >= 300 )) || die "unexpectedly few migrations applied: ${migrations}"
  printf '  ok  %-42s %s rows\n' "schema_migrations" "${migrations}"

  cleanup_remote "${SMOKE_DIR}" sub2api-smoke
fi

if [[ "${STOP_AFTER}" == "smoke" ]]; then
  printf '\n==> stop after smoke\n'
  exit 0
fi

# ---------------------------------------------------------------------------
# 3. 用坏镜像演练回滚路径（隔离环境，不碰生产）
# ---------------------------------------------------------------------------
if [[ "${SKIP_ROLLBACK_DRILL:-0}" == "1" ]]; then
  log "skip rollback drill (SKIP_ROLLBACK_DRILL=1)"
else
  log "rollback drill on ${HOST}:${ROLLBACK_PORT}"
  ssh_do "rm -rf '${ROLLBACK_DIR}' && mkdir -p '${ROLLBACK_DIR}'"
  cat <<YAML | ssh_do "cat > '${ROLLBACK_DIR}/compose.yaml'"
name: sub2api-rollback-smoke
services:
  app:
    image: rollback-smoke:latest
    container_name: sub2api-rollback-smoke-app
    ports:
      - "127.0.0.1:${ROLLBACK_PORT}:80"
    healthcheck:
      test: ["CMD", "wget", "-q", "-T", "3", "-O", "/dev/null", "http://localhost/"]
      interval: 2s
      timeout: 3s
      retries: 10
      start_period: 3s
YAML
  cat <<YAML | ssh_do "cat > '${ROLLBACK_DIR}/bad.yaml'"
name: sub2api-rollback-smoke
services:
  app:
    image: rollback-smoke:bad
    container_name: sub2api-rollback-smoke-app
    ports:
      - "127.0.0.1:${ROLLBACK_PORT}:80"
    entrypoint: ["sh", "-c", "exit 1"]
    healthcheck:
      test: ["CMD", "wget", "-q", "-T", "3", "-O", "/dev/null", "http://localhost/"]
      interval: 2s
      timeout: 3s
      retries: 3
      start_period: 1s
YAML

  ssh_do "docker tag nginx:alpine rollback-smoke:old && docker tag nginx:alpine rollback-smoke:latest && docker tag redis:8-alpine rollback-smoke:bad"
  ssh_do "docker compose -p sub2api-rollback-smoke -f '${ROLLBACK_DIR}/compose.yaml' up -d --wait --wait-timeout 60"
  old_id="$(ssh_do "docker image inspect rollback-smoke:old --format '{{.Id}}'")"

  if ssh_do "docker compose -p sub2api-rollback-smoke -f '${ROLLBACK_DIR}/bad.yaml' up -d --wait --wait-timeout 30"; then
    cleanup_remote "${ROLLBACK_DIR}" sub2api-rollback-smoke
    ssh_do "docker rmi rollback-smoke:old rollback-smoke:bad rollback-smoke:latest" || true
    die "rollback drill: the deliberately broken service unexpectedly became healthy"
  fi

  ssh_do "docker tag rollback-smoke:old rollback-smoke:latest && docker compose -p sub2api-rollback-smoke -f '${ROLLBACK_DIR}/compose.yaml' up -d --wait --wait-timeout 60"
  now_id="$(ssh_do "docker inspect sub2api-rollback-smoke-app --format '{{.Image}}'")"
  [[ "${now_id}" == "${old_id}" ]] || die "rollback drill: restored image ${now_id}, expected ${old_id}"
  printf '  ok  %-42s %s\n' "rollback restored image" "${now_id:0:19}"

  cleanup_remote "${ROLLBACK_DIR}" sub2api-rollback-smoke
  ssh_do "docker rmi rollback-smoke:old rollback-smoke:bad rollback-smoke:latest" || true
fi

if [[ "${STOP_AFTER}" == "rollback" ]]; then
  printf '\n==> stop after rollback drill\n'
  exit 0
fi

# ---------------------------------------------------------------------------
# 4. 正式上线（带自动回滚）
# ---------------------------------------------------------------------------
OLD_ID="$(ssh_do "docker inspect --format '{{.Image}}' sub2api")"
OLD_TAG="${IMAGE}:rollback-${VERSION}-$(date -u +%Y%m%d-%H%M%S)"
ssh_do "docker tag '${OLD_ID}' '${OLD_TAG}'"
printf '  previous image: %s\n  rollback tag:   %s\n' "${OLD_ID}" "${OLD_TAG}"

DEPLOY_STARTED=0
# shellcheck disable=SC2329  # 由 trap 调用
rollback() {
  local rc="${1:-$?}"
  trap - ERR INT TERM
  if [[ "${DEPLOY_STARTED}" != "1" ]]; then
    exit "${rc}"
  fi
  printf '\n!! deploy failed (rc=%s); rolling back to %s\n' "${rc}" "${OLD_ID}" >&2
  ssh_do "docker tag '${OLD_ID}' '${IMAGE}:latest'" || true
  ssh_do "cd '${REMOTE_DIR}' && docker compose up -d --pull never --wait --wait-timeout 180 sub2api" || true
  local health restored
  health="$(ssh_do "curl -fsS http://127.0.0.1:8080/health" || true)"
  restored="$(ssh_do "docker inspect --format '{{.Image}}' sub2api" || true)"
  if [[ "${restored}" != "${OLD_ID}" || "${health}" != *'"status":"ok"'* ]]; then
    printf '!! rollback verification failed: image=%s health=%s\n' "${restored}" "${health}" >&2
    exit 1
  fi
  printf '!! rollback verified: %s %s\n' "${restored}" "${health}" >&2
  exit "${rc}"
}
trap rollback ERR INT TERM

log "deploy ${IMAGE}:latest -> ${VERSION}"
ssh_do "docker tag '${IMAGE}:${VERSION}' '${IMAGE}:latest'"
DEPLOY_STARTED=1
# pull_policy: always 会让 compose 去拉 :latest，所以必须 --pull never。
ssh_do "cd '${REMOTE_DIR}' && docker compose up -d --pull never --wait --wait-timeout 240 sub2api"

log "verify production"
health="$(ssh_do "curl -fsS http://127.0.0.1:8080/health")"
[[ "${health}" == *'"status":"ok"'* ]] || die "production health: ${health}"
printf '  ok  %-42s %s\n' "GET /health" "${health}"
code="$(http_code "http://127.0.0.1:8080/api/v1/auth/login" "-X POST -H 'Content-Type: application/json' -d '{}'")"
check_route "400 401" "${code}" "POST /api/v1/auth/login (invalid body)"
code="$(http_code "http://127.0.0.1:8080/api/v1/auth/me")"
check_route "401" "${code}" "GET /api/v1/auth/me (no token)"
code="$(http_code "http://127.0.0.1:8080/v1/models")"
check_route "401" "${code}" "GET /v1/models (no key)"

if [[ -n "${migrations:-}" ]]; then
  prod_migrations="$(ssh_do "docker compose -p sub2api -f '${REMOTE_COMPOSE}' exec -T postgres psql -U sub2api -d sub2api -Atc 'select count(*) from schema_migrations'")"
  (( prod_migrations >= migrations )) || die "production migrations ${prod_migrations} < smoke ${migrations}"
  printf '  ok  %-42s %s rows\n' "production schema_migrations" "${prod_migrations}"
fi

ssh_do "docker inspect --format 'image={{.Image}} started={{.State.StartedAt}}' sub2api"

trap - ERR INT TERM
printf '\n==> deploy complete: %s (previous image kept at %s)\n' "${VERSION}" "${OLD_TAG}"
printf '    log: %s\n' "${LOG_FILE}"
