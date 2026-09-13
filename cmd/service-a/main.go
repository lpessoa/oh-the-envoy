package main

import (
	"context"
	"log"
	"net/http"

	"envoy-experiment/internal/envutil"
	"envoy-experiment/internal/otelsetup"
	"envoy-experiment/internal/servicea"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	ctx := context.Background()
	shutdown := otelsetup.Init(ctx, "service-a")
	defer shutdown(ctx)

	addr := envutil.Get("LISTEN_ADDR", ":8080")
	serviceBAddr := envutil.Get("SERVICE_B_ADDR", "service-b.envoy-experiment.svc.cluster.local:9000")

	handler := otelhttp.NewHandler(servicea.New(serviceBAddr), "service-a")

	// Serve plain HTTP/2 (h2c, no TLS) so both direct clients and the
	// inbound Envoy Gateway can speak HTTP/2 to this backend.
	h2s := &http2.Server{}
	server := &http.Server{
		Addr:    addr,
		Handler: h2c.NewHandler(handler, h2s),
	}

	log.Printf("service-a: HTTP/2 (h2c) listening on %s (service-b=%s)", addr, serviceBAddr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("service-a: serve: %v", err)
	}
}
