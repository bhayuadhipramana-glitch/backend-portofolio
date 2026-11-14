package dto

import (
	"errors"
	"strings"
	"testing"
)

const validEvent = `{"event_id":"3f2b8c1e-5d4a-4b6e-9c7d-1a2b3c4d5e6f","event_type":"page_view","source":"web","latency_ms":12.5,"occurred_at":"2026-01-02T15:04:05Z"}`

func TestParseEventsSingleAndBatch(t *testing.T) {
	reqs, err := ParseEvents([]byte(validEvent))
	if err != nil || len(reqs) != 1 {
		t.Fatalf("single: got %d reqs, err %v", len(reqs), err)
	}
	reqs, err = ParseEvents([]byte("[" + validEvent + "," + validEvent + "]"))
	if err != nil || len(reqs) != 2 {
		t.Fatalf("batch: got %d reqs, err %v", len(reqs), err)
	}
}

func TestParseEventsRejects(t *testing.T) {
	many := "[" + strings.Repeat(validEvent+",", 100) + validEvent + "]" // 101 events
	tests := []struct {
		name string
		body string
		want error
	}{
		{"empty body", ``, ErrInvalidEventBody},
		{"scalar", `42`, ErrInvalidEventBody},
		{"empty array", `[]`, ErrEmptyEventBatch},
		{"over 100 events", many, ErrEventBatchTooLarge},
		{"unknown field", `{"event_id":"x","bogus":1}`, ErrInvalidEventBody},
		{"trailing data", validEvent + ` {}`, ErrInvalidEventBody},
		{"bad json", `{"event_id":`, ErrInvalidEventBody},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseEvents([]byte(tt.body)); !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestParseEventsAcceptsExactlyMaxBatch(t *testing.T) {
	body := "[" + strings.Repeat(validEvent+",", 99) + validEvent + "]"
	reqs, err := ParseEvents([]byte(body))
	if err != nil || len(reqs) != 100 {
		t.Fatalf("got %d reqs, err %v", len(reqs), err)
	}
}

func TestValidateEvents(t *testing.T) {
	field := func(name, value string) string {
		return strings.Replace(validEvent, name, value, 1)
	}
	tests := []struct {
		name      string
		body      string
		wantField string // "" means valid
	}{
		{"valid", validEvent, ""},
		{"bad uuid", field(`3f2b8c1e-5d4a-4b6e-9c7d-1a2b3c4d5e6f`, `nope`), "events[0].event_id"},
		{"blank type", field(`"page_view"`, `"   "`), "events[0].event_type"},
		{"type too long", field(`"page_view"`, `"`+strings.Repeat("a", 65)+`"`), "events[0].event_type"},
		{"blank source", field(`"web"`, `""`), "events[0].source"},
		{"negative latency", field(`12.5`, `-1`), "events[0].latency_ms"},
		{"bad timestamp", field(`2026-01-02T15:04:05Z`, `yesterday`), "events[0].occurred_at"},
		{"payload not object", strings.Replace(validEvent, `"source"`, `"payload":[1],"source"`, 1), "events[0].payload"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqs, err := ParseEvents([]byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			errs := ValidateEvents(reqs)
			if tt.wantField == "" {
				if errs != nil {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			for _, e := range errs {
				if e.Field == tt.wantField {
					return
				}
			}
			t.Fatalf("no error for %s in %v", tt.wantField, errs)
		})
	}
}

func TestValidateEventsRequiresLatency(t *testing.T) {
	body := `{"event_id":"3f2b8c1e-5d4a-4b6e-9c7d-1a2b3c4d5e6f","event_type":"a","source":"b","occurred_at":"2026-01-02T15:04:05Z"}`
	reqs, _ := ParseEvents([]byte(body))
	if errs := ValidateEvents(reqs); len(errs) == 0 {
		t.Fatal("a missing latency_ms must be rejected, not treated as 0")
	}
}

func TestToEventsNormalises(t *testing.T) {
	body := `{"event_id":"3F2B8C1E-5D4A-4B6E-9C7D-1A2B3C4D5E6F","event_type":" page_view ","source":"web","latency_ms":0,"is_error":true,"occurred_at":"2026-01-02T17:04:05+02:00"}`
	reqs, _ := ParseEvents([]byte(body))
	if errs := ValidateEvents(reqs); errs != nil {
		t.Fatal(errs)
	}
	evs, err := ToEvents(reqs)
	if err != nil {
		t.Fatal(err)
	}
	e := evs[0]
	if e.EventID != "3f2b8c1e-5d4a-4b6e-9c7d-1a2b3c4d5e6f" || e.EventType != "page_view" || !e.IsError {
		t.Fatalf("unexpected event: %+v", e)
	}
	if got := e.OccurredAt.Format("2006-01-02T15:04:05Z07:00"); got != "2026-01-02T15:04:05Z" {
		t.Fatalf("occurred_at not normalised to UTC: %s", got)
	}
}
