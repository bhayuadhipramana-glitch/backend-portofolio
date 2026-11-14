package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

func TestBenchmarkRepositoryRoundTrip(t *testing.T) {
	pool := newTestPool(t)
	repo := NewBenchmarkRepository(pool)
	ctx := context.Background()

	for i, scenario := range []string{"old", "new"} {
		b := &domain.Benchmark{
			CommitSHA: "6e7b16f", Branch: "main", Scenario: scenario, TargetRPS: 5000,
			AchievedRPS: 5012.5, P50MS: 3.1, P95MS: 9.8, P99MS: 21.4, ErrorRate: 0.0001,
		}
		if i == 1 {
			b.RawSummary = json.RawMessage(`{"vus": 200}`)
		}
		if err := repo.Create(ctx, b); err != nil {
			t.Fatal(err)
		}
		if !uuidRe.MatchString(b.ID) || b.CreatedAt.IsZero() {
			t.Fatalf("id/created_at not filled: %+v", b)
		}
		time.Sleep(5 * time.Millisecond)
	}

	got, err := repo.List(ctx, domain.Page{})
	if err != nil || len(got) != 2 {
		t.Fatalf("got %d rows, err %v", len(got), err)
	}
	if got[0].Scenario != "new" || got[1].Scenario != "old" {
		t.Fatalf("order = %s, %s; want newest first", got[0].Scenario, got[1].Scenario)
	}
	if got[0].P99MS != 21.4 || got[0].TargetRPS != 5000 || !strings.Contains(string(got[0].RawSummary), "200") {
		t.Fatalf("row did not round trip: %+v", got[0])
	}
	if string(got[1].RawSummary) != "{}" {
		t.Fatalf("missing raw_summary stored as %q, want {}", got[1].RawSummary)
	}
	if page, _ := repo.List(ctx, domain.Page{Limit: 1, Offset: 1}); len(page) != 1 || page[0].Scenario != "old" {
		t.Fatalf("offset page = %+v", page)
	}
}
