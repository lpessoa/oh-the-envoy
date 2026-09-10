// Package serviceb calls service-d over gRPC through the outbound Envoy
// Gateway instance instead of dialing service-d directly, simulating an
// egress-gateway routing pattern.
package serviceb

import (
	"context"
	"fmt"
	"log"

	chainv1 "envoy-experiment/gen/chain/v1"
	"envoy-experiment/internal/egress"
)

const hopName = "service-b"

// Server implements chainv1.ChainServiceServer and proxies to service-d.
type Server struct {
	chainv1.UnimplementedChainServiceServer

	// OutboundGatewayAddr is the host:port of the outbound Envoy Gateway
	// Service (e.g. outbound-gateway.envoy-experiment.svc.cluster.local:80).
	OutboundGatewayAddr string
	// ServiceDAuthority is the :authority (Host) value the outbound
	// gateway's GRPCRoute matches to route to service-d.
	ServiceDAuthority string
}

func New(outboundGatewayAddr, serviceDAuthority string) *Server {
	return &Server{OutboundGatewayAddr: outboundGatewayAddr, ServiceDAuthority: serviceDAuthority}
}

func (s *Server) Process(ctx context.Context, req *chainv1.ChainRequest) (*chainv1.ChainResponse, error) {
	log.Printf("[%s] trace=%s payload=%q hops=%v -> dialing outbound gateway %s (authority=%s)",
		hopName, req.GetTraceId(), req.GetPayload(), req.GetHops(), s.OutboundGatewayAddr, s.ServiceDAuthority)

	conn, err := egress.Dial(s.OutboundGatewayAddr, s.ServiceDAuthority)
	if err != nil {
		return nil, fmt.Errorf("%s: dial outbound gateway: %w", hopName, err)
	}
	defer conn.Close()

	client := chainv1.NewChainServiceClient(conn)

	forwarded := &chainv1.ChainRequest{
		TraceId: req.GetTraceId(),
		Payload: req.GetPayload(),
		Hops:    append(append([]string{}, req.GetHops()...), hopName),
	}

	resp, err := client.Process(ctx, forwarded)
	if err != nil {
		return nil, fmt.Errorf("%s: call service-d via outbound gateway: %w", hopName, err)
	}
	return resp, nil
}
