package middleware

import (
	"net/http"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// CacheHeaderName is the response header that tells where a read was served from.
const CacheHeaderName = "X-Cache"

// CacheStatus lets the usecase layer report cache hits for the request and
// exposes the outcome as X-Cache (HIT-L1, HIT-L2 or MISS). Apply it only to
// public content reads.
func CacheStatus(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, trace := domain.WithCacheTrace(r.Context())
		next.ServeHTTP(&cacheHeaderWriter{ResponseWriter: w, trace: trace}, r.WithContext(ctx))
	})
}

// cacheHeaderWriter adds X-Cache just before the response headers are sent.
type cacheHeaderWriter struct {
	http.ResponseWriter
	trace       *domain.CacheTrace
	wroteHeader bool
}

func (w *cacheHeaderWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		// Only successful reads came from the cache; errors never are.
		if code == http.StatusOK {
			if s := w.trace.Status(); s != "" {
				w.Header().Set(CacheHeaderName, string(s))
			}
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *cacheHeaderWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *cacheHeaderWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
