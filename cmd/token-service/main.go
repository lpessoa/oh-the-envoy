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
	"envoy-experiment/internal/tokenservice"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdown := otelsetup.Init(context.Background(), "token-service")

	addr := envutil.Get("LISTEN_ADDR", ":8080")
	issuer := envutil.Get("ISSUER", "http://token-service.envoy-experiment.svc.cluster.local:8080")
	audience := envutil.Get("AUDIENCE", "envoy-experiment")

	svc, err := tokenservice.New(issuer, audience, time.Hour)
	if err != nil {
		log.Fatalf("token-service: init: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/token", svc.TokenHandler)
	mux.HandleFunc("/auth/jwks.json", svc.JWKSHandler)

	server := &http.Server{
		Addr:    addr,
		Handler: otelsetup.WrapHTTP(mux, "token-service"),
	}

	// Drain on SIGTERM/SIGINT so batched spans below get flushed.
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(sctx)
	}()

	log.Printf("token-service: HTTP listening on %s (issuer=%s aud=%s)", addr, issuer, audience)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("token-service: serve: %v", err)
	}

	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdown(sctx)
}
