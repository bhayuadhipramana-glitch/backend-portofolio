// Package notify implements domain.ContactNotifier.
package notify

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// requestTimeout bounds one webhook call.
const requestTimeout = 10 * time.Second

// Notifier posts the outbox payload to a webhook, or only logs it when no
// webhook URL is configured.
type Notifier struct {
	webhookURL string
	client     *http.Client
	logger     *slog.Logger
}

var _ domain.ContactNotifier = (*Notifier)(nil)

// New returns a Notifier. An empty webhookURL selects the log-only fallback;
// a non-empty one must be an absolute http(s) URL.
func New(webhookURL string, logger *slog.Logger) (*Notifier, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if webhookURL != "" {
		u, err := url.Parse(webhookURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("notify: webhook URL must be an absolute http(s) URL")
		}
	}
	return &Notifier{
		webhookURL: webhookURL,
		client:     &http.Client{Timeout: requestTimeout},
		logger:     logger,
	}, nil
}

// Notify delivers msg. Any non-2xx answer is an error so the outbox retries.
func (n *Notifier) Notify(ctx context.Context, msg domain.OutboxMessage) error {
	if n.webhookURL == "" {
		// The payload holds visitor data, so log only identifiers.
		n.logger.Info("contact notification (no webhook configured)",
			"outbox_id", msg.ID, "aggregate_id", msg.AggregateID, "event_type", msg.EventType)
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhookURL, bytes.NewReader(msg.Payload))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Outbox-Id", msg.ID) // lets the receiver deduplicate redeliveries

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("post webhook: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("webhook answered %d", resp.StatusCode)
	}
	return nil
}
