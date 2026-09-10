package main

import (
	"log"
	"net"

	chainv1 "envoy-experiment/gen/chain/v1"
	"envoy-experiment/internal/envutil"
	"envoy-experiment/internal/serviceb"

	"google.golang.org/grpc"
)

func main() {
	addr := envutil.Get("LISTEN_ADDR", ":9000")
	outboundGatewayAddr := envutil.Get("OUTBOUND_GATEWAY_ADDR", "outbound-gateway.envoy-experiment.svc.cluster.local:80")
	serviceDAuthority := envutil.Get("SERVICE_D_AUTHORITY", "service-d.internal")

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("service-b: listen %s: %v", addr, err)
	}

	grpcServer := grpc.NewServer()
	chainv1.RegisterChainServiceServer(grpcServer, serviceb.New(outboundGatewayAddr, serviceDAuthority))

	log.Printf("service-b: gRPC listening on %s (outbound gateway=%s, authority=%s)", addr, outboundGatewayAddr, serviceDAuthority)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("service-b: serve: %v", err)
	}
}
