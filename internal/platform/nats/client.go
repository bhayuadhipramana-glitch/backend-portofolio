// Package nats implements the event bus ports on NATS JetStream.
package nats

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

const (
	connectTimeout = 3 * time.Second
	pingTimeout    = 2 * time.Second
	// streamSetupTimeout bounds the stream create/update call at startup.
	streamSetupTimeout = 5 * time.Second

	// Stream limits are far below the 256 MB JetStream file store so the
	// events stream can never starve other streams.
	streamMaxMsgs  = 100_000
	streamMaxBytes = 64 << 20
	streamMaxMsg   = 64 << 10 // one event, payload included
	// streamDedupWindow is how long a repeated Nats-Msg-Id is dropped.
	streamDedupWindow = 5 * time.Minute
)

// Client owns the NATS connection and the JetStream handle.
type Client struct {
	nc *nats.Conn
	js jetstream.JetStream
}

// Connect dials NATS and makes sure the events stream exists. It fails fast so
// the process never starts without a broker.
func Connect(ctx context.Context, url string) (*Client, error) {
	nc, err := nats.Connect(url,
		nats.Name("portfolio-api"),
		nats.Timeout(connectTimeout),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("init jetstream: %w", err)
	}

	c := &Client{nc: nc, js: js}
	if err := c.ensureStream(ctx); err != nil {
		nc.Close()
		return nil, err
	}
	return c, nil
}

// ensureStream creates the bounded events stream or updates it to this
// configuration. WorkQueue retention removes a message once the single
// consumer acks it; DiscardNew turns a full stream into publish errors
// (HTTP 503) instead of silently dropping unprocessed events.
func (c *Client) ensureStream(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, streamSetupTimeout)
	defer cancel()

	_, err := c.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       domain.EventStreamName,
		Subjects:   []string{domain.EventSubject},
		Retention:  jetstream.WorkQueuePolicy,
		Discard:    jetstream.DiscardNew,
		Storage:    jetstream.FileStorage,
		MaxMsgs:    streamMaxMsgs,
		MaxBytes:   streamMaxBytes,
		MaxMsgSize: streamMaxMsg,
		Duplicates: streamDedupWindow,
	})
	if err != nil {
		return fmt.Errorf("ensure stream %s: %w", domain.EventStreamName, err)
	}
	return nil
}

// Ping reports whether the connection to NATS is up.
func (c *Client) Ping(ctx context.Context) error {
	if c.nc.Status() != nats.CONNECTED {
		return errors.New("nats: not connected")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, pingTimeout)
		defer cancel()
	}
	// A flush round trip proves the server is responsive, not just the socket.
	if err := c.nc.FlushWithContext(ctx); err != nil {
		return fmt.Errorf("nats flush: %w", err)
	}
	return nil
}

// Drain stops accepting new work, flushes pending messages and closes the
// connection. Call it after the worker has finished its in-flight batch.
func (c *Client) Drain() error {
	return c.nc.Drain()
}

// Close closes the connection immediately.
func (c *Client) Close() {
	c.nc.Close()
}

// JetStream exposes the handle for consumers built in this package.
func (c *Client) JetStream() jetstream.JetStream {
	return c.js
}
