package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/dto"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/pkg/response"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
)

// maxDemoBodyBytes caps the tiny optional config body of a demo burst.
const maxDemoBodyBytes = 1024

// DemoHandler exposes the public F7 demo burst endpoint.
type DemoHandler struct {
	uc usecase.DemoBurstUsecase
}

// NewDemoHandler constructs a DemoHandler.
func NewDemoHandler(uc usecase.DemoBurstUsecase) *DemoHandler {
	return &DemoHandler{uc: uc}
}

// Burst handles POST /api/v1/demo/burst. The body is optional:
// {"count": 10..200 (default 100), "duration_ms": 500..5000 (default 2000)}.
// Out-of-range values are clamped. It answers 202 as soon as the burst has
// started; its events then flow through NATS, the worker and Postgres, and
// show up in the live feed. It answers 503 when the server is already running
// its maximum number of bursts. The per-IP limit is applied by middleware.
func (h *DemoHandler) Burst(w http.ResponseWriter, r *http.Request) {
	var req dto.DemoBurstRequest
	if !decodeOptionalJSON(w, r, &req, maxDemoBodyBytes) {
		return
	}
	count, duration := req.Resolve()

	if err := h.uc.Start(count, duration); err != nil {
		if errors.Is(err, domain.ErrBurstBusy) {
			w.Header().Set("Retry-After", "5")
			response.Error(w, http.StatusServiceUnavailable, "a demo burst is already running, try again shortly", nil)
			return
		}
		slog.ErrorContext(r.Context(), "handler: start demo burst failed", "error", err)
		response.Error(w, http.StatusInternalServerError, "failed to start demo burst", nil)
		return
	}

	response.Success(w, http.StatusAccepted, "demo burst started", map[string]int64{
		"count":       int64(count),
		"duration_ms": duration.Milliseconds(),
	})
}
