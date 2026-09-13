// Package otelsetup configures OpenTelemetry tracing for the demo services.
// It is deliberately opt-in at runtime: when OTEL_EXPORTER_OTLP_ENDPOINT is
// unset (local runs, unit tests) Init is a no-op and the otelhttp/otelgrpc
// wrappers fall back to the global no-op tracer.
package otelsetup

import (
	"context"
	"log"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Init installs a global OTLP/gRPC TracerProvider (batched export) plus W3C
// tracecontext/baggage propagation, matching Envoy's OpenTelemetry tracer so
// gateway and service spans join into one distributed trace.
//
// It returns a shutdown function that flushes pending spans; call it on
// process exit. When OTEL_EXPORTER_OTLP_ENDPOINT is unset, Init changes
// nothing and the returned shutdown is a no-op.
func Init(ctx context.Context, serviceName string) func(context.Context) error {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		return func(context.Context) error { return nil }
	}

	// Endpoint is a bare host:port (see deploy/k8s manifests); the demo
	// collector speaks plaintext OTLP/gRPC.
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		// Telemetry must never take the service down.
		log.Printf("otelsetup: disabled, exporter init failed: %v", err)
		return func(context.Context) error { return nil }
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(attribute.String("service.name", serviceName)),
	)
	if err != nil {
		log.Printf("otelsetup: disabled, resource init failed: %v", err)
		return func(context.Context) error { return nil }
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	log.Printf("otelsetup: exporting traces for %s to %s", serviceName, endpoint)
	return tp.Shutdown
}
