package valkey

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// fakeKV is an in-memory kvStore. TTLs are ignored; failures are switchable.
type fakeKV struct {
	mu   sync.Mutex
	data map[string][]byte
	fail atomic.Bool
	ops  atomic.Int64
}

func newFakeKV() *fakeKV { return &fakeKV{data: map[string][]byte{}} }

var errDown = errors.New("valkey down")

func (f *fakeKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	f.ops.Add(1)
	if f.fail.Load() {
		return nil, false, errDown
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.data[key]
	return v, ok, nil
}

func (f *fakeKV) Set(_ context.Context, key string, v []byte, _ time.Duration) error {
	f.ops.Add(1)
	if f.fail.Load() {
		return errDown
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[key] = v
	return nil
}

func (f *fakeKV) Incr(_ context.Context, key string) (int64, error) {
	f.ops.Add(1)
	if f.fail.Load() {
		return 0, errDown
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n, _ := strconv.ParseInt(string(f.data[key]), 10, 64)
	n++
	f.data[key] = []byte(strconv.FormatInt(n, 10))
	return n, nil
}

func (f *fakeKV) Ping(context.Context) error {
	if f.fail.Load() {
		return errDown
	}
	return nil
}

// loader returns a loader that counts calls and returns "v<call number>".
func loader(calls *atomic.Int64) func(context.Context) ([]byte, error) {
	return func(context.Context) ([]byte, error) {
		return []byte("v" + strconv.FormatInt(calls.Add(1), 10)), nil
	}
}

// fetch runs Fetch and returns the value together with the recorded status.
func fetch(t *testing.T, c *Cache, ns, key string, load func(context.Context) ([]byte, error)) (string, domain.CacheStatus) {
	t.Helper()
	ctx, trace := domain.WithCacheTrace(context.Background())
	v, err := c.Fetch(ctx, ns, key, load)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	return string(v), trace.Status()
}

func TestL1HitSkipsEverythingElse(t *testing.T) {
	kv := newFakeKV()
	c := newCache(kv, Config{})
	var calls atomic.Int64

	if v, s := fetch(t, c, "projects", "list", loader(&calls)); v != "v1" || s != domain.CacheMiss {
		t.Fatalf("first read = %q %s, want v1 MISS", v, s)
	}
	opsAfterFill := kv.ops.Load()

	if v, s := fetch(t, c, "projects", "list", loader(&calls)); v != "v1" || s != domain.CacheHitL1 {
		t.Fatalf("second read = %q %s, want v1 HIT-L1", v, s)
	}
	if calls.Load() != 1 {
		t.Errorf("loader ran %d times, want 1", calls.Load())
	}
	if kv.ops.Load() != opsAfterFill {
		t.Error("an L1 hit must not touch Valkey")
	}
}

func TestL1EntryExpires(t *testing.T) {
	c := newCache(newFakeKV(), Config{L1TTL: 3 * time.Second})
	now := time.Now()
	c.now = func() time.Time { return now }
	var calls atomic.Int64

	fetch(t, c, "projects", "k", loader(&calls))
	now = now.Add(2 * time.Second)
	if _, s := fetch(t, c, "projects", "k", loader(&calls)); s != domain.CacheHitL1 {
		t.Fatalf("within TTL: status %s, want HIT-L1", s)
	}
	now = now.Add(2 * time.Second) // 4s after the fill, past the 3s TTL
	if _, s := fetch(t, c, "projects", "k", loader(&calls)); s != domain.CacheHitL2 {
		t.Fatalf("after L1 TTL the shared layer must answer: status %s, want HIT-L2", s)
	}
	if calls.Load() != 1 {
		t.Errorf("loader ran %d times, want 1", calls.Load())
	}
}

func TestL2HitWhenL1IsCold(t *testing.T) {
	kv := newFakeKV()
	var calls atomic.Int64

	// Two caches sharing one Valkey stand in for "L1 expired" / a restarted process.
	first := newCache(kv, Config{})
	fetch(t, first, "articles", "id:1", loader(&calls))

	second := newCache(kv, Config{})
	v, s := fetch(t, second, "articles", "id:1", loader(&calls))
	if v != "v1" || s != domain.CacheHitL2 {
		t.Fatalf("cold L1 read = %q %s, want v1 HIT-L2", v, s)
	}
	if calls.Load() != 1 {
		t.Errorf("loader ran %d times, want 1", calls.Load())
	}
	if _, s := fetch(t, second, "articles", "id:1", loader(&calls)); s != domain.CacheHitL1 {
		t.Errorf("an L2 hit must warm L1: status %s", s)
	}
}

func TestFallsBackToLoaderWhenValkeyIsDown(t *testing.T) {
	kv := newFakeKV()
	kv.fail.Store(true)
	c := newCache(kv, Config{L1TTL: time.Millisecond})
	now := time.Now()
	c.now = func() time.Time { return now }
	var calls atomic.Int64

	v, s := fetch(t, c, "profiles", "list", loader(&calls))
	if v != "v1" || s != domain.CacheMiss {
		t.Fatalf("outage read = %q %s, want v1 MISS", v, s)
	}

	// While cooling down, Valkey must not be contacted at all.
	opsAfterFirst := kv.ops.Load()
	now = now.Add(time.Second) // L1 expired, cooldown (5s) still running
	if v, _ := fetch(t, c, "profiles", "list", loader(&calls)); v != "v2" {
		t.Fatalf("read during cooldown = %q, want v2", v)
	}
	if kv.ops.Load() != opsAfterFirst {
		t.Error("Valkey was contacted during the cooldown")
	}

	// Once Valkey is back and the cooldown is over, L2 works again.
	kv.fail.Store(false)
	now = now.Add(10 * time.Second)
	if _, s := fetch(t, c, "profiles", "list", loader(&calls)); s != domain.CacheMiss {
		t.Fatalf("first read after recovery: status %s, want MISS (and an L2 fill)", s)
	}
	now = now.Add(time.Second)
	if _, s := fetch(t, c, "profiles", "list", loader(&calls)); s != domain.CacheHitL2 {
		t.Fatalf("second read after recovery: status %s, want HIT-L2", s)
	}
}

func TestLoaderErrorIsReturnedAndNeverCached(t *testing.T) {
	c := newCache(newFakeKV(), Config{})
	boom := errors.New("db down")

	_, err := c.Fetch(context.Background(), "projects", "k", func(context.Context) ([]byte, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	var calls atomic.Int64
	if v, s := fetch(t, c, "projects", "k", loader(&calls)); v != "v1" || s != domain.CacheMiss {
		t.Errorf("after a failed load = %q %s, want v1 MISS", v, s)
	}
}

func TestSingleflightCollapsesStampede(t *testing.T) {
	c := newCache(newFakeKV(), Config{})
	var calls atomic.Int64
	release := make(chan struct{})

	slow := func(context.Context) ([]byte, error) {
		calls.Add(1)
		<-release // hold the flight open until every caller has joined
		return []byte("payload"), nil
	}

	const callers = 64
	var wg sync.WaitGroup
	results := make([]string, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := c.Fetch(context.Background(), "projects", "hot", slow)
			if err != nil {
				t.Errorf("caller %d: %v", i, err)
				return
			}
			results[i] = string(v)
		}()
	}

	// Wait until the loader is running, give the rest time to pile up behind it.
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("loader ran %d times for %d concurrent callers, want 1", got, callers)
	}
	for i, r := range results {
		if r != "payload" {
			t.Errorf("caller %d got %q", i, r)
		}
	}
}

func TestCallerCancellationDoesNotKillSharedFlight(t *testing.T) {
	c := newCache(newFakeKV(), Config{})
	release := make(chan struct{})
	started := make(chan struct{})

	slow := func(ctx context.Context) ([]byte, error) {
		close(started)
		select {
		case <-release:
			return []byte("ok"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan error, 1)
	go func() {
		_, err := c.Fetch(ctx1, "projects", "k", slow)
		done1 <- err
	}()
	<-started

	done2 := make(chan string, 1)
	go func() {
		v, _ := c.Fetch(context.Background(), "projects", "k", slow)
		done2 <- string(v)
	}()
	time.Sleep(20 * time.Millisecond)

	cancel1() // the first caller leaves ...
	if err := <-done1; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller: %v, want context.Canceled", err)
	}
	close(release) // ... and the flight must still complete for the second.
	if v := <-done2; v != "ok" {
		t.Fatalf("second caller got %q, want ok", v)
	}
}

func TestInvalidateDropsL1AndL2(t *testing.T) {
	kv := newFakeKV()
	c := newCache(kv, Config{})
	other := newCache(kv, Config{}) // a second process with its own cold L1
	var calls atomic.Int64

	fetch(t, c, "projects", "list", loader(&calls))
	fetch(t, c, "projects", "id:1", loader(&calls))
	fetch(t, c, "articles", "list", loader(&calls)) // another namespace
	calls.Store(10)

	if err := c.Invalidate(context.Background(), "projects"); err != nil {
		t.Fatal(err)
	}

	if v, s := fetch(t, c, "projects", "list", loader(&calls)); v != "v11" || s != domain.CacheMiss {
		t.Errorf("list after invalidate = %q %s, want v11 MISS", v, s)
	}
	if _, s := fetch(t, c, "projects", "id:1", loader(&calls)); s != domain.CacheMiss {
		t.Errorf("detail after invalidate: status %s, want MISS (whole namespace is dropped)", s)
	}
	// A different process must not find the old L2 entry either.
	if _, s := fetch(t, other, "projects", "list", loader(&calls)); s != domain.CacheHitL2 {
		t.Errorf("fresh L2 entry written after invalidate: status %s, want HIT-L2", s)
	}
	// Other namespaces are untouched.
	if _, s := fetch(t, c, "articles", "list", loader(&calls)); s != domain.CacheHitL1 {
		t.Errorf("unrelated namespace: status %s, want HIT-L1", s)
	}
}

func TestInvalidateRacingWithLoadNeverLeavesStaleData(t *testing.T) {
	kv := newFakeKV()
	c := newCache(kv, Config{})

	// The loader reads "old", then an admin write + invalidation lands before
	// the loader's result is stored.
	stale := func(ctx context.Context) ([]byte, error) {
		if err := c.Invalidate(ctx, "projects"); err != nil {
			t.Error(err)
		}
		return []byte("old"), nil
	}
	if v, _ := fetch(t, c, "projects", "k", stale); v != "old" {
		t.Fatalf("racing read = %q", v)
	}

	var calls atomic.Int64
	if v, s := fetch(t, c, "projects", "k", loader(&calls)); v != "v1" || s != domain.CacheMiss {
		t.Fatalf("read after racing invalidate = %q %s, want fresh v1 MISS", v, s)
	}
}

func TestInvalidateReportsValkeyFailureButClearsL1(t *testing.T) {
	kv := newFakeKV()
	c := newCache(kv, Config{})
	var calls atomic.Int64
	fetch(t, c, "projects", "k", loader(&calls))

	kv.fail.Store(true)
	if err := c.Invalidate(context.Background(), "projects"); err == nil {
		t.Fatal("a failed version bump must be reported")
	}
	if v, _ := fetch(t, c, "projects", "k", loader(&calls)); v != "v2" {
		t.Errorf("local L1 must be cleared even when Valkey fails: got %q", v)
	}
}

func TestL1IsBounded(t *testing.T) {
	c := newCache(nil, Config{L1MaxEntries: 8})
	var calls atomic.Int64
	for i := range 100 {
		fetch(t, c, "projects", "k"+strconv.Itoa(i), loader(&calls))
	}
	c.mu.RLock()
	n := len(c.l1)
	c.mu.RUnlock()
	if n > 8 {
		t.Fatalf("L1 holds %d entries, cap is 8", n)
	}
}

func TestL2TTLJitterStaysInRange(t *testing.T) {
	c := newCache(nil, Config{L2TTL: 100 * time.Second, L2Jitter: 0.1})
	distinct := map[time.Duration]bool{}
	for range 200 {
		ttl := c.l2TTL()
		if ttl < 100*time.Second || ttl > 110*time.Second {
			t.Fatalf("ttl %v outside [100s, 110s]", ttl)
		}
		distinct[ttl] = true
	}
	if len(distinct) < 2 {
		t.Error("jitter produced a single TTL")
	}
}

func TestL1OnlyCacheWorksWithoutValkey(t *testing.T) {
	c := NewCache(nil, Config{})
	var calls atomic.Int64
	if _, s := fetch(t, c, "projects", "k", loader(&calls)); s != domain.CacheMiss {
		t.Errorf("status %s, want MISS", s)
	}
	if _, s := fetch(t, c, "projects", "k", loader(&calls)); s != domain.CacheHitL1 {
		t.Errorf("status %s, want HIT-L1", s)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Errorf("L1-only ping: %v", err)
	}
	if err := c.Invalidate(context.Background(), "projects"); err != nil {
		t.Errorf("L1-only invalidate: %v", err)
	}
}

func TestPingReflectsValkeyHealth(t *testing.T) {
	kv := newFakeKV()
	c := newCache(kv, Config{})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	kv.fail.Store(true)
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("ping must fail while Valkey is down")
	}
}
