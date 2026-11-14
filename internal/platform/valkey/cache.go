package valkey

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// Config tunes the two cache layers. Zero values fall back to the defaults.
type Config struct {
	L1TTL        time.Duration // lifetime of an in-process entry (default 3s)
	L1MaxEntries int           // hard cap on in-process entries (default 512)
	L2TTL        time.Duration // base lifetime of a Valkey entry (default 120s)
	L2Jitter     float64       // extra random TTL as a fraction of L2TTL (default 0.1)
	OpTimeout    time.Duration // per Valkey round trip (default 200ms)
	LoadTimeout  time.Duration // whole fill: version, L2 read, DB load, L2 write (default 5s)
	Cooldown     time.Duration // how long L2 is skipped after a failure (default 5s)
}

func (c Config) withDefaults() Config {
	if c.L1TTL <= 0 {
		c.L1TTL = 3 * time.Second
	}
	if c.L1MaxEntries <= 0 {
		c.L1MaxEntries = 512
	}
	if c.L2TTL <= 0 {
		c.L2TTL = 120 * time.Second
	}
	if c.L2Jitter <= 0 {
		c.L2Jitter = 0.1
	}
	if c.OpTimeout <= 0 {
		c.OpTimeout = 200 * time.Millisecond
	}
	if c.LoadTimeout <= 0 {
		c.LoadTimeout = 5 * time.Second
	}
	if c.Cooldown <= 0 {
		c.Cooldown = 5 * time.Second
	}
	return c
}

type l1Entry struct {
	value   []byte
	expires time.Time
}

// fetchResult is what one singleflight execution hands to every waiting caller.
type fetchResult struct {
	value  []byte
	status domain.CacheStatus
}

// Cache is a domain.ContentCache with an in-process L1 (short TTL, bounded,
// guarded by a RWMutex) and a Valkey L2 (longer TTL with jitter). Concurrent
// misses on one key are collapsed by singleflight, so a stampede costs a
// single trip to the database.
//
// Invalidation bumps a per-namespace version in Valkey. L2 keys embed that
// version, so old entries become unreachable at once and simply expire.
// Invalidation also clears this process' L1 immediately; another process would
// serve its L1 for at most L1TTL (this deployment runs a single API instance).
type Cache struct {
	kv  kvStore // nil means L1 only
	cfg Config
	now func() time.Time

	mu   sync.RWMutex
	l1   map[string]l1Entry
	gens map[string]uint64 // per-namespace generation, bumped on Invalidate

	sf singleflight.Group

	// downUntil is the unix-nano time before which L2 is skipped. It keeps a
	// Valkey outage from adding a timeout to every request.
	downUntil atomic.Int64
}

var _ domain.ContentCache = (*Cache)(nil)

// NewCache builds a Cache on top of a connected Client. A nil client yields an
// L1-only cache.
func NewCache(client *Client, cfg Config) *Cache {
	var kv kvStore
	if client != nil {
		kv = client
	}
	return newCache(kv, cfg)
}

func newCache(kv kvStore, cfg Config) *Cache {
	return &Cache{
		kv:   kv,
		cfg:  cfg.withDefaults(),
		now:  time.Now,
		l1:   make(map[string]l1Entry),
		gens: make(map[string]uint64),
	}
}

// Fetch implements domain.ContentCache.
func (c *Cache) Fetch(ctx context.Context, namespace, key string, load func(ctx context.Context) ([]byte, error)) ([]byte, error) {
	lk := namespace + "\x00" + key

	if v, ok := c.l1Get(lk); ok {
		domain.RecordCacheStatus(ctx, domain.CacheHitL1)
		return v, nil
	}

	// The shared flight must not die with the first caller's request, so it
	// runs on a detached context with its own deadline. Each caller still
	// stops waiting when its own context ends.
	ch := c.sf.DoChan(lk, func() (any, error) {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.cfg.LoadTimeout)
		defer cancel()
		return c.fill(fctx, namespace, key, lk, load)
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.Err != nil {
			return nil, r.Err
		}
		res := r.Val.(fetchResult)
		domain.RecordCacheStatus(ctx, res.status)
		return res.value, nil
	}
}

// fill resolves a miss of L1: L2 first, then the loader.
func (c *Cache) fill(ctx context.Context, namespace, key, lk string, load func(context.Context) ([]byte, error)) (fetchResult, error) {
	// A flight that just finished may have filled L1 already.
	if v, ok := c.l1Get(lk); ok {
		return fetchResult{value: v, status: domain.CacheHitL1}, nil
	}

	// Capture the generation before reading anything so a concurrent
	// Invalidate prevents this (possibly stale) result from entering L1.
	gen := c.generation(namespace)

	// The version must be read before the loader runs: if an invalidation
	// lands in between, the value is stored under the old version and is
	// never read again.
	var l2Key string
	if ver, ok := c.version(ctx, namespace); ok {
		l2Key = "cache:" + namespace + ":v" + ver + ":" + key
		if v, found, ok := c.l2Get(ctx, l2Key); ok && found {
			c.l1Set(lk, namespace, v, gen)
			return fetchResult{value: v, status: domain.CacheHitL2}, nil
		}
	}

	v, err := load(ctx)
	if err != nil {
		return fetchResult{}, err
	}

	if l2Key != "" {
		c.l2Set(ctx, l2Key, v)
	}
	c.l1Set(lk, namespace, v, gen)
	return fetchResult{value: v, status: domain.CacheMiss}, nil
}

// Invalidate implements domain.ContentCache.
func (c *Cache) Invalidate(ctx context.Context, namespace string) error {
	// Local first: it cannot fail and takes effect immediately.
	c.mu.Lock()
	c.gens[namespace]++
	prefix := namespace + "\x00"
	for k := range c.l1 {
		if strings.HasPrefix(k, prefix) {
			delete(c.l1, k)
		}
	}
	c.mu.Unlock()

	if c.kv == nil {
		return nil
	}
	// The cooldown is deliberately ignored here: a skipped bump would make
	// stale L2 entries readable again once Valkey comes back.
	octx, cancel := context.WithTimeout(ctx, c.cfg.OpTimeout)
	defer cancel()
	if _, err := c.kv.Incr(octx, versionKey(namespace)); err != nil {
		c.markDown()
		return errors.Join(errors.New("valkey: bump namespace version"), err)
	}
	return nil
}

// Ping implements domain.ContentCache. An L1-only cache is always healthy.
func (c *Cache) Ping(ctx context.Context) error {
	if c.kv == nil {
		return nil
	}
	octx, cancel := context.WithTimeout(ctx, c.cfg.OpTimeout)
	defer cancel()
	return c.kv.Ping(octx)
}

func versionKey(namespace string) string { return "cache:ver:" + namespace }

// version returns the current namespace version. ok is false when L2 must not
// be used (no Valkey, cooling down after a failure, or a failed read).
func (c *Cache) version(ctx context.Context, namespace string) (string, bool) {
	if !c.l2Usable() {
		return "", false
	}
	octx, cancel := context.WithTimeout(ctx, c.cfg.OpTimeout)
	defer cancel()
	v, found, err := c.kv.Get(octx, versionKey(namespace))
	if err != nil {
		c.fail("read namespace version", err)
		return "", false
	}
	if !found {
		return "0", true
	}
	return string(v), true
}

func (c *Cache) l2Get(ctx context.Context, key string) (v []byte, found, ok bool) {
	octx, cancel := context.WithTimeout(ctx, c.cfg.OpTimeout)
	defer cancel()
	v, found, err := c.kv.Get(octx, key)
	if err != nil {
		c.fail("read entry", err)
		return nil, false, false
	}
	return v, found, true
}

func (c *Cache) l2Set(ctx context.Context, key string, v []byte) {
	if !c.l2Usable() {
		return
	}
	octx, cancel := context.WithTimeout(ctx, c.cfg.OpTimeout)
	defer cancel()
	if err := c.kv.Set(octx, key, v, c.l2TTL()); err != nil {
		c.fail("write entry", err)
	}
}

// l2TTL adds up to L2Jitter of random extra lifetime so entries written
// together do not all expire together.
func (c *Cache) l2TTL() time.Duration {
	extra := time.Duration(rand.Float64() * c.cfg.L2Jitter * float64(c.cfg.L2TTL))
	return c.cfg.L2TTL + extra
}

func (c *Cache) l2Usable() bool {
	return c.kv != nil && c.now().UnixNano() >= c.downUntil.Load()
}

func (c *Cache) markDown() {
	c.downUntil.Store(c.now().Add(c.cfg.Cooldown).UnixNano())
}

func (c *Cache) fail(op string, err error) {
	if c.l2Usable() { // log once per outage, not once per request
		slog.Warn("valkey cache degraded, serving from the database", "op", op, "error", err, "cooldown", c.cfg.Cooldown.String())
	}
	c.markDown()
}

func (c *Cache) generation(namespace string) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gens[namespace]
}

func (c *Cache) l1Get(lk string) ([]byte, bool) {
	c.mu.RLock()
	e, ok := c.l1[lk]
	c.mu.RUnlock()
	if !ok || !c.now().Before(e.expires) {
		return nil, false
	}
	return e.value, true
}

// l1Set stores v unless the namespace was invalidated since gen was read.
func (c *Cache) l1Set(lk, namespace string, v []byte, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gens[namespace] != gen {
		return
	}
	if _, exists := c.l1[lk]; !exists && len(c.l1) >= c.cfg.L1MaxEntries {
		c.evictLocked()
	}
	c.l1[lk] = l1Entry{value: v, expires: c.now().Add(c.cfg.L1TTL)}
}

// evictLocked frees room: expired entries first, then one arbitrary entry.
func (c *Cache) evictLocked() {
	now := c.now()
	for k, e := range c.l1 {
		if !now.Before(e.expires) {
			delete(c.l1, k)
		}
	}
	if len(c.l1) < c.cfg.L1MaxEntries {
		return
	}
	for k := range c.l1 {
		delete(c.l1, k)
		return
	}
}
