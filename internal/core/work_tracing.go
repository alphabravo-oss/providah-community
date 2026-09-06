package core

import (
	"context"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func traceParent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier.Get("traceparent")
}
func workContext(ctx context.Context, parent string) context.Context {
	return propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": parent})
}
func (s *Service) callProvider(ctx context.Context, request provider.Request) (provider.Response, error) {
	ctx, span := s.telemetry.tracer.Start(ctx, "provider.execute", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attribute.String("provider", request.Provider)))
	defer span.End()
	result, err := s.cfg.ProviderCall(ctx, request)
	if err != nil || result.Error != "" {
		span.SetStatus(codes.Error, "provider call failed")
	}
	return result, err
}
