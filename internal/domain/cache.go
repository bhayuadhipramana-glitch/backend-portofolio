package domain

import (
	"context"
	"sync"
)

// Cache namespaces. Every public read is cached under one of them and every
// admin mutation invalidates the matching namespace as a whole, so a stale
// list or detail entry can never outlive a write.
const (
	CacheNamespaceProjects = "projects"
	CacheNamespaceArticles = "articles"
	CacheNamespaceProfiles = "profiles"
	CacheNamespaceResumes  = "resumes"
)

// CacheStatus tells where a cached read was served from. It backs the
// X-Cache response header.
type CacheStatus string

const (
	// CacheHitL1 means the in-process cache answered.
	CacheHitL1 CacheStatus = "HIT-L1"
	// CacheHitL2 means the shared cache (Valkey) answered.
	CacheHitL2 CacheStatus = "HIT-L2"
	// CacheMiss means the value was loaded from the source of truth.
	CacheMiss CacheStatus = "MISS"
)

// ContentCache is a two-layer, namespaced cache-aside store for serialized
// public content.
//
// Implementations must be safe for concurrent use, must collapse concurrent
// loads of the same key into one, and must never fail a read just because the
// cache backend is unreachable: they fall back to load.
type ContentCache interface {
	// Fetch returns the bytes cached under (namespace, key). On a miss it
	// calls load, stores the result and returns it. Errors from load are
	// returned as-is and never cached. Callers must treat the returned slice as
	// read-only because it may be shared with other callers.
	Fetch(ctx context.Context, namespace, key string, load func(ctx context.Context) ([]byte, error)) ([]byte, error)

	// Invalidate drops every entry of namespace so the next read reloads from
	// the source of truth.
	Invalidate(ctx context.Context, namespace string) error

	// Ping reports whether the shared layer is reachable.
	Ping(ctx context.Context) error
}

// NopCache is a ContentCache that always loads from the source of truth.
type NopCache struct{}

// Fetch calls load directly.
func (NopCache) Fetch(ctx context.Context, _, _ string, load func(context.Context) ([]byte, error)) ([]byte, error) {
	RecordCacheStatus(ctx, CacheMiss)
	return load(ctx)
}

// Invalidate does nothing.
func (NopCache) Invalidate(context.Context, string) error { return nil }

// Ping always succeeds.
func (NopCache) Ping(context.Context) error { return nil }

// CacheTrace records how the cache served a request so the HTTP layer can
// expose it without the usecase layer knowing about HTTP.
type CacheTrace struct {
	mu     sync.Mutex
	status CacheStatus
}

// Status returns the last recorded status, or "" if nothing was cached.
func (t *CacheTrace) Status() CacheStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

type cacheTraceKey struct{}

// WithCacheTrace returns a context that carries a new CacheTrace.
func WithCacheTrace(ctx context.Context) (context.Context, *CacheTrace) {
	t := &CacheTrace{}
	return context.WithValue(ctx, cacheTraceKey{}, t), t
}

// RecordCacheStatus stores status in the trace carried by ctx, if any.
func RecordCacheStatus(ctx context.Context, status CacheStatus) {
	if t, ok := ctx.Value(cacheTraceKey{}).(*CacheTrace); ok {
		t.mu.Lock()
		t.status = status
		t.mu.Unlock()
	}
}
