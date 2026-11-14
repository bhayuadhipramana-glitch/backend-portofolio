# CLAUDE.md — Portfolio Backend

## Project Context
Go modular monolith + background aggregation worker proving p99 read latency < 50 ms and 5,000 RPS on a single Proxmox k3s node behind Cloudflare Tunnel, with off-site backups on Azure Blob Storage ($100 student credit budget—keep recurring cloud spend near $0).

## Build, Format, and Verification (Run Before Finishing Every Task)
- Format: `gofmt -w .`
- Static analysis: `go vet ./...`
- Tests with race detector: `go test -race ./...`
- Never mark a task complete if `gofmt -l .` produces output or tests fail.
- Every new repository, usecase, middleware, or HTTP handler MUST ship with `_test.go` unit or integration tests in the same commit.

## Hexagonal Architecture Boundaries
Actual package layout (do NOT rename or move to `internal/adapter/*`):

- `internal/domain`: Pure Go entities, value objects (`Page`, `Visibility`), domain errors, and repository/cache/broker/identity-provider interfaces. **Zero imports** from `net/http`, `pgx`, `valkey`, `nats`, `internal/handler`, or `internal/platform`.
- `internal/dto`: Inbound request DTOs and validation (`go-playground/validator`) with safe error messages.
- `internal/usecase`: Business logic, cache-aside orchestration, and transaction boundaries. Depends only on `internal/domain` interfaces.
- `internal/handler`: Chi/stdlib HTTP handlers (JSON decoding via `decodeJSON`, pagination via `parsePage`, response mapping). Never put SQL queries or cache keys here.
- `internal/middleware`: HTTP middleware (admin session guard, trusted-origin check, client-IP resolution, future rate limiting).
- `internal/pkg/response`: Shared JSON response envelope.
- `internal/platform/postgres`: `pgxpool` implementations of domain repositories.
- `internal/platform/github`: GitHub OAuth2 implementation of `domain.IdentityProvider`.
- `internal/platform/valkey`: `domain.ContentCache` as a two-layer cache (see Caching Conventions).
- Future adapters follow the same pattern: `internal/platform/nats`.
- `cmd/api`: Process entrypoint, environment config (`config.go`), router and dependency wiring.

Reads that differ between visitors and the admin take a `domain.Visibility`; the zero value is `VisibilityPublic`, so a forgotten argument fails closed. Admin-only reads live under `/api/v1/admin/...`.

## Caching Conventions (F1, Valkey 8)
- `domain.ContentCache` (`Fetch`, `Invalidate`, `Ping`) is the only cache API usecases see. It deals in `[]byte`; usecases use the generic helper `cached[T]` and `invalidate` in `internal/usecase/cache.go`.
- **L1** in-process: 3 s TTL, max 512 entries, `sync.RWMutex`, `singleflight` per key. **L2** Valkey: 120 s TTL + up to 10 % random jitter, JSON values, keys `cache:<namespace>:v<version>:<key>`.
- Namespaces: `projects`, `articles`, `profiles`, `resumes` (`domain.CacheNamespace*`). Cache keys are built in the usecase (`list:<limit>:<offset>`, `id:<id>`, `slug:<slug>`), never in handlers.
- Only `VisibilityPublic` reads are cached; admin (`VisibilityAll`) reads always hit PostgreSQL.
- Every admin `Create`/`Update`/`Delete` must call `invalidate(ctx, u.cache, <namespace>)` after the repository write succeeds. This bumps the namespace version in Valkey (`cache:ver:<namespace>`) and clears local L1. A new mutation without it serves stale data.
- Valkey is optional at runtime: an outage falls back to the database (5 s circuit-breaker cooldown, 200 ms per Valkey call) and `/readyz` answers 200 `ready (cache degraded)`. Valkey is required at startup (`VALKEY_ADDR`, default `127.0.0.1:6379`).
- Public content GETs carry `X-Cache: HIT-L1 | HIT-L2 | MISS` via `middleware.CacheStatus`. Cache errors are never leaked to clients.
- Valkey runs with `--maxmemory 128mb --maxmemory-policy allkeys-lru`, no persistence. Never store secrets, sessions or admin data in it.
- Benchmark: `k6 run loadtest/f1_read_p99.js` (thresholds: p99 < 50 ms, failures < 0.1 %).

## Database & Migration Rules (PostgreSQL 17)
- Single source of truth for schema: `migrations/` (`000001_name.up.sql` and `.down.sql`). Do NOT create `db/migrations/`.
- Always use parameterized queries (`$1`, `$2`). Never concatenate user input into SQL strings.
- Every public list query MUST enforce an explicit `LIMIT` and deterministic `ORDER BY` (include `id` as a tiebreaker). HTTP lists take `?limit=` (default 20, max 100) and `?offset=`.- Background worker writes (F4 aggregation, F9 outbox) MUST be idempotent using `INSERT ... ON CONFLICT DO UPDATE/NOTHING` keyed by event/message ID.
- Always pass `context.Context` with explicit timeouts to database, Valkey, and NATS operations.

## Security & Cloudflare Tunnel Guardrails
- **Secrets:** Never commit passwords, OAuth client secrets, or tokens in `docker-compose.yml`, k8s manifests, or code. Use `.env` (gitignored) locally and Kubernetes Secrets in k3s.
- **Sessions & OAuth (F2):** Validate cryptographic OAuth `state` cookie on callback. Session cookies must set `HttpOnly: true`, `SameSite: Lax`, and `Secure: true` whenever `APP_ENV != "local"` or `X-Forwarded-Proto == "https"` (`middleware.IsSecureRequest`); a Secure session cookie is named `__Host-session`, otherwise `session`. Logout revokes (`revoked_at`) rather than deletes; sessions must also belong to `ADMIN_GITHUB_ID`.
- **Client IP:** Never trust `X-Forwarded-For` / `X-Real-IP`. Per-IP logic uses `r.RemoteAddr`, which `middleware.ClientIP` overwrites from `CF-Connecting-IP` only when `TRUST_CLOUDFLARE_TUNNEL=true`.
- **Config:** Required settings are validated at startup in `cmd/api/config.go` (fail fast). See `.env.example` for every variable.
- **Body Limits & Errors:** Decode every `POST`/`PUT`/`PATCH` body with `handler.decodeJSON` (`http.MaxBytesReader` 1 MB, `DisallowUnknownFields`, trailing-token check), or an explicit smaller cap for F3 event batches. Never leak raw `pgx` or internal error strings in HTTP 4xx/5xx responses.
- **Timeouts:** `middleware.Timeout` (25 s) must stay below the server `WriteTimeout` (30 s). Long-lived routes (F5 SSE) need their own server/route timeout handling.
- **Public Write Abuse Protection:** Endpoints F3 (`/events`), F7 (`/demo/burst`), and F9 (`/contact`) must enforce per-IP rate limiting and strict payload/concurrency bounds before touching NATS or PostgreSQL.

## Code & Git Hygiene
- Write all code comments, logs, error messages, and commit messages in **English**.
- Commit completed features in small, atomic layers (1: migrations, 2: domain/usecase, 3: adapters, 4: tests).