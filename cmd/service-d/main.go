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
	"envoy-experiment/internal/serviced"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdown := otelsetup.Init(context.Background(), "service-d")

	addr := envutil.Get("LISTEN_ADDR", ":9000")

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("service-d: listen %s: %v", addr, err)
	}

	grpcServer := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	chainv1.RegisterChainServiceServer(grpcServer, serviced.New())

	// Drain on SIGTERM/SIGINT so batched spans below get flushed.
	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()

	log.Printf("service-d: gRPC listening on %s", addr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("service-d: serve: %v", err)
	}

	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdown(sctx)
}
