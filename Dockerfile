# syntax=docker/dockerfile:1

# Targets (default = the last one, "api"):
#   docker build -t portfolio-api:latest .                      API image
#   docker build --target migrate -t portfolio-migrate:latest . schema migrations (k8s Job)
# The backup image has its own file: deploy/scripts/Dockerfile.backup

ARG GO_VERSION=1.27

# ---- build: static, stripped binary ------------------------------------------
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

# Dependencies first so this layer is cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# ---- migrate: golang-migrate with the SQL files baked in ---------------------
FROM migrate/migrate:v4.18.3 AS migrate
COPY migrations /migrations
# Usage: migrate -path /migrations -database "$DATABASE_URL" up

# ---- api: distroless, non-root ------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS api
COPY --from=build /out/api /api
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/api"]
