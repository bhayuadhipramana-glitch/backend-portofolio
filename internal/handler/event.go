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

// maxEventBodyBytes caps an event ingest body (a full batch of 100 events).
const maxEventBodyBytes = 262144

// EventHandler exposes the public F3 event ingest endpoint.
type EventHandler struct {
	uc usecase.EventUsecase
}

// NewEventHandler constructs an EventHandler.
func NewEventHandler(uc usecase.EventUsecase) *EventHandler {
	return &EventHandler{uc: uc}
}

// Ingest handles POST /api/v1/events. The body is one event object or an array
// of at most domain.MaxEventBatch events. It answers 202 once the event bus
// has durably accepted every event.
func (h *EventHandler) Ingest(w http.ResponseWriter, r *http.Request) {
	var raw rawBody
	if !decodeJSONLimit(w, r, &raw, maxEventBodyBytes) {
		return
	}

	reqs, err := dto.ParseEvents(raw)
	switch {
	case errors.Is(err, dto.ErrEventBatchTooLarge):
		response.Error(w, http.StatusRequestEntityTooLarge, "too many events in one request", nil)
		return
	case errors.Is(err, dto.ErrEmptyEventBatch):
		response.Error(w, http.StatusBadRequest, "at least one event is required", nil)
		return
	case err != nil:
		slog.WarnContext(r.Context(), "handler: invalid event body", "error", err)
		response.Error(w, http.StatusBadRequest, "invalid request body", nil)
		return
	}

	if errs := dto.ValidateEvents(reqs); errs != nil {
		response.Error(w, http.StatusBadRequest, "validation failed", errs)
		return
	}
	events, err := dto.ToEvents(reqs)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "invalid request body", nil)
		return
	}

	if err := h.uc.Ingest(r.Context(), events); err != nil {
		slog.ErrorContext(r.Context(), "handler: ingest events failed", "error", err)
		if errors.Is(err, domain.ErrEventsUnavailable) {
			w.Header().Set("Retry-After", "5")
			response.Error(w, http.StatusServiceUnavailable, "event ingest temporarily unavailable", nil)
			return
		}
		response.Error(w, http.StatusInternalServerError, "failed to ingest events", nil)
		return
	}

	response.Success(w, http.StatusAccepted, "events accepted", map[string]int{"accepted": len(events)})
}

// rawBody captures the undecoded JSON document so dto.ParseEvents can accept
// an object or an array and apply strict field checking itself.
type rawBody []byte

// UnmarshalJSON stores the raw bytes.
func (b *rawBody) UnmarshalJSON(data []byte) error {
	*b = append((*b)[:0], data...)
	return nil
}
