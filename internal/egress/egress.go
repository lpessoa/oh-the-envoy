// Package egress dials the outbound Envoy Gateway with an overridden
// :authority so the gateway's GRPCRoute hostname matching selects the
// intended upstream. Shared by every service that egresses via the gateway.
package egress

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Dial returns a plaintext client connection to addr (the outbound gateway
// Service) whose requests carry authority as :authority.
func Dial(addr, authority string) (*grpc.ClientConn, error) {
	return grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithAuthority(authority),
	)
}
