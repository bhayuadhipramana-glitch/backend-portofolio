package dto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

var (
	// ErrEmptyEventBatch is returned for an empty array.
	ErrEmptyEventBatch = errors.New("event batch is empty")
	// ErrEventBatchTooLarge is returned for more than domain.MaxEventBatch events.
	ErrEventBatchTooLarge = errors.New("event batch is too large")
	// ErrInvalidEventBody is returned when the body is neither an event nor an array of events.
	ErrInvalidEventBody = errors.New("invalid event body")
)

// EventRequest is the inbound shape of one event for POST /api/v1/events.
type EventRequest struct {
	EventID    string          `json:"event_id"    validate:"required,uuid"`
	EventType  string          `json:"event_type"  validate:"required,notblank,max=64"`
	Source     string          `json:"source"      validate:"required,notblank,max=64"`
	LatencyMS  *float64        `json:"latency_ms"  validate:"required,gte=0"`
	IsError    bool            `json:"is_error"`
	Payload    json.RawMessage `json:"payload"     validate:"omitempty,jsonobject"`
	OccurredAt string          `json:"occurred_at" validate:"required,rfc3339"`
}

// ParseEvents decodes a body that is either one event object or an array of
// events. Unknown fields and trailing data are rejected, and the batch size is
// bounded by domain.MaxEventBatch.
func ParseEvents(body []byte) ([]EventRequest, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, ErrInvalidEventBody
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()

	var reqs []EventRequest
	switch trimmed[0] {
	case '[':
		if err := dec.Decode(&reqs); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidEventBody, err)
		}
	case '{':
		var one EventRequest
		if err := dec.Decode(&one); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidEventBody, err)
		}
		reqs = []EventRequest{one}
	default:
		return nil, ErrInvalidEventBody
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidEventBody
	}

	switch {
	case len(reqs) == 0:
		return nil, ErrEmptyEventBatch
	case len(reqs) > domain.MaxEventBatch:
		return nil, ErrEventBatchTooLarge
	}
	return reqs, nil
}

// ValidateEvents validates every request and returns the errors keyed by
// position, such as "events[2].event_id". It returns nil when all are valid.
func ValidateEvents(reqs []EventRequest) []FieldError {
	var out []FieldError
	for i := range reqs {
		for _, fe := range Validate(&reqs[i]) {
			out = append(out, FieldError{
				Field:   fmt.Sprintf("events[%d].%s", i, fe.Field),
				Message: fe.Message,
			})
		}
	}
	return out
}

// ToEvents converts validated requests into domain events. Call it only after
// ValidateEvents returned nil.
func ToEvents(reqs []EventRequest) ([]domain.Event, error) {
	events := make([]domain.Event, 0, len(reqs))
	for _, r := range reqs {
		at, err := time.Parse(time.RFC3339, r.OccurredAt)
		if err != nil || r.LatencyMS == nil {
			return nil, ErrInvalidEventBody
		}
		events = append(events, domain.Event{
			EventID:    strings.ToLower(r.EventID),
			EventType:  strings.TrimSpace(r.EventType),
			Source:     strings.TrimSpace(r.Source),
			LatencyMS:  *r.LatencyMS,
			IsError:    r.IsError,
			Payload:    r.Payload,
			OccurredAt: at.UTC(),
		})
	}
	return events, nil
}
