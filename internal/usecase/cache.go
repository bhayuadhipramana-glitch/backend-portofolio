package usecase

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// invalidateTimeout bounds the cache bump that follows a committed write.
const invalidateTimeout = 2 * time.Second

// maxCacheKeyLen keeps attacker-chosen slugs from becoming huge cache keys;
// longer keys bypass the cache.
const maxCacheKeyLen = 200

// cached runs load through the cache under (namespace, key) and decodes the
// result into T. Whatever goes wrong with the cache itself, the caller still
// gets a value straight from load.
func cached[T any](ctx context.Context, c domain.ContentCache, namespace, key string, load func(context.Context) (T, error)) (T, error) {
	if len(key) > maxCacheKeyLen {
		return load(ctx)
	}

	var loaded bool
	var direct T
	raw, err := c.Fetch(ctx, namespace, key, func(ctx context.Context) ([]byte, error) {
		v, err := load(ctx)
		if err != nil {
			return nil, err
		}
		loaded, direct = true, v
		return json.Marshal(v)
	})
	if err != nil {
		var zero T
		return zero, err
	}
	if loaded {
		return direct, nil // this call ran the loader; skip the decode
	}

	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		slog.WarnContext(ctx, "usecase: undecodable cache entry, reloading", "namespace", namespace, "key", key, "error", err)
		return load(ctx)
	}
	return out, nil
}

// invalidate drops a namespace after a successful write. The write is already
// committed, so a failure is logged rather than returned: the entries expire
// on their own within the L2 TTL.
func invalidate(ctx context.Context, c domain.ContentCache, namespace string) {
	ictx, cancel := context.WithTimeout(context.WithoutCancel(ctx), invalidateTimeout)
	defer cancel()
	if err := c.Invalidate(ictx, namespace); err != nil {
		slog.ErrorContext(ctx, "usecase: cache invalidation failed; stale entries may be served until they expire", "namespace", namespace, "error", err)
	}
}

func listKey(page domain.Page) string {
	return "list:" + strconv.Itoa(page.Limit) + ":" + strconv.Itoa(page.Offset)
}
