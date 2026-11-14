-- F8: k6 benchmark results published by CI.
CREATE TABLE benchmarks (
    id           UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
    commit_sha   VARCHAR(40)      NOT NULL,
    branch       VARCHAR(100)     NOT NULL DEFAULT 'main',
    scenario     VARCHAR(64)      NOT NULL,
    target_rps   INT              NOT NULL,
    achieved_rps DOUBLE PRECISION NOT NULL,
    p50_ms       DOUBLE PRECISION NOT NULL,
    p95_ms       DOUBLE PRECISION NOT NULL,
    p99_ms       DOUBLE PRECISION NOT NULL,
    error_rate   DOUBLE PRECISION NOT NULL,
    raw_summary  JSONB            NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_benchmarks_created_at ON benchmarks (created_at DESC);
