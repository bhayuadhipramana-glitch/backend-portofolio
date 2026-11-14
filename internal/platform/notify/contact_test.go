package notify

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

var testMsg = domain.OutboxMessage{ID: "out-1", AggregateID: "agg-1", EventType: domain.OutboxEventContactCreated, Payload: []byte(`{"name":"Ada","email":"ada@example.com"}`)}

func TestNotifyPostsPayloadToWebhook(t *testing.T) {
	var gotBody, gotID, gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotID, gotType = string(b), r.Header.Get("X-Outbox-Id"), r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	n, err := New(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Notify(context.Background(), testMsg); err != nil {
		t.Fatal(err)
	}
	if gotBody != string(testMsg.Payload) || gotID != "out-1" || gotType != "application/json" {
		t.Fatalf("webhook got body=%q id=%q type=%q", gotBody, gotID, gotType)
	}
}

func TestNotifyTreatsNon2xxAsFailure(t *testing.T) {
	for _, code := range []int{http.StatusBadRequest, http.StatusInternalServerError, http.StatusBadGateway} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
		n, _ := New(srv.URL, nil)
		if err := n.Notify(context.Background(), testMsg); err == nil {
			t.Errorf("status %d was treated as delivered", code)
		}
		srv.Close()
	}
}

func TestNotifyFailsWhenWebhookUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	n, _ := New(url, nil)
	if err := n.Notify(context.Background(), testMsg); err == nil {
		t.Fatal("an unreachable webhook was treated as delivered")
	}
}

func TestNotifyFallsBackToLogOnlyWithoutWebhook(t *testing.T) {
	var buf bytes.Buffer
	n, err := New("", slog.New(slog.NewTextHandler(&buf, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Notify(context.Background(), testMsg); err != nil {
		t.Fatalf("log-only mode must succeed, got %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "out-1") {
		t.Errorf("log line missing the outbox id: %s", out)
	}
	if strings.Contains(out, "ada@example.com") || strings.Contains(out, "Ada") {
		t.Errorf("log line leaks visitor data: %s", out)
	}
}

func TestNewRejectsInvalidWebhookURL(t *testing.T) {
	for _, u := range []string{"not a url", "ftp://example.com", "http://", "/relative"} {
		if _, err := New(u, nil); err == nil {
			t.Errorf("New(%q) accepted an invalid webhook URL", u)
		}
	}
}
