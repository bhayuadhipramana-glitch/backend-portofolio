package observability

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// unmatchedRoute labels requests no route handled, so scanners probing random
// paths cannot create unbounded label cardinality.
const unmatchedRoute = "unmatched"

// HTTP records route, method, status and duration of every request. Mount it
// on the root router; it reads the route pattern after chi has matched it.
//
// Server-Sent Events responses are skipped: a stream lives for minutes and
// would distort the latency histogram (they are tracked by the active-streams
// gauge instead). The writer is wrapped with chi's wrapper, which keeps
// http.Flusher and Unwrap working for the SSE handler.
func (p *Provider) HTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		var span trace.Span
		if p.traces != nil {
			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span = p.tracer.Start(ctx, r.Method, trace.WithSpanKind(trace.SpanKindServer))
			r = r.WithContext(ctx)
		}

		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		route := unmatchedRoute
		if rc := chi.RouteContext(r.Context()); rc != nil {
			if pat := rc.RoutePattern(); pat != "" {
				route = pat
			}
		}
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}

		if span != nil {
			span.SetName(r.Method + " " + route)
			span.SetAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("http.route", route),
				attribute.Int("http.response.status_code", status),
			)
			if status >= 500 {
				span.SetStatus(codes.Error, http.StatusText(status))
			}
			span.End()
		}

		if strings.HasPrefix(ww.Header().Get("Content-Type"), "text/event-stream") {
			return
		}
		p.httpDuration.Record(r.Context(), time.Since(start).Seconds(), metric.WithAttributes(
			attribute.String("http_route", route),
			attribute.String("http_method", r.Method),
			attribute.String("http_status", strconv.Itoa(status)),
		))
	})
}
