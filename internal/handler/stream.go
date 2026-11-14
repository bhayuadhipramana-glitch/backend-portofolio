package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	authmw "github.com/bhayuadhipramana-glicth/backend-portofolio/internal/middleware"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/pkg/response"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
)

const (
	// streamInterval is how often a snapshot is pushed (PRD G2: < 2 s).
	streamInterval = time.Second
	// keepaliveInterval keeps Cloudflare Tunnel from dropping an idle stream.
	keepaliveInterval = 15 * time.Second
	// maxStreamLifetime ends a stream so a forgotten tab cannot hold a slot
	// forever; EventSource reconnects on its own.
	maxStreamLifetime = 30 * time.Minute
	// clientRetry is the reconnect delay hint sent to EventSource, in ms.
	clientRetry = 3000
)

// StreamHandler serves the F5 live feed over Server-Sent Events.
type StreamHandler struct {
	uc      usecase.LiveStatsUsecase
	limiter *authmw.ConnLimiter

	interval  time.Duration
	keepalive time.Duration
	lifetime  time.Duration
}

// NewStreamHandler constructs a StreamHandler. The limiter caps concurrent
// streams globally and per client IP.
func NewStreamHandler(uc usecase.LiveStatsUsecase, limiter *authmw.ConnLimiter) *StreamHandler {
	return &StreamHandler{
		uc:        uc,
		limiter:   limiter,
		interval:  streamInterval,
		keepalive: keepaliveInterval,
		lifetime:  maxStreamLifetime,
	}
}

// Stream handles GET /api/v1/events/stream.
//
// It must be mounted outside middleware.Timeout, and it clears the connection
// write deadline itself, because the server-wide WriteTimeout would otherwise
// cut the stream after 30 seconds.
func (h *StreamHandler) Stream(w http.ResponseWriter, r *http.Request) {
	release, reason := h.limiter.Acquire(authmw.IPKey(r.RemoteAddr))
	switch reason {
	case authmw.ReasonGlobalFull:
		w.Header().Set("Retry-After", "10")
		response.Error(w, http.StatusServiceUnavailable, "live feed is at capacity, try again shortly", nil)
		return
	case authmw.ReasonIPFull:
		w.Header().Set("Retry-After", "10")
		response.Error(w, http.StatusTooManyRequests, "too many live feed connections from this address", nil)
		return
	}
	defer release()

	rc := http.NewResponseController(w)
	// ErrNotSupported only happens for writers without deadlines (tests).
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		slog.WarnContext(r.Context(), "handler: clear write deadline failed", "error", err)
	}

	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache")
	hdr.Set("Connection", "keep-alive")
	hdr.Set("X-Accel-Buffering", "no") // stop nginx-style proxies from buffering
	w.WriteHeader(http.StatusOK)

	if _, err := fmt.Fprintf(w, "retry: %d\n\n", clientRetry); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		slog.WarnContext(r.Context(), "handler: response does not support flushing", "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.lifetime)
	defer cancel()

	// First frame immediately, then one per interval.
	if !h.sendSnapshot(ctx, w, rc) {
		return
	}
	tick := time.NewTicker(h.interval)
	defer tick.Stop()
	ping := time.NewTicker(h.keepalive)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if !h.sendSnapshot(ctx, w, rc) {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		}
	}
}

// sendSnapshot writes one `snapshot` event. It returns false when the client
// is gone. A failed query skips the frame (the next tick retries) and never
// reaches the client.
func (h *StreamHandler) sendSnapshot(ctx context.Context, w http.ResponseWriter, rc *http.ResponseController) bool {
	snap, err := h.uc.Snapshot(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return false
		}
		slog.ErrorContext(ctx, "handler: live snapshot failed", "error", err)
		return true
	}
	data, err := json.Marshal(snap)
	if err != nil {
		slog.ErrorContext(ctx, "handler: marshal live snapshot failed", "error", err)
		return true
	}
	if _, err := fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data); err != nil {
		return false
	}
	return rc.Flush() == nil
}
