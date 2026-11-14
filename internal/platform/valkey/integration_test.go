package valkey

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// newTestClient connects to a real Valkey (VALKEY_ADDR, default
// 127.0.0.1:6379; start it with `docker compose up -d valkey`) and skips the
// test when none is reachable.
func newTestClient(t *testing.T) *Client {
	t.Helper()
	addr := os.Getenv("VALKEY_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	client, err := Connect(addr)
	if err != nil {
		t.Skipf("Valkey not reachable at %s: %v", addr, err)
	}
	t.Cleanup(client.Close)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		t.Skipf("Valkey ping failed at %s: %v", addr, err)
	}
	return client
}

// uniqueNamespace keeps parallel runs and old data from interfering.
func uniqueNamespace(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return "test-" + hex.EncodeToString(b)
}

func TestValkeyRoundTripAcrossProcesses(t *testing.T) {
	client := newTestClient(t)
	ns := uniqueNamespace(t)
	var calls atomic.Int64

	first := NewCache(client, Config{})
	if v, s := fetch(t, first, ns, "list", loader(&calls)); v != "v1" || s != domain.CacheMiss {
		t.Fatalf("first read = %q %s, want v1 MISS", v, s)
	}
	if _, s := fetch(t, first, ns, "list", loader(&calls)); s != domain.CacheHitL1 {
		t.Fatalf("second read: status %s, want HIT-L1", s)
	}

	second := NewCache(client, Config{}) // empty L1, same Valkey
	if v, s := fetch(t, second, ns, "list", loader(&calls)); v != "v1" || s != domain.CacheHitL2 {
		t.Fatalf("cold-L1 read = %q %s, want v1 HIT-L2", v, s)
	}

	if err := first.Invalidate(context.Background(), ns); err != nil {
		t.Fatal(err)
	}
	// The other process still has the value in L1 for at most L1TTL ...
	if _, s := fetch(t, second, ns, "list", loader(&calls)); s != domain.CacheHitL1 {
		t.Fatalf("other process L1: status %s, want HIT-L1 within its TTL", s)
	}
	// ... but anything that reaches L2 sees the bumped version.
	third := NewCache(client, Config{})
	if v, s := fetch(t, third, ns, "list", loader(&calls)); v != "v2" || s != domain.CacheMiss {
		t.Fatalf("read after invalidate = %q %s, want v2 MISS", v, s)
	}
}

func TestValkeyUnreachableDegradesToLoader(t *testing.T) {
	// Nothing listens on this port; Connect itself fails, which is why main
	// treats Valkey as required at boot. A cache built without a client
	// models the runtime-outage path.
	if _, err := Connect("127.0.0.1:1"); err == nil {
		t.Skip("unexpected listener on 127.0.0.1:1")
	}

	c := NewCache(nil, Config{})
	var calls atomic.Int64
	if v, s := fetch(t, c, "projects", "k", loader(&calls)); v != "v1" || s != domain.CacheMiss {
		t.Fatalf("degraded read = %q %s, want v1 MISS", v, s)
	}
}
