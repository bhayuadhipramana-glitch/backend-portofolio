package middleware

import "testing"

func TestConnLimiterGlobalAndPerIP(t *testing.T) {
	l := NewConnLimiter(3, 2)

	r1, why := l.Acquire("a")
	if why != ReasonNone {
		t.Fatal("first connection refused")
	}
	if _, why := l.Acquire("a"); why != ReasonNone {
		t.Fatal("second connection from the same IP refused")
	}
	if _, why := l.Acquire("a"); why != ReasonIPFull {
		t.Fatalf("third from one IP: got %v, want ReasonIPFull", why)
	}
	if _, why := l.Acquire("b"); why != ReasonNone {
		t.Fatal("another IP must still fit")
	}
	if _, why := l.Acquire("c"); why != ReasonGlobalFull {
		t.Fatalf("over the global cap: got %v, want ReasonGlobalFull", why)
	}
	if l.Active() != 3 {
		t.Fatalf("active = %d, want 3", l.Active())
	}

	r1()
	r1() // releasing twice must not free a second slot
	if l.Active() != 2 {
		t.Fatalf("after release active = %d, want 2", l.Active())
	}
	if _, why := l.Acquire("c"); why != ReasonNone {
		t.Fatal("a released slot must be reusable")
	}
}

func TestConnLimiterForgetsIdleIPs(t *testing.T) {
	l := NewConnLimiter(10, 1)
	rel, _ := l.Acquire("x")
	rel()
	if len(l.perIP) != 0 {
		t.Fatalf("per-IP table kept %d idle entries", len(l.perIP))
	}
}

func TestIPKey(t *testing.T) {
	if IPKey("203.0.113.7:4000") != "203.0.113.7" || IPKey("203.0.113.7") != "203.0.113.7" || IPKey("[::1]:80") != "::1" {
		t.Fatal("IPKey mishandled an address form")
	}
}
