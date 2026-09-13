package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"envoy-experiment/internal/envutil"
	"envoy-experiment/internal/otelsetup"
	"envoy-experiment/internal/tokenservice"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	ctx := context.Background()
	shutdown := otelsetup.Init(ctx, "token-service")
	defer shutdown(ctx)

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

	log.Printf("token-service: HTTP listening on %s (issuer=%s aud=%s)", addr, issuer, audience)
	if err := http.ListenAndServe(addr, otelhttp.NewHandler(mux, "token-service")); err != nil {
		log.Fatalf("token-service: serve: %v", err)
	}
}
