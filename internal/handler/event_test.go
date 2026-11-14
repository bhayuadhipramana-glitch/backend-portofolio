package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

type fakeEventUsecase struct {
	got []domain.Event
	err error
}

func (f *fakeEventUsecase) Ingest(_ context.Context, ev []domain.Event) error {
	f.got = append(f.got, ev...)
	return f.err
}

const oneEvent = `{"event_id":"3f2b8c1e-5d4a-4b6e-9c7d-1a2b3c4d5e6f","event_type":"page_view","source":"web","latency_ms":12.5,"occurred_at":"2026-01-02T15:04:05Z"}`

func postEvents(h *EventHandler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.Ingest(rec, req)
	return rec
}

func TestEventIngestAcceptsSingleAndBatch(t *testing.T) {
	uc := &fakeEventUsecase{}
	h := NewEventHandler(uc)

	if rec := postEvents(h, oneEvent); rec.Code != http.StatusAccepted {
		t.Fatalf("single: got %d: %s", rec.Code, rec.Body)
	}
	if rec := postEvents(h, "["+oneEvent+","+oneEvent+"]"); rec.Code != http.StatusAccepted {
		t.Fatalf("batch: got %d: %s", rec.Code, rec.Body)
	}
	if len(uc.got) != 3 {
		t.Fatalf("usecase saw %d events, want 3", len(uc.got))
	}
}

func TestEventIngestLimits(t *testing.T) {
	h := NewEventHandler(&fakeEventUsecase{})

	over := "[" + strings.Repeat(oneEvent+",", domain.MaxEventBatch) + oneEvent + "]"
	if rec := postEvents(h, over); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("101 events: got %d, want 413", rec.Code)
	}

	// A body above 262144 bytes is cut off by MaxBytesReader.
	padded := `{"event_id":"x","payload":{"pad":"` + strings.Repeat("a", maxEventBodyBytes) + `"}}`
	if rec := postEvents(h, padded); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: got %d, want 413", rec.Code)
	}

	// 100 events is the maximum and must pass.
	exact := "[" + strings.Repeat(oneEvent+",", domain.MaxEventBatch-1) + oneEvent + "]"
	if rec := postEvents(h, exact); rec.Code != http.StatusAccepted {
		t.Fatalf("100 events: got %d: %s", rec.Code, rec.Body)
	}
}

func TestEventIngestRejectsBadInput(t *testing.T) {
	uc := &fakeEventUsecase{}
	h := NewEventHandler(uc)

	tests := map[string]string{
		"empty array":   `[]`,
		"unknown field": `{"event_id":"3f2b8c1e-5d4a-4b6e-9c7d-1a2b3c4d5e6f","nope":1}`,
		"not json":      `hello`,
		"bad uuid":      strings.Replace(oneEvent, "3f2b8c1e-5d4a-4b6e-9c7d-1a2b3c4d5e6f", "123", 1),
		"neg latency":   strings.Replace(oneEvent, "12.5", "-3", 1),
		"bad time":      strings.Replace(oneEvent, "2026-01-02T15:04:05Z", "2026-01-02", 1),
		"mixed batch":   "[" + oneEvent + `,{"event_id":"bad"}]`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if rec := postEvents(h, body); rec.Code != http.StatusBadRequest {
				t.Fatalf("got %d, want 400: %s", rec.Code, rec.Body)
			}
		})
	}
	if len(uc.got) != 0 {
		t.Fatalf("invalid input reached the usecase: %d events", len(uc.got))
	}
}

func TestEventIngestBusFailureDoesNotLeakDetails(t *testing.T) {
	h := NewEventHandler(&fakeEventUsecase{err: errors.Join(domain.ErrEventsUnavailable, errors.New("nats://secret-host:4222 refused"))})
	rec := postEvents(h, oneEvent)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret-host") {
		t.Fatal("internal error text leaked to the client")
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("503 should carry Retry-After")
	}

	h = NewEventHandler(&fakeEventUsecase{err: errors.New("boom")})
	if rec := postEvents(h, oneEvent); rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", rec.Code)
	}
}
