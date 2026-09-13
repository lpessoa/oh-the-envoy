package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"envoy-experiment/internal/envutil"
	"envoy-experiment/internal/otelsetup"
	"envoy-experiment/internal/servicea"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdown := otelsetup.Init(context.Background(), "service-a")

	addr := envutil.Get("LISTEN_ADDR", ":8080")
	serviceBAddr := envutil.Get("SERVICE_B_ADDR", "service-b.envoy-experiment.svc.cluster.local:9000")

	handler := otelsetup.WrapHTTP(servicea.New(serviceBAddr), "service-a")

	// Serve plain HTTP/2 (h2c, no TLS) so both direct clients and the
	// inbound Envoy Gateway can speak HTTP/2 to this backend.
	h2s := &http2.Server{}
	server := &http.Server{
		Addr:    addr,
		Handler: h2c.NewHandler(handler, h2s),
	}

	// Drain on SIGTERM/SIGINT so batched spans below get flushed.
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(sctx)
	}()

	log.Printf("service-a: HTTP/2 (h2c) listening on %s (service-b=%s)", addr, serviceBAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("service-a: serve: %v", err)
	}

	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdown(sctx)
}
