-- F3/F4: raw ingested events and their per-minute rollups.
CREATE TABLE events (
    event_id    UUID             PRIMARY KEY,
    event_type  VARCHAR(64)      NOT NULL,
    source      VARCHAR(64)      NOT NULL,
    latency_ms  DOUBLE PRECISION NOT NULL CHECK (latency_ms >= 0),
    is_error    BOOLEAN          NOT NULL DEFAULT FALSE,
    payload     JSONB            NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ      NOT NULL,
    created_at  TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_events_occurred_at ON events (occurred_at DESC);

CREATE TABLE event_aggregates (
    bucket         TIMESTAMPTZ      NOT NULL,
    event_type     VARCHAR(64)      NOT NULL,
    total_count    BIGINT           NOT NULL DEFAULT 0,
    error_count    BIGINT           NOT NULL DEFAULT 0,
    sum_latency_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    max_latency_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    PRIMARY KEY (bucket, event_type)
);
