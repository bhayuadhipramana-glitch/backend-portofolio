package middleware

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/pkg/response"
)

const (
	// maxLimiterKeys bounds the memory used by per-IP buckets.
	maxLimiterKeys = 10_000
	// limiterIdleTTL is how long an untouched bucket is kept.
	limiterIdleTTL = 5 * time.Minute
	// overflowKey is the shared bucket used once the table is full, so a flood
	// of spoofed addresses cannot grow memory or evict honest clients.
	overflowKey = "*"
)

type bucket struct {
	tokens float64
	last   time.Time
}

// RateLimiter is a per-IP token bucket. It refills at rate tokens per second
// up to burst. The key is r.RemoteAddr, which ClientIP has already set from
// CF-Connecting-IP when (and only when) the Cloudflare tunnel is trusted.
type RateLimiter struct {
	rate  float64
	burst float64
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	sweptAt time.Time
}

// NewRateLimiter creates a limiter allowing burst requests at once and rate
// requests per second sustained, per client IP.
func NewRateLimiter(rate float64, burst int) *RateLimiter {
	return newRateLimiter(rate, burst, time.Now)
}

func newRateLimiter(rate float64, burst int, now func() time.Time) *RateLimiter {
	return &RateLimiter{
		rate:    rate,
		burst:   float64(burst),
		now:     now,
		buckets: make(map[string]*bucket),
	}
}

// Allow takes one token for key. When it is denied, retryAfter says how long
// until a token is available.
func (l *RateLimiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.sweptAt) > time.Minute {
		l.sweep(now)
	}

	b, found := l.buckets[key]
	if !found {
		if len(l.buckets) >= maxLimiterKeys {
			key = overflowKey
			b, found = l.buckets[key]
		}
		if !found {
			b = &bucket{tokens: l.burst, last: now}
			l.buckets[key] = b
		}
	}

	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// sweep drops buckets idle for longer than limiterIdleTTL. Callers hold l.mu.
func (l *RateLimiter) sweep(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.last) > limiterIdleTTL {
			delete(l.buckets, k)
		}
	}
	l.sweptAt = now
}

// Middleware rejects requests over the limit with 429 and a Retry-After header.
func (l *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.RemoteAddr
		if host, _, err := net.SplitHostPort(key); err == nil {
			key = host
		}

		if ok, wait := l.Allow(key); !ok {
			secs := int(math.Ceil(wait.Seconds()))
			w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
			response.Error(w, http.StatusTooManyRequests, "too many requests", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}
