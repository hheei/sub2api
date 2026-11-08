# AGENTS.md - AI Agent Development & Maintenance Guide

This document defines core rules, architectural guidelines, and workflows for AI agents working in this repository.

---

## 1. Project Overview & Fork Identity

- **Upstream Repository**: `Wei-Shaw/sub2api`
- **Current Fork**: `hheei/sub2api`
- **Production Host**: `tencent-kr` (`/home/ubuntu/sub2api`)
- **Docker Image**: `ghcr.io/hheei/sub2api:latest` / `ghcr.io/hheei/sub2api:<version>`（镜像名保留，但**不再由 GitHub Actions 构建**）
- **Version Convention**: `X.Y.Z-lo.N` (e.g., `0.2.17-lo.1`, defined in `backend/cmd/server/VERSION`)

### Key Fork Features
1. **Dynamic Group Rate Multiplier Expressions**:
   - Location: `backend/internal/service/group_rate_expression*.go`, `backend/ent/schema/group.go`
   - Safe sandbox evaluator parsing dynamic rate formulas at billing time.
   - Usage log records dynamic snapshot with `DYN` tag.
   - Model Plaza reflects localized dynamic group pricing.
2. **One-Click Deploy Script**:
   - Location: `scripts/deploy-local.sh`（主路径）
   - 本地 `docker build`（容器内完成前端构建 + `CGO_ENABLED=0` 交叉编译）→ 镜像经 ssh 送到 `tencent-kr` 并 `docker compose up -d --pull never` → 健康检查。全程不依赖 GitHub Actions 或任何 registry。
   - CI 版部署脚本已删除；**仓库 Actions 已关闭**，`.github/workflows/release.yml` 保留但不再触发（上游跟踪的文件，删掉每次 rebase 都要处理冲突）。

---

## 2. Tech Stack & Environment

- **Backend**: Go (Gin, Ent ORM, Google Wire DI)
  - Pure Go cross-compilation (`CGO_ENABLED=0`).
  - Frontend dist embedded via `-tags embed`.
- **Frontend**: Vue 3 + TypeScript + Vite + Pinia + Tailwind / Element Plus
  - Package manager: **pnpm v9** (`pnpm install --frozen-lockfile`).
- **Database & Cache**:
  - PostgreSQL 18
  - Redis 8
- **Reverse Proxy**: Nginx (must include `underscores_in_headers on;`)

---

## 3. Essential Agent Rules & Commands

### Code Generation (Ent & Wire)
When modifying database entities in `backend/ent/schema/`:
```bash
cd backend
go generate ./ent
go generate ./cmd/server
```
*Never manually edit generated files under `backend/ent/`.*

### Testing
Always run relevant tests before committing:
```bash
# Backend unit & integration tests
make test-backend
# Or specific test suites:
go test -v ./internal/service -run 'Rate|Plaza|Group'

# Frontend lint & critical tests
make test-frontend
```

### Building
```bash
# Build full application (frontend + backend binary)
make build

# Build backend binary only
make build-backend

# Build frontend only
make build-frontend
```

---

## 4. Upstream Rebase Workflow

When synchronizing with upstream `Wei-Shaw/sub2api`:

1. **Fetch & Rebase**:
   ```bash
   git fetch upstream
   git checkout main
   git rebase upstream/main
   ```

2. **Expected Conflict Spots**:
   - `.github/workflows/backend-ci.yml`: Keep our simplified CI triggers (`push` and `workflow_dispatch`).
   - `backend/cmd/server/VERSION`: Keep/bump local version tag format `0.X.Y-lo.N`.

3. **Post-Rebase Verification**:
   ```bash
   cd backend
   go generate ./ent
   go generate ./cmd/server
   go vet ./...
   make test
   ```

---

## 5. Deployment Workflow

Deploying updates to the production server (`tencent-kr`):

```bash
# One-click deployment (local build, no CI)
./scripts/deploy-local.sh

# 只出镜像并部署，不动 git
SKIP_GIT=1 ./scripts/deploy-local.sh

# 备选：推 GHCR（需要 token 有 write:packages）
MODE=registry ./scripts/deploy-local.sh
```

What `scripts/deploy-local.sh` does:
1. `docker build`：镜像内先构建前端，再 `CGO_ENABLED=0 GOARCH=$TARGETARCH go build -tags embed` 交叉编译后端，版本信息由 `VERSION`/`COMMIT`/`DATE` 注入。
2. ssh 模式（默认）：`docker save | gzip | ssh tencent-kr 'gunzip | docker load'`，然后 `docker compose up -d --pull never sub2api`——服务端不需要 registry 凭据。
3. `MODE=registry` 时改为 push 到 GHCR，再 `docker compose pull/up`。
4. 打 tag（`SKIP_GIT=1` 可跳过）、健康检查、打印容器状态与镜像创建时间。

> **仓库的 GitHub Actions 目前是关闭的**（`gh api repos/hheei/sub2api/actions/permissions` → `enabled:false`）。没有 CI 发版路径，唯一发布方式是 `./scripts/deploy-local.sh`。

---

## 6. Prohibited Actions (No-Gos)

- **Do NOT re-introduce upstream promotional materials**: No sponsor tables, affiliate links, trendshift badges, or star-begging banners.
- **Do NOT enable CGO**: All Go builds and cross-compilations must remain `CGO_ENABLED=0`.
- **Do NOT commit unsynced Ent schemas**: Always run `go generate ./ent` after schema changes.
- **Do NOT use npm or yarn**: Frontend dependencies are strictly managed with `pnpm`.
