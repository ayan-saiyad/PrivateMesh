package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor traces and measures unary RPCs.
func (t *Telemetry) UnaryServerInterceptor(
	ctx context.Context,
	request any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	ctx = extractMetadata(ctx)
	ctx, span := t.tracer.Start(ctx, info.FullMethod, trace.WithSpanKind(trace.SpanKindServer))
	started := time.Now()
	response, err := handler(ctx, request)
	t.recordRPC(ctx, info.FullMethod, status.Code(err).String(), started)
	span.SetAttributes(attribute.String("rpc.grpc.status_code", status.Code(err).String()))
	span.End()
	return response, err
}

// StreamServerInterceptor traces and measures streaming RPCs.
func (t *Telemetry) StreamServerInterceptor(
	service any,
	stream grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	ctx := extractMetadata(stream.Context())
	ctx, span := t.tracer.Start(ctx, info.FullMethod, trace.WithSpanKind(trace.SpanKindServer))
	started := time.Now()
	err := handler(service, &contextServerStream{ServerStream: stream, ctx: ctx})
	t.recordRPC(ctx, info.FullMethod, status.Code(err).String(), started)
	span.SetAttributes(attribute.String("rpc.grpc.status_code", status.Code(err).String()))
	span.End()
	return err
}

// UnaryClientInterceptor propagates, traces, and measures unary RPCs.
func (t *Telemetry) UnaryClientInterceptor(
	ctx context.Context,
	method string,
	request, response any,
	connection *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	options ...grpc.CallOption,
) error {
	ctx, span := t.tracer.Start(ctx, method, trace.WithSpanKind(trace.SpanKindClient))
	ctx = injectMetadata(ctx)
	started := time.Now()
	err := invoker(ctx, method, request, response, connection, options...)
	t.recordRPC(ctx, method, status.Code(err).String(), started)
	span.SetAttributes(attribute.String("rpc.grpc.status_code", status.Code(err).String()))
	span.End()
	return err
}

// StreamClientInterceptor propagates, traces, and measures streaming RPC setup.
func (t *Telemetry) StreamClientInterceptor(
	ctx context.Context,
	description *grpc.StreamDesc,
	connection *grpc.ClientConn,
	method string,
	streamer grpc.Streamer,
	options ...grpc.CallOption,
) (grpc.ClientStream, error) {
	ctx, span := t.tracer.Start(ctx, method, trace.WithSpanKind(trace.SpanKindClient))
	ctx = injectMetadata(ctx)
	started := time.Now()
	stream, err := streamer(ctx, description, connection, method, options...)
	t.recordRPC(ctx, method, status.Code(err).String(), started)
	span.SetAttributes(attribute.String("rpc.grpc.status_code", status.Code(err).String()))
	span.End()
	return stream, err
}

func (t *Telemetry) recordRPC(ctx context.Context, method, code string, started time.Time) {
	attributes := metric.WithAttributes(
		attribute.String("rpc.method", method), attribute.String("rpc.grpc.status_code", code),
	)
	t.rpcRequests.Add(ctx, 1, attributes)
	t.rpcDuration.Record(ctx, time.Since(started).Seconds(), attributes)
}

type contextServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *contextServerStream) Context() context.Context {
	return s.ctx
}

type metadataCarrier metadata.MD

func (c metadataCarrier) Get(key string) string {
	values := metadata.MD(c).Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (c metadataCarrier) Set(key, value string) {
	metadata.MD(c).Set(key, value)
}

func (c metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for key := range c {
		keys = append(keys, key)
	}
	return keys
}

func extractMetadata(ctx context.Context) context.Context {
	incoming, _ := metadata.FromIncomingContext(ctx)
	return otel.GetTextMapPropagator().Extract(ctx, metadataCarrier(incoming.Copy()))
}

func injectMetadata(ctx context.Context) context.Context {
	outgoing, _ := metadata.FromOutgoingContext(ctx)
	outgoing = outgoing.Copy()
	otel.GetTextMapPropagator().Inject(ctx, metadataCarrier(outgoing))
	return metadata.NewOutgoingContext(ctx, outgoing)
}

var _ propagation.TextMapCarrier = metadataCarrier{}
