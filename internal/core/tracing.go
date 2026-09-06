package core

import (
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// ConfigureTracing is called once at startup, before serving requests. No remote data is fetched at startup.
func (s *Service) ConfigureTracing(ctx context.Context, endpoint string, ratio float64) (func(context.Context) error, error) {
	if math.IsNaN(ratio) || ratio < 0 || ratio > 1 {
		return nil, errors.New("invalid trace sampling ratio")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid trace endpoint")
	}
	loopback := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && (u.Scheme != "http" || loopback == nil || !loopback.IsLoopback()) {
		return nil, errors.New("trace endpoint requires HTTPS or a loopback IP")
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: boundedResponseTransport{&http.Transport{Proxy: nil}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint), otlptracehttp.WithCompression(otlptracehttp.NoCompression), otlptracehttp.WithHeaders(map[string]string{}), otlptracehttp.WithHTTPClient(client), otlptracehttp.WithTimeout(3*time.Second), otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}))
	if err != nil {
		return nil, errors.New("trace exporter initialization failed")
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "providah"))), sdktrace.WithSampler(sdktrace.TraceIDRatioBased(ratio)), sdktrace.WithBatcher(redactedExporter{exporter}, sdktrace.WithMaxQueueSize(1024), sdktrace.WithMaxExportBatchSize(128), sdktrace.WithBatchTimeout(5*time.Second), sdktrace.WithExportTimeout(3*time.Second)))
	otel.SetTracerProvider(provider)
	s.telemetry.tracer = provider.Tracer("providah/core")
	return provider.Shutdown, nil
}

// Collector errors can contain response bodies and URLs; never forward them to the SDK's diagnostic logger.
type redactedExporter struct{ sdktrace.SpanExporter }

func (e redactedExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if e.SpanExporter.ExportSpans(ctx, spans) != nil {
		return errors.New("trace export failed")
	}
	return nil
}
