package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	chainv1 "envoy-experiment/gen/chain/v1"
	"envoy-experiment/internal/envutil"
	"envoy-experiment/internal/otelsetup"
	"envoy-experiment/internal/serviceb"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdown := otelsetup.Init(context.Background(), "service-b")

	addr := envutil.Get("LISTEN_ADDR", ":9000")
	outboundGatewayAddr := envutil.Get("OUTBOUND_GATEWAY_ADDR", "outbound-gateway.envoy-experiment.svc.cluster.local:80")
	serviceDAuthority := envutil.Get("SERVICE_D_AUTHORITY", "service-d.internal")

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("service-b: listen %s: %v", addr, err)
	}

	grpcServer := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	chainv1.RegisterChainServiceServer(grpcServer, serviceb.New(outboundGatewayAddr, serviceDAuthority))

	// Drain on SIGTERM/SIGINT so batched spans below get flushed.
	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()

	log.Printf("service-b: gRPC listening on %s (outbound gateway=%s, authority=%s)", addr, outboundGatewayAddr, serviceDAuthority)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("service-b: serve: %v", err)
	}

	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdown(sctx)
}
