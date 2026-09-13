package otelsetup

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
)

// Without OTEL_EXPORTER_OTLP_ENDPOINT, Init must be a no-op: the global
// TracerProvider stays untouched and the returned shutdown succeeds.
func TestInitNoOpWithoutEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	before := otel.GetTracerProvider()
	shutdown := Init(context.Background(), "test-service")
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("no-op shutdown returned error: %v", err)
	}
	if otel.GetTracerProvider() != before {
		t.Fatal("Init without endpoint replaced the global TracerProvider")
	}
}
