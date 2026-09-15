// Package telemetry configures traces and metrics for PrivateMesh services.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	prometheusexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/ayansaiyad/privatemesh"

// Telemetry owns process-level trace and metric providers.
type Telemetry struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *sdkmetric.MeterProvider
	tracer         trace.Tracer
	httpRequests   metric.Int64Counter
	httpDuration   metric.Float64Histogram
	rpcRequests    metric.Int64Counter
	rpcDuration    metric.Float64Histogram
	searches       metric.Int64Counter
	results        metric.Int64Histogram
	unavailable    metric.Int64Counter
}

// New configures OTLP traces when an endpoint is present and Prometheus metrics always.
func New(ctx context.Context, serviceName string) (*Telemetry, error) {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return nil, errors.New("telemetry service name is required")
	}
	serviceResource, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", serviceName),
	))
	if err != nil {
		return nil, fmt.Errorf("create telemetry resource: %w", err)
	}
	traceOptions := []sdktrace.TracerProviderOption{sdktrace.WithResource(serviceResource)}
	if strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" {
		exporter, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}
		traceOptions = append(traceOptions, sdktrace.WithBatcher(exporter))
	} else {
		traceOptions = append(traceOptions, sdktrace.WithSampler(sdktrace.NeverSample()))
	}
	tracerProvider := sdktrace.NewTracerProvider(traceOptions...)
	prometheusReader, err := prometheusexporter.New()
	if err != nil {
		return nil, fmt.Errorf("create Prometheus metric reader: %w", err)
	}
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(serviceResource), sdkmetric.WithReader(prometheusReader),
	)
	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	meter := meterProvider.Meter(instrumentationName)
	telemetry := &Telemetry{
		tracerProvider: tracerProvider,
		meterProvider:  meterProvider,
		tracer:         tracerProvider.Tracer(instrumentationName),
	}
	if telemetry.httpRequests, err = meter.Int64Counter("privatemesh.http.requests"); err != nil {
		return nil, err
	}
	if telemetry.httpDuration, err = meter.Float64Histogram("privatemesh.http.duration", metric.WithUnit("s")); err != nil {
		return nil, err
	}
	if telemetry.rpcRequests, err = meter.Int64Counter("privatemesh.rpc.requests"); err != nil {
		return nil, err
	}
	if telemetry.rpcDuration, err = meter.Float64Histogram("privatemesh.rpc.duration", metric.WithUnit("s")); err != nil {
		return nil, err
	}
	if telemetry.searches, err = meter.Int64Counter("privatemesh.searches"); err != nil {
		return nil, err
	}
	if telemetry.results, err = meter.Int64Histogram("privatemesh.search.results"); err != nil {
		return nil, err
	}
	if telemetry.unavailable, err = meter.Int64Counter("privatemesh.search.unavailable_shards"); err != nil {
		return nil, err
	}
	return telemetry, nil
}

// MetricsHandler returns the Prometheus scrape handler.
func (t *Telemetry) MetricsHandler() http.Handler {
	return promhttp.Handler()
}

// RegisterRoutes adds the metrics endpoint to a mux.
func (t *Telemetry) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /metrics", t.MetricsHandler())
}

// RecordSearch records a completed distributed search without query or document attributes.
func (t *Telemetry) RecordSearch(ctx context.Context, strategy string, resultCount, unavailableShards int) {
	attributes := metric.WithAttributes(attribute.String("strategy", strategy))
	t.searches.Add(ctx, 1, attributes)
	t.results.Record(ctx, int64(resultCount), attributes)
	if unavailableShards > 0 {
		t.unavailable.Add(ctx, int64(unavailableShards), attributes)
	}
}

// Shutdown flushes telemetry providers.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	return errors.Join(t.meterProvider.Shutdown(ctx), t.tracerProvider.Shutdown(ctx))
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// HTTPMiddleware traces and measures HTTP requests without recording paths or payloads.
func (t *Telemetry) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(request.Context(), propagation.HeaderCarrier(request.Header))
		ctx, span := t.tracer.Start(ctx, "HTTP "+request.Method, trace.WithSpanKind(trace.SpanKindServer))
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(recorder, request.WithContext(ctx))
		attributes := metric.WithAttributes(
			attribute.String("http.request.method", request.Method),
			attribute.Int("http.response.status_code", recorder.status),
		)
		t.httpRequests.Add(ctx, 1, attributes)
		t.httpDuration.Record(ctx, time.Since(started).Seconds(), attributes)
		span.SetAttributes(
			attribute.String("http.request.method", request.Method),
			attribute.Int("http.response.status_code", recorder.status),
		)
		span.End()
	})
}
