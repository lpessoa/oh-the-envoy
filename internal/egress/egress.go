// Package egress dials the outbound Envoy Gateway with an overridden
// :authority so the gateway's GRPCRoute hostname matching selects the
// intended upstream. Shared by every service that egresses via the gateway.
package egress

import (
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Dial returns a plaintext client connection to addr (the outbound gateway
// Service) whose requests carry authority as :authority. Calls carry OTel
// client spans and W3C trace context (no-op unless otelsetup.Init ran).
func Dial(addr, authority string) (*grpc.ClientConn, error) {
	return grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithAuthority(authority),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
}
