// Package observability implements F6: OpenTelemetry metrics exposed in the
// Prometheus text format at GET /metrics, plus optional OTLP trace export.
//
// It is an adapter. The domain, usecase and handler layers know nothing about
// OpenTelemetry: cache, event bus and outbox are observed by decorators that
// implement the same domain ports, and HTTP by a standard middleware.
package observability

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	serviceName = "portfolio-api"
	scopeName   = "github.com/bhayuadhipramana-glicth/backend-portofolio/internal/observability"

	// traceShutdownTimeout bounds the final flush of buffered spans.
	traceShutdownTimeout = 5 * time.Second
	// pendingQueryTimeout bounds the outbox backlog query run at scrape time.
	pendingQueryTimeout = 2 * time.Second
)

// httpBuckets are latency boundaries in seconds, dense around the 50 ms p99 goal.
var httpBuckets = []float64{0.001, 0.0025, 0.005, 0.01, 0.02, 0.035, 0.05, 0.075, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Config selects the optional parts of the provider.
type Config struct {
	// OTLPEndpoint is an OTLP/HTTP collector URL (e.g. http://otel-collector:4318).
	// Empty means no trace export: spans are never created, at no cost.
	OTLPEndpoint string
}

// Provider owns the meter provider, the Prometheus registry and every
// instrument. Create it once with New and call Shutdown on exit.
type Provider struct {
	registry *prometheus.Registry
	meters   *sdkmetric.MeterProvider
	traces   *sdktrace.TracerProvider // nil when tracing is off
	tracer   trace.Tracer
	meter    metric.Meter

	httpDuration metric.Float64Histogram
	cacheReqs    metric.Int64Counter
	natsPub      metric.Int64Counter
	natsConsumed metric.Int64Counter
	outboxOut    metric.Int64Counter
}

// New builds the provider. Metrics always work; tracing is enabled only when
// cfg.OTLPEndpoint is set, otherwise the tracer is a no-op.
func New(ctx context.Context, cfg Config) (*Provider, error) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	exporter, err := otelprom.New(
		otelprom.WithRegisterer(reg),
		otelprom.WithoutScopeInfo(),
		otelprom.WithoutTargetInfo(),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: prometheus exporter: %w", err)
	}
	meters := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))

	p := &Provider{
		registry: reg,
		meters:   meters,
		meter:    meters.Meter(scopeName),
		tracer:   noop.NewTracerProvider().Tracer(scopeName),
	}

	if cfg.OTLPEndpoint != "" {
		if err := p.enableTracing(ctx, cfg.OTLPEndpoint); err != nil {
			_ = meters.Shutdown(ctx)
			return nil, err
		}
	}
	if err := p.initInstruments(); err != nil {
		_ = p.Shutdown(ctx)
		return nil, err
	}
	return p, nil
}

func (p *Provider) enableTracing(ctx context.Context, endpoint string) error {
	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
	if err != nil {
		return fmt.Errorf("observability: otlp trace exporter: %w", err)
	}
	res := resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(serviceName))
	p.traces = sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	p.tracer = p.traces.Tracer(scopeName)
	otel.SetTracerProvider(p.traces)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return nil
}

func (p *Provider) initInstruments() error {
	var err error
	if p.httpDuration, err = p.meter.Float64Histogram("portfolio.http.server.duration",
		metric.WithUnit("s"),
		metric.WithDescription("HTTP request duration by route, method and status."),
		metric.WithExplicitBucketBoundaries(httpBuckets...)); err != nil {
		return err
	}
	if p.cacheReqs, err = p.meter.Int64Counter("portfolio.cache.requests",
		metric.WithDescription("Content cache reads by namespace and result (HIT-L1, HIT-L2, MISS).")); err != nil {
		return err
	}
	if p.natsPub, err = p.meter.Int64Counter("portfolio.nats.published.events",
		metric.WithDescription("Events published to JetStream by outcome.")); err != nil {
		return err
	}
	if p.natsConsumed, err = p.meter.Int64Counter("portfolio.nats.consumed.events",
		metric.WithDescription("Events pulled from JetStream by the aggregation worker.")); err != nil {
		return err
	}
	p.outboxOut, err = p.meter.Int64Counter("portfolio.outbox.deliveries",
		metric.WithDescription("Outbox delivery attempts by outcome (sent, failed, dead)."))
	return err
}

// Handler serves the Prometheus text exposition. Guard it with MetricsAccess.
func (p *Provider) Handler() http.Handler {
	return promhttp.HandlerFor(p.registry, promhttp.HandlerOpts{})
}

// ObserveActiveSSE reports the live-feed connection count at scrape time.
func (p *Provider) ObserveActiveSSE(active func() int) error {
	_, err := p.meter.Int64ObservableGauge("portfolio.sse.connections.active",
		metric.WithDescription("Open Server-Sent Events live-feed streams."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(int64(active()))
			return nil
		}))
	return err
}

// ObserveOutboxPending reports the undelivered outbox backlog at scrape time.
// A failing query skips the sample instead of failing the scrape.
func (p *Provider) ObserveOutboxPending(pending func(ctx context.Context) (int64, error)) error {
	_, err := p.meter.Int64ObservableGauge("portfolio.outbox.pending",
		metric.WithDescription("Outbox messages waiting for delivery (pending or failed)."),
		metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
			ctx, cancel := context.WithTimeout(ctx, pendingQueryTimeout)
			defer cancel()
			if n, err := pending(ctx); err == nil {
				o.Observe(n)
			}
			return nil
		}))
	return err
}

// Shutdown flushes pending spans and stops the meter provider.
func (p *Provider) Shutdown(ctx context.Context) error {
	var errs []error
	if p.traces != nil {
		tctx, cancel := context.WithTimeout(ctx, traceShutdownTimeout)
		errs = append(errs, p.traces.Shutdown(tctx))
		cancel()
	}
	errs = append(errs, p.meters.Shutdown(ctx))
	return errors.Join(errs...)
}
