package middleware

import (
	"net"
	"sync"
)

// Reason says why ConnLimiter.Acquire refused a connection.
type Reason int

const (
	// ReasonNone means the connection was admitted.
	ReasonNone Reason = iota
	// ReasonGlobalFull means the server-wide cap is reached.
	ReasonGlobalFull
	// ReasonIPFull means this client IP already holds its share.
	ReasonIPFull
)

// ConnLimiter caps concurrent long-lived connections (SSE) globally and per
// client IP. It never blocks: a refused connection is rejected immediately.
type ConnLimiter struct {
	maxGlobal int
	maxPerIP  int

	mu     sync.Mutex
	global int
	perIP  map[string]int
}

// NewConnLimiter creates a limiter with the given caps.
func NewConnLimiter(maxGlobal, maxPerIP int) *ConnLimiter {
	return &ConnLimiter{maxGlobal: maxGlobal, maxPerIP: maxPerIP, perIP: make(map[string]int)}
}

// IPKey returns the host part of a RemoteAddr, which ClientIP has already set
// from CF-Connecting-IP when the Cloudflare tunnel is trusted.
func IPKey(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

// Acquire admits one connection for ip. On success release must be called
// exactly once (it is idempotent) when the connection ends.
func (l *ConnLimiter) Acquire(ip string) (release func(), reason Reason) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.global >= l.maxGlobal {
		return nil, ReasonGlobalFull
	}
	if l.perIP[ip] >= l.maxPerIP {
		return nil, ReasonIPFull
	}
	l.global++
	l.perIP[ip]++

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.global--
			if l.perIP[ip]--; l.perIP[ip] <= 0 {
				delete(l.perIP, ip)
			}
		})
	}, ReasonNone
}

// Active returns the number of admitted connections.
func (l *ConnLimiter) Active() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.global
}
