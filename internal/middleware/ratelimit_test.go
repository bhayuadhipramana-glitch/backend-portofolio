package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestRateLimiterBurstThenRefill(t *testing.T) {
	now := time.Unix(1_000, 0)
	l := newRateLimiter(1, 3, func() time.Time { return now })

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("ip"); !ok {
			t.Fatalf("request %d within burst was denied", i)
		}
	}
	ok, wait := l.Allow("ip")
	if ok || wait <= 0 {
		t.Fatalf("4th request must be denied with a wait, got ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("a different IP must have its own bucket")
	}

	now = now.Add(time.Second)
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("a token must refill after 1s at 1 req/s")
	}
	if ok, _ := l.Allow("ip"); ok {
		t.Fatal("only one token should have refilled")
	}
}

func TestRateLimiterSweepsIdleAndBoundsKeys(t *testing.T) {
	now := time.Unix(1_000, 0)
	l := newRateLimiter(1, 1, func() time.Time { return now })

	l.Allow("old")
	now = now.Add(limiterIdleTTL + 2*time.Minute)
	l.Allow("new")
	if _, ok := l.buckets["old"]; ok {
		t.Fatal("idle bucket was not swept")
	}

	for i := 0; i < maxLimiterKeys+50; i++ {
		l.Allow("ip-" + strconv.Itoa(i))
	}
	if len(l.buckets) > maxLimiterKeys+1 {
		t.Fatalf("bucket table grew to %d, want at most %d", len(l.buckets), maxLimiterKeys+1)
	}
}

func TestRateLimiterMiddleware429(t *testing.T) {
	l := NewRateLimiter(0.001, 2)
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }))

	do := func(addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < 2; i++ {
		if rec := do("203.0.113.1:5000"); rec.Code != http.StatusAccepted {
			t.Fatalf("request %d: got %d", i, rec.Code)
		}
	}
	rec := do("203.0.113.1:6000") // same IP, different port
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 must carry Retry-After")
	}
	if rec := do("203.0.113.2:5000"); rec.Code != http.StatusAccepted {
		t.Fatalf("other IP got %d", rec.Code)
	}
}
