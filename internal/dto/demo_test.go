package dto

import (
	"testing"
	"time"
)

func ip(v int) *int { return &v }

func TestDemoBurstResolveDefaultsAndClamping(t *testing.T) {
	tests := []struct {
		name     string
		req      DemoBurstRequest
		count    int
		duration time.Duration
	}{
		{"absent uses defaults", DemoBurstRequest{}, 100, 2 * time.Second},
		{"in range kept", DemoBurstRequest{Count: ip(50), DurationMS: ip(1500)}, 50, 1500 * time.Millisecond},
		{"count below min", DemoBurstRequest{Count: ip(3)}, 10, 2 * time.Second},
		{"count above max", DemoBurstRequest{Count: ip(5000)}, 200, 2 * time.Second},
		{"duration below min", DemoBurstRequest{DurationMS: ip(10)}, 100, 500 * time.Millisecond},
		{"duration above max", DemoBurstRequest{DurationMS: ip(60000)}, 100, 5 * time.Second},
		{"explicit zero is the minimum", DemoBurstRequest{Count: ip(0), DurationMS: ip(0)}, 10, 500 * time.Millisecond},
		{"negative is the minimum", DemoBurstRequest{Count: ip(-5), DurationMS: ip(-1)}, 10, 500 * time.Millisecond},
		{"exact bounds", DemoBurstRequest{Count: ip(200), DurationMS: ip(5000)}, 200, 5 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, dur := tt.req.Resolve()
			if count != tt.count || dur != tt.duration {
				t.Fatalf("got %d / %v, want %d / %v", count, dur, tt.count, tt.duration)
			}
		})
	}
}
