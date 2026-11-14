// Package valkey implements domain.ContentCache as a two-layer cache: a tiny
// in-process L1 in front of a shared Valkey L2.
package valkey

import (
	"context"
	"errors"
	"fmt"
	"time"

	vk "github.com/valkey-io/valkey-go"
)

// kvStore is the small slice of Valkey the cache needs. It exists so the cache
// logic can be tested without a server.
type kvStore interface {
	Get(ctx context.Context, key string) (value []byte, found bool, err error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Incr(ctx context.Context, key string) (int64, error)
	Ping(ctx context.Context) error
}

// Client is a kvStore backed by valkey-go.
type Client struct {
	c vk.Client
}

// Connect opens a connection to the Valkey server at addr (host:port).
// Client-side caching is disabled: L1 is managed by Cache itself.
func Connect(addr string) (*Client, error) {
	c, err := vk.NewClient(vk.ClientOption{
		InitAddress:  []string{addr},
		DisableCache: true,
	})
	if err != nil {
		return nil, fmt.Errorf("valkey: connect %s: %w", addr, err)
	}
	return &Client{c: c}, nil
}

// Close releases the connection.
func (s *Client) Close() { s.c.Close() }

// Get returns the value stored at key; found is false when it does not exist.
func (s *Client) Get(ctx context.Context, key string) ([]byte, bool, error) {
	b, err := s.c.Do(ctx, s.c.B().Get().Key(key).Build()).AsBytes()
	if err != nil {
		if vk.IsValkeyNil(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return b, true, nil
}

// Set stores value at key with a time-to-live.
func (s *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	ms := ttl.Milliseconds()
	if ms < 1 {
		return errors.New("valkey: ttl must be at least 1ms")
	}
	return s.c.Do(ctx, s.c.B().Set().Key(key).Value(string(value)).PxMilliseconds(ms).Build()).Error()
}

// Incr atomically increments the integer at key and returns the new value.
func (s *Client) Incr(ctx context.Context, key string) (int64, error) {
	return s.c.Do(ctx, s.c.B().Incr().Key(key).Build()).AsInt64()
}

// Ping checks that the server answers.
func (s *Client) Ping(ctx context.Context) error {
	return s.c.Do(ctx, s.c.B().Ping().Build()).Error()
}
