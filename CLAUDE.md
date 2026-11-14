# CLAUDE.md — Portfolio Backend

## Project Context
Go modular monolith + background aggregation worker proving p99 read latency < 50 ms and 5,000 RPS on a single Proxmox k3s node behind Cloudflare Tunnel, with off-site backups on Azure Blob Storage ($100 student credit budget—keep recurring cloud spend near $0).

## Build, Format, and Verification (Run Before Finishing Every Task)
- Format: `gofmt -w .`
- Static analysis: `go vet ./...`
- Tests with race detector: `go test -race ./...` (needs cgo; on Windows without gcc run it in Docker: `docker run --rm -v "$PWD:/src" -w /src golang:1.24 go test -race ./...`)
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
- `internal/platform/nats`: JetStream `domain.EventPublisher` (F3) and `domain.EventSource` durable pull consumer (F4), plus stream setup.
- `internal/worker`: Background F4 aggregation loop (`Aggregator`). Depends only on `internal/domain` ports.
- `internal/platform/valkey`: `domain.ContentCache` as a two-layer cache (see Caching Conventions).
- `internal/platform/notify`: `domain.ContactNotifier` (webhook POST or log-only fallback) used by the F9 outbox worker.
- `internal/observability`: F6 OpenTelemetry adapter (metrics, optional traces, `/metrics` access guard, decorators for the cache, event bus and outbox ports).
- Future adapters follow the same pattern.
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

## Events & Aggregation (F3 NATS JetStream, F4 worker)
- `POST /api/v1/events` (public) takes one event object or an array of at most 100. Body cap 262144 bytes via `decodeJSONLimit`, `DisallowUnknownFields`, DTO validation in `internal/dto/event.go`. Answers `202`; `413` for an oversized body or batch; `429` when the per-IP token bucket (`middleware.RateLimiter`, 10 req/s, burst 20, keyed by `r.RemoteAddr`) is empty; `503` when NATS refuses the events.
- Stream `PORTFOLIO_EVENTS`, subject `events.ingest`: WorkQueue retention, `DiscardNew`, `MaxMsgs` 100k, `MaxBytes` 64 MB, file storage, 5 min dedup window. `Nats-Msg-Id` is the `event_id`. A full stream turns into 503s (backpressure), never silent loss.
- The worker is one durable pull consumer (`event-aggregator`, explicit acks, `MaxDeliver` 10) started from `cmd/api/main.go`. It writes each batch through `domain.EventRepository.InsertBatch`: one CTE statement (`INSERT ... ON CONFLICT (event_id) DO NOTHING RETURNING` feeding the per-minute `event_aggregates` upsert), so redeliveries and duplicates never reach the counters. Failed batches are nak'd with a delay, then terminated after 10 attempts.
- Shutdown order: HTTP server stops, worker finishes its in-flight batch (the write uses `context.WithoutCancel`), NATS drains, then the pool closes. Never close Postgres before the worker returns.
- NATS is required at startup (`NATS_URL`, default `nats://127.0.0.1:4222`). At runtime an outage only fails ingest (503); `/readyz` stays 200 and reports `ready (events degraded)`.
- `nats-server` has no CLI flags for store limits; they live in `deploy/nats/nats-server.conf` (64 MB memory, 256 MB file). Tests use a private stream via `natsbus.ConnectStream`, never `PORTFOLIO_EVENTS`.
- Integration tests need `docker compose up -d nats postgres` and `DATABASE_URL`; they skip when the service is unreachable.

## Live Feed (F5 SSE)
- `GET /api/v1/events/stream` (`handler.StreamHandler`) pushes an `event: snapshot` frame every 1 s: 15 gap-free per-minute buckets from `event_aggregates` plus rolling RPS, error rate and avg/max latency (`domain.LiveSnapshot`). Frames are shared by all clients through `usecase.LiveStatsUsecase` (250 ms cache), so 100 viewers cost about 4 queries/s; the worker pull wait is 250 ms, keeping event-to-stream latency under the 2 s goal (about 1 s observed, 1.5 s worst case: 1 s tick + 250 ms pull + 250 ms cache).
- **Timeout override:** the route is registered on the root router in `newRouter`, before `middleware.Timeout(25s)` is attached with `root.With(...)`. Any new long-lived route must do the same, and must call `http.NewResponseController(w).SetWriteDeadline(time.Time{})` itself, because the server `WriteTimeout` (30 s) is not per-route. Never wrap the stream in a middleware that buffers or hides `http.Flusher`/`Unwrap`.
- Send `: ping` every 15 s (Cloudflare Tunnel drops idle connections) and set `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`, `X-Accel-Buffering: no`. A stream ends after 30 min; `EventSource` reconnects (`retry: 3000`).
- **Concurrency caps** (`middleware.ConnLimiter`, never blocking): 100 streams globally (`503` + `Retry-After`), 3 per client IP (`429`). Release the slot with `defer` the moment the handler returns.
- Snapshot errors are logged and skipped; they never reach the client.

## Demo Burst (F7)
- `POST /api/v1/demo/burst` takes an optional body (`decodeOptionalJSON`, 1 KB cap, unknown fields rejected): `count` clamped 10..200 (default 100), `duration_ms` clamped 500..5000 (default 2000). Clamping lives in `domain.ClampDemoBurst`; out-of-range values are never errors.
- It answers `202` at once. `usecase.DemoBurstUsecase` then publishes synthetic events (`source: "demo-burst"`, `page_view` 50 % / `api_read` 30 % / `cache_hit` 20 %) in 100 ms slices through the normal `EventUsecase`, so they travel NATS -> worker -> Postgres -> SSE like real traffic. Never write demo events straight to the database.
- **Abuse protection, both required:** a sliding-window per-IP limit of 2 bursts/minute (`middleware.WindowLimiter`, `429` + `Retry-After`; it is deliberately not a token bucket, which would allow 3 in the first minute) and a server-wide semaphore of 2 concurrent bursts (`domain.DemoMaxConcurrency`, `503` + `Retry-After` when busy). The limiter runs before the usecase, so a throttled visitor never reaches NATS.
- Bursts run on the process context passed to `NewDemoBurstUsecase`, not the request context, so they survive the 202 response and stop on shutdown. `main` calls `Wait` before draining NATS.

## Contact Form & Outbox (F9)
- `POST /api/v1/contact` (public) decodes with `decodeJSONLimit` (32 KB, `DisallowUnknownFields`) and validates name (<=120), email (<=254), subject (<=200), message (10..4000 chars, trimmed first). A non-empty honeypot field `website` answers `202` **before validation** and writes nothing, so bots cannot tell they were dropped.
- Abuse protection: sliding-window `middleware.WindowLimiter`, 3 submissions per 10 minutes per client IP (`429` + `Retry-After`), applied before the usecase. The stored `ip_hash` is the SHA-256 hex of `r.RemoteAddr` (`usecase.HashIP`); never store or log the raw IP, and never expose `ip_hash` in a response.
- **Transactional outbox:** `contacts` and `outbox` rows are inserted in ONE Postgres transaction (`ContactRepository.CreateWithOutbox`). Never notify, call a webhook, or publish to NATS from the request path; side effects go through a new outbox `event_type` and the dispatcher. The outbox payload carries everything the notifier needs (it must not contain `ip_hash`).
- `worker.OutboxDispatcher` polls every 2 s (immediately again while work remains). `OutboxRepository.Dispatch` claims up to 10 due rows (`status IN ('pending','failed') AND next_attempt_at <= NOW()`) with `FOR UPDATE SKIP LOCKED`, delivers inside the claiming transaction and records the outcome: `sent`, `failed` (next attempt after `OutboxBackoff`: 30 s doubling, capped at 1 h) or `dead` after `domain.OutboxMaxAttempts` (5). Delivery is at-least-once, so receivers deduplicate on the `X-Outbox-Id` header.
- Notifier: `CONTACT_WEBHOOK_URL` (optional, http/https) gets the payload as a JSON POST with a 10 s timeout; any non-2xx is a failure and is retried. Unset means log-only, and logs carry ids only, never visitor data.
- Shutdown: a dispatch round that has started finishes (its context is detached from the shutdown signal). `main` waits for the dispatcher before draining NATS and before the deferred `pool.Close`.
- Admin inbox: `GET /api/v1/admin/contacts` (`RequireAdmin`, `?limit=&offset=`, newest first).

## Benchmarks & Admin pprof (F8)
- `GET /api/v1/benchmarks` is public, paginated and cache-aside under namespace `benchmarks` (`domain.CacheNamespaceBenchmarks`, `X-Cache` header). `BenchmarkUsecase.Create` calls `invalidate` after the insert, so a new run is visible at once.
- `POST /api/v1/benchmarks` is authorized by `middleware.BearerOrAdmin`: either `Authorization: Bearer <CI_BENCHMARK_TOKEN>` (GitHub Actions publishing k6 results) or, when no `Authorization` header is present, an admin session (with `TrustedOrigin`). The token is compared in constant time (`crypto/subtle` over SHA-256 digests). A bearer header never falls back to the session: a wrong token, or any bearer while `CI_BENCHMARK_TOKEN` is unset, is `401`.
- `CI_BENCHMARK_TOKEN` is optional, min 32 characters (validated at startup, `openssl rand -hex 32`), and lives in `.env` / a Kubernetes Secret / a GitHub Actions secret, never in the repo. The CI token authorizes **only** `POST /benchmarks`; it must never unlock any other route.
- `net/http/pprof` is mounted only at `/api/v1/admin/debug/pprof/*` inside the admin group (`TrustedOrigin` + `RequireAdmin`). Never import it for side effects or expose it on `DefaultServeMux`/a public router. Requests run under the 25 s `Timeout` and 30 s `WriteTimeout`, so profile with `?seconds=20` or less, e.g. `go tool pprof https://<host>/api/v1/admin/debug/pprof/profile?seconds=20` with the admin session cookie.

## Observability (F6 OpenTelemetry + Prometheus + Grafana)
- `internal/observability.Provider` (built once in `main`, `Shutdown` is deferred) owns an OTel `MeterProvider` bridged to a private Prometheus registry (not the global one, so tests stay isolated). `domain`, `usecase` and `handler` never import OpenTelemetry: ports are observed by **decorators** (`InstrumentCache`, `InstrumentPublisher`, `InstrumentSource`, `InstrumentOutbox`) and HTTP by the `Provider.HTTP` middleware. A new port worth measuring gets a decorator, not an `otel` import in a usecase.
- Metrics (Prometheus names): `portfolio_http_server_duration_seconds{http_route,http_method,http_status}` (histogram, buckets dense around 50 ms), `portfolio_cache_requests_total{cache_namespace,cache_result=HIT-L1|HIT-L2|MISS}`, `portfolio_nats_published_events_total{outcome=ok|error}`, `portfolio_nats_consumed_events_total`, `portfolio_sse_connections_active` (gauge, read from `ConnLimiter.Active` at scrape time), `portfolio_outbox_pending` (gauge, `OutboxRepository.PendingCount` at scrape time, 2 s timeout, a failed query skips the sample) and `portfolio_outbox_deliveries_total{outcome=sent|failed|dead}`, plus Go and process collectors.
- `http_route` is chi's route **pattern** (`/api/v1/projects/{id}`), never the raw path; unmatched requests are `unmatched`. Never add a label with unbounded values (ids, IPs, paths, user input). SSE responses (`text/event-stream`) are excluded from the latency histogram.
- `InstrumentCache` gives the inner cache its own `CacheTrace` and forwards the status to the caller's trace, so `X-Cache` keeps working. Wrap the cache with it in `main` before handing it to any usecase.
- `GET /metrics` is registered on the timeout router in `newRouter` and guarded by `observability.MetricsAccess(METRICS_TOKEN)`. With a token: only `Authorization: Bearer <token>` (constant time), else `401`. Without one: the peer must be loopback/private **and** the request must carry no `CF-Connecting-IP`, `Cf-Ray`, `X-Forwarded-For`, `X-Real-IP` or `Forwarded` header, else `403`, so nothing that came through the tunnel can read it. Set `METRICS_TOKEN` (min 32 chars) in production. The token unlocks nothing else.
- Traces: `OTEL_EXPORTER_OTLP_ENDPOINT` (optional absolute http(s) URL, OTLP/HTTP). Unset means a no-op tracer and no spans are created. Metrics work either way.
- Dashboard: `deploy/grafana/portfolio-dashboard.json` (import, pick the Prometheus data source). It charts p50/p95/p99 (with the 50 ms line), RPS, 5xx ratio, L1/L2/miss ratio, SSE streams, NATS published vs consumed, outbox backlog and delivery outcomes. Renaming a metric means updating the dashboard in the same commit.

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