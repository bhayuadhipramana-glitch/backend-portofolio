package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	authmw "github.com/bhayuadhipramana-glicth/backend-portofolio/internal/middleware"
)

// countingStats returns a snapshot whose RPS equals the call number, so a test
// can see that every frame is a fresh one.
type countingStats struct {
	n   atomic.Int64
	err error
}

func (c *countingStats) Snapshot(context.Context) (*domain.LiveSnapshot, error) {
	if c.err != nil {
		return nil, c.err
	}
	n := c.n.Add(1)
	return &domain.LiveSnapshot{
		GeneratedAt: time.Now().UTC(),
		Rolling:     domain.RollingStats{RPS: float64(n)},
	}, nil
}

func fastStream(uc *countingStats, l *authmw.ConnLimiter) *StreamHandler {
	h := NewStreamHandler(uc, l)
	h.interval = 15 * time.Millisecond
	h.keepalive = 40 * time.Millisecond
	return h
}

// readLines reads SSE lines until stop returns true or the deadline passes.
func readLines(t *testing.T, resp *http.Response, stop func(lines []string) bool) []string {
	t.Helper()
	var lines []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines = append(lines, sc.Text())
			if stop(lines) {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out; read so far: %q", lines)
	}
	return lines
}

func count(lines []string, prefix string) int {
	n := 0
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func TestStreamFramingHeadersAndPeriodicUpdates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(fastStream(&countingStats{}, authmw.NewConnLimiter(5, 5)).Stream))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	for k, want := range map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"Connection":        "keep-alive",
		"X-Accel-Buffering": "no",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}

	lines := readLines(t, resp, func(l []string) bool { return count(l, "event: snapshot") >= 4 })

	if lines[0] != "retry: 3000" {
		t.Fatalf("first line = %q, want the retry hint", lines[0])
	}
	var rps []float64
	for i, l := range lines {
		if l != "event: snapshot" || i+2 >= len(lines) {
			continue // the last frame may still be in flight
		}
		if i+2 >= len(lines) || !strings.HasPrefix(lines[i+1], "data: ") || lines[i+2] != "" {
			t.Fatalf("event is not framed as event/data/blank: %q", lines[i:])
		}
		var snap domain.LiveSnapshot
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[i+1], "data: ")), &snap); err != nil {
			t.Fatalf("data is not valid JSON: %v", err)
		}
		rps = append(rps, snap.Rolling.RPS)
	}
	if len(rps) < 3 || rps[0] >= rps[1] || rps[1] >= rps[2] {
		t.Fatalf("frames are not periodic fresh snapshots: %v", rps)
	}
}

func TestStreamSendsKeepalivePings(t *testing.T) {
	h := fastStream(&countingStats{}, authmw.NewConnLimiter(5, 5))
	h.interval = time.Hour // only the keepalive should fire after the first frame
	srv := httptest.NewServer(http.HandlerFunc(h.Stream))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	lines := readLines(t, resp, func(l []string) bool { return count(l, ": ping") >= 2 })
	if count(lines, "event: snapshot") != 1 {
		t.Fatalf("expected exactly one snapshot before the pings, got %q", lines)
	}
}

// The server's WriteTimeout must not end the stream: the handler clears the
// connection write deadline.
func TestStreamSurvivesServerWriteTimeout(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(fastStream(&countingStats{}, authmw.NewConnLimiter(5, 5)).Stream))
	srv.Config.WriteTimeout = 150 * time.Millisecond
	srv.Start()
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	start := time.Now()
	lines := readLines(t, resp, func([]string) bool { return time.Since(start) > 500*time.Millisecond })
	if count(lines, "event: snapshot") < 20 { // 15 ms interval over 500 ms is about 33 frames
		t.Fatalf("stream stalled after the write timeout: only %d frames", count(lines, "event: snapshot"))
	}
}

func TestStreamConnectionLimits(t *testing.T) {
	limiter := authmw.NewConnLimiter(2, 1)
	srv := httptest.NewServer(http.HandlerFunc(fastStream(&countingStats{}, limiter).Stream))
	defer srv.Close()

	open := func() *http.Response {
		resp, err := http.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	first := open()
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first stream: %d", first.StatusCode)
	}

	// Same IP (loopback) again: per-IP limit of 1.
	if second := open(); second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second stream from one IP: got %d, want 429", second.StatusCode)
	} else {
		second.Body.Close()
	}

	// Closing the first stream frees its slot.
	first.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for limiter.Active() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if limiter.Active() != 0 {
		t.Fatalf("slot was not released after disconnect: %d active", limiter.Active())
	}
}

func TestStreamGlobalCapReturns503(t *testing.T) {
	limiter := authmw.NewConnLimiter(2, 10)
	h := fastStream(&countingStats{}, limiter)

	// Hold both global slots as if two other clients were connected.
	r1, _ := limiter.Acquire("198.51.100.1")
	r2, _ := limiter.Acquire("198.51.100.2")
	defer r1()
	defer r2()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream", nil)
	req.RemoteAddr = "203.0.113.9:5555"
	h.Stream(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" || strings.Contains(rec.Header().Get("Content-Type"), "event-stream") {
		t.Fatal("a refused stream must be a plain JSON error with Retry-After")
	}
}

func TestStreamHidesSnapshotErrorsAndKeepsRunning(t *testing.T) {
	uc := &countingStats{err: errors.New("pq: secret internal detail")}
	srv := httptest.NewServer(http.HandlerFunc(fastStream(uc, authmw.NewConnLimiter(5, 5)).Stream))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	lines := readLines(t, resp, func(l []string) bool { return count(l, ": ping") >= 1 })
	for _, l := range lines {
		if strings.Contains(l, "secret") {
			t.Fatal("internal error text leaked into the stream")
		}
	}
	if count(lines, "event: snapshot") != 0 {
		t.Fatal("no snapshot should be sent while the query fails")
	}
}

func TestStreamEndsAfterMaxLifetime(t *testing.T) {
	h := fastStream(&countingStats{}, authmw.NewConnLimiter(5, 5))
	h.lifetime = 100 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(h.Stream))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// The body must reach EOF on its own; readLines only returns on EOF here.
	readLines(t, resp, func([]string) bool { return false })
}
