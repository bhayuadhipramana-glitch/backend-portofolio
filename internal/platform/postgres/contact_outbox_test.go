package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

func testContact(n string) *domain.Contact {
	return &domain.Contact{Name: "Ada " + n, Email: "ada@example.com", Subject: "Hello " + n, Message: "I would like to talk about " + n, IPHash: strings.Repeat("a", 64)}
}

func noBackoff(int) time.Duration { return -time.Second } // due again at once

func TestContactAndOutboxAreWrittenAtomically(t *testing.T) {
	pool := newTestPool(t)
	repo := NewContactRepository(pool)
	ctx := context.Background()

	c := testContact("1")
	if err := repo.CreateWithOutbox(ctx, c); err != nil {
		t.Fatal(err)
	}
	if !uuidRe.MatchString(c.ID) || c.CreatedAt.IsZero() {
		t.Fatalf("id/created_at not filled: %+v", c)
	}
	if countRows(t, pool, "contacts") != 1 || countRows(t, pool, "outbox") != 1 {
		t.Fatal("expected exactly one contact and one outbox row")
	}

	var aggType, aggID, evType, status string
	var attempts int
	var payload []byte
	err := pool.QueryRow(ctx, `SELECT aggregate_type, aggregate_id::text, event_type, status, attempts, payload FROM outbox`).
		Scan(&aggType, &aggID, &evType, &status, &attempts, &payload)
	if err != nil {
		t.Fatal(err)
	}
	if aggType != "contact" || aggID != c.ID || evType != "contact.created" || status != "pending" || attempts != 0 {
		t.Fatalf("outbox row = %s %s %s %s %d", aggType, aggID, evType, status, attempts)
	}
	var p map[string]string
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatal(err)
	}
	if p["contact_id"] != c.ID || p["email"] != "ada@example.com" || p["message"] != c.Message {
		t.Fatalf("payload = %v", p)
	}
	if _, leaked := p["ip_hash"]; leaked {
		t.Fatal("payload leaks ip_hash")
	}
}

func TestContactRollsBackWhenOutboxInsertFails(t *testing.T) {
	pool := newTestPool(t)
	repo := NewContactRepository(pool)
	ctx := context.Background()

	// Make every outbox insert fail; the contact insert before it must not survive.
	if _, err := pool.Exec(ctx, `ALTER TABLE outbox ADD CONSTRAINT outbox_always_fails CHECK (false)`); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateWithOutbox(ctx, testContact("x")); err == nil {
		t.Fatal("expected the outbox failure to surface")
	}
	if n := countRows(t, pool, "contacts"); n != 0 {
		t.Fatalf("%d contact rows left behind: the transaction is not atomic", n)
	}
	if n := countRows(t, pool, "outbox"); n != 0 {
		t.Fatalf("%d outbox rows left behind", n)
	}
}

func TestContactSchemaRejectsOversizedFields(t *testing.T) {
	pool := newTestPool(t)
	c := testContact("y")
	c.IPHash = "short" // CHAR(64) pads, but a longer value must be refused
	c.Name = strings.Repeat("n", 121)
	if err := NewContactRepository(pool).CreateWithOutbox(context.Background(), c); err == nil {
		t.Fatal("name over 120 characters was accepted")
	}
}

func TestContactListIsNewestFirstAndBounded(t *testing.T) {
	pool := newTestPool(t)
	repo := NewContactRepository(pool)
	ctx := context.Background()

	for _, n := range []string{"1", "2", "3"} {
		if err := repo.CreateWithOutbox(ctx, testContact(n)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	all, err := repo.List(ctx, domain.Page{Limit: 100})
	if err != nil || len(all) != 3 {
		t.Fatalf("got %d, err %v, want 3", len(all), err)
	}
	if !strings.HasSuffix(all[0].Subject, "3") || !strings.HasSuffix(all[2].Subject, "1") {
		t.Fatalf("order = %s, %s, %s; want newest first", all[0].Subject, all[1].Subject, all[2].Subject)
	}
	page, _ := repo.List(ctx, domain.Page{Limit: 2, Offset: 2})
	if len(page) != 1 || !strings.HasSuffix(page[0].Subject, "1") {
		t.Fatalf("offset page = %+v", page)
	}
	// A zero page must not become an unbounded query.
	if zero, err := repo.List(ctx, domain.Page{}); err != nil || len(zero) != 3 {
		t.Fatalf("zero page: %d rows, err %v", len(zero), err)
	}
}

func outboxState(t *testing.T, pool *pgxpool.Pool) (status string, attempts int, lastErr *string, sent bool, next time.Time) {
	t.Helper()
	var sentAt *time.Time
	err := pool.QueryRow(context.Background(), `SELECT status, attempts, last_error, sent_at, next_attempt_at FROM outbox`).
		Scan(&status, &attempts, &lastErr, &sentAt, &next)
	if err != nil {
		t.Fatal(err)
	}
	return status, attempts, lastErr, sentAt != nil, next
}

func TestOutboxDispatchDeliversOnceAndMarksSent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	if err := NewContactRepository(pool).CreateWithOutbox(ctx, testContact("1")); err != nil {
		t.Fatal(err)
	}
	repo := NewOutboxRepository(pool)

	var seen []domain.OutboxMessage
	deliver := func(_ context.Context, m domain.OutboxMessage) error { seen = append(seen, m); return nil }

	n, err := repo.Dispatch(ctx, 10, 5, noBackoff, deliver)
	if err != nil || n != 1 || len(seen) != 1 {
		t.Fatalf("handled %d, delivered %d, err %v", n, len(seen), err)
	}
	if seen[0].AggregateType != "contact" || !strings.Contains(string(seen[0].Payload), "ada@example.com") {
		t.Fatalf("message = %+v", seen[0])
	}
	status, attempts, lastErr, sent, _ := outboxState(t, pool)
	if status != "sent" || attempts != 1 || lastErr != nil || !sent {
		t.Fatalf("state = %s %d %v sent=%v", status, attempts, lastErr, sent)
	}

	if n, _ := repo.Dispatch(ctx, 10, 5, noBackoff, deliver); n != 0 || len(seen) != 1 {
		t.Fatalf("a sent message was delivered again (handled %d, %d deliveries)", n, len(seen))
	}
}

func TestOutboxDispatchRetriesThenGoesDead(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	if err := NewContactRepository(pool).CreateWithOutbox(ctx, testContact("1")); err != nil {
		t.Fatal(err)
	}
	repo := NewOutboxRepository(pool)
	failing := func(context.Context, domain.OutboxMessage) error { return errors.New("webhook 502") }

	for attempt := 1; attempt <= 5; attempt++ {
		n, err := repo.Dispatch(ctx, 10, 5, noBackoff, failing)
		if err != nil || n != 1 {
			t.Fatalf("attempt %d: handled %d, err %v", attempt, n, err)
		}
		status, attempts, lastErr, sent, _ := outboxState(t, pool)
		want := "failed"
		if attempt == 5 {
			want = "dead"
		}
		if status != want || attempts != attempt || lastErr == nil || *lastErr != "webhook 502" || sent {
			t.Fatalf("attempt %d: %s attempts=%d lastErr=%v sent=%v, want %s", attempt, status, attempts, lastErr, sent, want)
		}
	}

	called := false
	if n, _ := repo.Dispatch(ctx, 10, 5, noBackoff, func(context.Context, domain.OutboxMessage) error { called = true; return nil }); n != 0 || called {
		t.Fatal("a dead message was picked up again")
	}
}

func TestOutboxDispatchRespectsBackoffAndRecoversFailedMessage(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	if err := NewContactRepository(pool).CreateWithOutbox(ctx, testContact("1")); err != nil {
		t.Fatal(err)
	}
	repo := NewOutboxRepository(pool)

	before := time.Now()
	_, _ = repo.Dispatch(ctx, 10, 5, func(int) time.Duration { return time.Hour }, func(context.Context, domain.OutboxMessage) error {
		return errors.New("down")
	})
	_, _, _, _, next := outboxState(t, pool)
	if next.Before(before.Add(59 * time.Minute)) {
		t.Fatalf("next_attempt_at = %s, want about an hour ahead", next)
	}
	if n, _ := repo.Dispatch(ctx, 10, 5, noBackoff, func(context.Context, domain.OutboxMessage) error { return nil }); n != 0 {
		t.Fatal("message retried before its backoff elapsed")
	}

	if _, err := pool.Exec(ctx, `UPDATE outbox SET next_attempt_at = NOW() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.Dispatch(ctx, 10, 5, noBackoff, func(context.Context, domain.OutboxMessage) error { return nil }); n != 1 {
		t.Fatal("failed message was not retried once due")
	}
	if status, attempts, _, sent, _ := outboxState(t, pool); status != "sent" || attempts != 2 || !sent {
		t.Fatalf("state = %s attempts=%d sent=%v, want sent after 2", status, attempts, sent)
	}
}

func TestOutboxDispatchSkipsLockedRows(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	cr := NewContactRepository(pool)
	for _, n := range []string{"1", "2"} {
		if err := cr.CreateWithOutbox(ctx, testContact(n)); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewOutboxRepository(pool)

	// Worker A holds both rows (still delivering) while worker B runs.
	entered, release := make(chan struct{}), make(chan struct{})
	aDone := make(chan int, 1)
	go func() {
		first := true
		n, _ := repo.Dispatch(ctx, 10, 5, noBackoff, func(context.Context, domain.OutboxMessage) error {
			if first {
				first = false
				close(entered)
				<-release
			}
			return nil
		})
		aDone <- n
	}()
	<-entered

	delivered := 0
	n, err := repo.Dispatch(ctx, 10, 5, noBackoff, func(context.Context, domain.OutboxMessage) error { delivered++; return nil })
	if err != nil || n != 0 || delivered != 0 {
		t.Fatalf("worker B handled %d (%d deliveries), err %v; want it to skip rows locked by A", n, delivered, err)
	}

	close(release)
	if got := <-aDone; got != 2 {
		t.Fatalf("worker A handled %d, want 2", got)
	}
}
