# AGENTS.md - AI Agent Development & Maintenance Guide

This document defines core rules, architectural guidelines, and workflows for AI agents working in this repository.

---

## 1. Project Overview & Fork Identity

- **Upstream Repository**: `Wei-Shaw/sub2api`
- **Current Fork**: `hheei/sub2api`
- **Production Host**: `tencent-kr` (`/home/ubuntu/sub2api`)
- **Docker Image**: `ghcr.io/hheei/sub2api:latest` / `ghcr.io/hheei/sub2api:<version>`
- **Version Convention**: `X.Y.Z-lo.N` (e.g., `0.2.17-lo.1`, defined in `backend/cmd/server/VERSION`)

### Key Fork Features
1. **Dynamic Group Rate Multiplier Expressions**:
   - Location: `backend/internal/service/group_rate_expression*.go`, `backend/ent/schema/group.go`
   - Safe sandbox evaluator parsing dynamic rate formulas at billing time.
   - Usage log records dynamic snapshot with `DYN` tag.
   - Model Plaza reflects localized dynamic group pricing.
2. **One-Click Deploy Script**:
   - Location: `scripts/deploy.sh`, `Makefile` (`./scripts/deploy.sh`)
   - Synchronizes git tag, runs CI release workflow on GHCR, SSHs to `tencent-kr`, updates container, checks health.

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
# One-click deployment
make deploy
```

What `scripts/deploy.sh` does:
1. Pushes `main` and `v<VERSION>` to GitHub.
2. Triggers the GitHub Actions `Release` workflow and watches until GHCR image is published.
3. Connects via SSH to `tencent-kr` and runs `docker compose pull sub2api && docker compose up -d sub2api`.
4. Polls `http://127.0.0.1:8080/health` until healthy.

---

## 6. Prohibited Actions (No-Gos)

- **Do NOT re-introduce upstream promotional materials**: No sponsor tables, affiliate links, trendshift badges, or star-begging banners.
- **Do NOT enable CGO**: All Go builds and cross-compilations must remain `CGO_ENABLED=0`.
- **Do NOT commit unsynced Ent schemas**: Always run `go generate ./ent` after schema changes.
- **Do NOT use npm or yarn**: Frontend dependencies are strictly managed with `pnpm`.
