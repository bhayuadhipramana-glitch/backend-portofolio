package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// memOutbox is an in-memory OutboxRepository with the same state machine as
// the PostgreSQL one: sent on success, failed with backoff, dead at the limit.
type memOutbox struct {
	mu   sync.Mutex
	rows []*memRow
}

type memRow struct {
	msg    domain.OutboxMessage
	status string
	next   time.Time
}

func (m *memOutbox) add(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = append(m.rows, &memRow{msg: domain.OutboxMessage{ID: id}, status: domain.OutboxStatusPending, next: time.Now().Add(-time.Second)})
}

func (m *memOutbox) status(id string) (string, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rows {
		if r.msg.ID == id {
			return r.status, r.msg.Attempts
		}
	}
	return "", 0
}

func (m *memOutbox) Dispatch(ctx context.Context, limit, maxAttempts int, backoff func(int) time.Duration, deliver func(context.Context, domain.OutboxMessage) error) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.rows {
		if n == limit {
			break
		}
		due := r.status == domain.OutboxStatusPending || r.status == domain.OutboxStatusFailed
		if !due || r.next.After(time.Now()) {
			continue
		}
		n++
		r.msg.Attempts++
		if err := deliver(ctx, r.msg); err == nil {
			r.status = domain.OutboxStatusSent
			continue
		}
		r.status = domain.OutboxStatusFailed
		r.next = time.Now().Add(backoff(r.msg.Attempts))
		if r.msg.Attempts >= maxAttempts {
			r.status = domain.OutboxStatusDead
		}
	}
	return n, nil
}

type fakeNotifier struct {
	mu    sync.Mutex
	err   error
	calls []string
}

func (f *fakeNotifier) Notify(_ context.Context, m domain.OutboxMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, m.ID)
	return f.err
}

func (f *fakeNotifier) setErr(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

func TestOutboxDeliversAndMarksSent(t *testing.T) {
	repo, n := &memOutbox{}, &fakeNotifier{}
	repo.add("a")
	repo.add("b")
	d, err := NewOutboxDispatcher(repo, n, quiet)
	if err != nil {
		t.Fatal(err)
	}

	handled, err := d.DispatchOnce(context.Background())
	if err != nil || handled != 2 {
		t.Fatalf("handled %d, err %v, want 2", handled, err)
	}
	for _, id := range []string{"a", "b"} {
		if st, _ := repo.status(id); st != domain.OutboxStatusSent {
			t.Errorf("%s status = %s, want sent", id, st)
		}
	}
	// Sent messages are never delivered again.
	if handled, _ := d.DispatchOnce(context.Background()); handled != 0 || len(n.calls) != 2 {
		t.Fatalf("second round handled %d, %d total deliveries, want 0 and 2", handled, len(n.calls))
	}
}

func TestOutboxRetriesWithBackoffThenGoesDead(t *testing.T) {
	repo, n := &memOutbox{}, &fakeNotifier{err: errors.New("webhook down")}
	repo.add("a")
	d, _ := NewOutboxDispatcher(repo, n, quiet)

	for attempt := 1; attempt <= domain.OutboxMaxAttempts; attempt++ {
		// Make the message due again, as if its backoff had elapsed.
		repo.mu.Lock()
		repo.rows[0].next = time.Now().Add(-time.Second)
		repo.mu.Unlock()

		if handled, err := d.DispatchOnce(context.Background()); err != nil || handled != 1 {
			t.Fatalf("attempt %d: handled %d, err %v", attempt, handled, err)
		}
		st, attempts := repo.status("a")
		want := domain.OutboxStatusFailed
		if attempt == domain.OutboxMaxAttempts {
			want = domain.OutboxStatusDead
		}
		if st != want || attempts != attempt {
			t.Fatalf("attempt %d: status %s attempts %d, want %s %d", attempt, st, attempts, want, attempt)
		}
	}

	// A dead message is never retried, even when due.
	repo.mu.Lock()
	repo.rows[0].next = time.Now().Add(-time.Hour)
	repo.mu.Unlock()
	if handled, _ := d.DispatchOnce(context.Background()); handled != 0 || len(n.calls) != domain.OutboxMaxAttempts {
		t.Fatalf("dead message redelivered: handled %d, %d deliveries", handled, len(n.calls))
	}
}

func TestOutboxFailedMessageWaitsForBackoff(t *testing.T) {
	repo, n := &memOutbox{}, &fakeNotifier{err: errors.New("down")}
	repo.add("a")
	d, _ := NewOutboxDispatcher(repo, n, quiet)

	d.DispatchOnce(context.Background())
	// Not due yet: the 30 s backoff has not elapsed.
	if handled, _ := d.DispatchOnce(context.Background()); handled != 0 || len(n.calls) != 1 {
		t.Fatalf("retried before the backoff elapsed: handled %d, %d deliveries", handled, len(n.calls))
	}

	n.setErr(nil)
	repo.mu.Lock()
	repo.rows[0].next = time.Now().Add(-time.Second)
	repo.mu.Unlock()
	d.DispatchOnce(context.Background())
	if st, attempts := repo.status("a"); st != domain.OutboxStatusSent || attempts != 2 {
		t.Fatalf("status %s after %d attempts, want sent after 2", st, attempts)
	}
}

func TestOutboxBackoffIsExponentialAndCapped(t *testing.T) {
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute}
	for i, w := range want {
		if got := OutboxBackoff(i + 1); got != w {
			t.Errorf("OutboxBackoff(%d) = %s, want %s", i+1, got, w)
		}
	}
	if got := OutboxBackoff(1000); got != time.Hour {
		t.Errorf("OutboxBackoff(1000) = %s, want the 1h cap", got)
	}
	if got := OutboxBackoff(0); got != 30*time.Second {
		t.Errorf("OutboxBackoff(0) = %s, want the base delay", got)
	}
}

func TestOutboxRunStopsCleanlyOnCancel(t *testing.T) {
	repo, n := &memOutbox{}, &fakeNotifier{}
	repo.add("a")
	d, _ := NewOutboxDispatcher(repo, n, quiet)
	d.poll = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	for {
		if st, _ := repo.status("a"); st == domain.OutboxStatusSent {
			break
		}
		select {
		case <-deadline:
			t.Fatal("message was not delivered by the running dispatcher")
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

func TestOutboxRunFinishesInFlightRoundOnCancel(t *testing.T) {
	repo := &memOutbox{}
	repo.add("a")
	started, release := make(chan struct{}), make(chan struct{})
	n := notifierFunc(func(ctx context.Context, _ domain.OutboxMessage) error {
		close(started)
		<-release
		return ctx.Err() // a cancelled context would fail the delivery
	})
	d, _ := NewOutboxDispatcher(repo, n, quiet)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx) }()

	<-started
	cancel()
	close(release)
	<-done

	if st, _ := repo.status("a"); st != domain.OutboxStatusSent {
		t.Fatalf("in-flight message ended %s, want sent (shutdown must not abort delivery)", st)
	}
}

type notifierFunc func(context.Context, domain.OutboxMessage) error

func (f notifierFunc) Notify(ctx context.Context, m domain.OutboxMessage) error { return f(ctx, m) }

func TestNewOutboxDispatcherRejectsNil(t *testing.T) {
	if _, err := NewOutboxDispatcher(nil, &fakeNotifier{}, nil); err == nil {
		t.Error("nil repository accepted")
	}
	if _, err := NewOutboxDispatcher(&memOutbox{}, nil, nil); err == nil {
		t.Error("nil notifier accepted")
	}
}
