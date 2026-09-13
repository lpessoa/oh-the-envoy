package main

import (
	"context"
	"log"
	"net"

	chainv1 "envoy-experiment/gen/chain/v1"
	"envoy-experiment/internal/envutil"
	"envoy-experiment/internal/otelsetup"
	"envoy-experiment/internal/serviced"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

func main() {
	ctx := context.Background()
	shutdown := otelsetup.Init(ctx, "service-d")
	defer shutdown(ctx)

	addr := envutil.Get("LISTEN_ADDR", ":9000")

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("service-d: listen %s: %v", addr, err)
	}

	grpcServer := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	chainv1.RegisterChainServiceServer(grpcServer, serviced.New())

	log.Printf("service-d: gRPC listening on %s", addr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("service-d: serve: %v", err)
	}
}
