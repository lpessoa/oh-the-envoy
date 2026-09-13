package main

import (
	"context"
	"log"
	"net/http"

	"envoy-experiment/internal/envutil"
	"envoy-experiment/internal/otelsetup"
	"envoy-experiment/internal/servicec"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	ctx := context.Background()
	shutdown := otelsetup.Init(ctx, "service-c")
	defer shutdown(ctx)

	addr := envutil.Get("LISTEN_ADDR", ":8080")
	outboundGatewayAddr := envutil.Get("OUTBOUND_GATEWAY_ADDR", "outbound-gateway.envoy-gateway-system.svc.cluster.local:80")
	serviceDAuthority := envutil.Get("SERVICE_D_AUTHORITY", "service-d.internal")

	handler := otelhttp.NewHandler(servicec.New(outboundGatewayAddr, serviceDAuthority), "service-c")

	h2s := &http2.Server{}
	server := &http.Server{
		Addr:    addr,
		Handler: h2c.NewHandler(handler, h2s),
	}

	log.Printf("service-c: HTTP/2 (h2c) listening on %s (outbound gateway=%s, authority=%s)", addr, outboundGatewayAddr, serviceDAuthority)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("service-c: serve: %v", err)
	}
}
