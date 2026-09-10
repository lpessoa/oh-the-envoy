// Package serviced implements the terminal hop of the chain. It never calls
// any other service — it just answers.
package serviced

import (
	"context"
	"log"

	chainv1 "envoy-experiment/gen/chain/v1"
)

const hopName = "service-d"

// Server implements chainv1.ChainServiceServer.
type Server struct {
	chainv1.UnimplementedChainServiceServer
}

func New() *Server {
	return &Server{}
}

func (s *Server) Process(ctx context.Context, req *chainv1.ChainRequest) (*chainv1.ChainResponse, error) {
	log.Printf("[%s] trace=%s payload=%q hops=%v", hopName, req.GetTraceId(), req.GetPayload(), req.GetHops())

	hops := append(append([]string{}, req.GetHops()...), hopName)
	return &chainv1.ChainResponse{
		Message: "hello from " + hopName + ", payload=" + req.GetPayload(),
		Hops:    hops,
	}, nil
}

func (s *Server) ProcessDirect(ctx context.Context, req *chainv1.ChainRequest) (*chainv1.ChainResponse, error) {
	log.Printf("[%s] (direct route) trace=%s payload=%q hops=%v", hopName, req.GetTraceId(), req.GetPayload(), req.GetHops())

	hops := append(append([]string{}, req.GetHops()...), hopName)
	return &chainv1.ChainResponse{
		Message: "hello from " + hopName + " (direct route), payload=" + req.GetPayload(),
		Hops:    hops,
	}, nil
}
