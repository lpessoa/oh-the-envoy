package serviced

import (
	"context"
	"strings"
	"testing"

	chainv1 "envoy-experiment/gen/chain/v1"
)

func TestProcessAppendsHop(t *testing.T) {
	s := New()
	resp, err := s.Process(context.Background(), &chainv1.ChainRequest{
		TraceId: "t1", Payload: "p", Hops: []string{"service-b"},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got, want := strings.Join(resp.GetHops(), ","), "service-b,service-d"; got != want {
		t.Errorf("hops = %q, want %q", got, want)
	}
	if strings.Contains(resp.GetMessage(), "direct route") {
		t.Errorf("Process message must not mention direct route: %q", resp.GetMessage())
	}
}

func TestProcessDirectAppendsHopAndMarksRoute(t *testing.T) {
	s := New()
	resp, err := s.ProcessDirect(context.Background(), &chainv1.ChainRequest{
		TraceId: "t2", Payload: "p", Hops: []string{"service-c"},
	})
	if err != nil {
		t.Fatalf("ProcessDirect: %v", err)
	}
	if got, want := strings.Join(resp.GetHops(), ","), "service-c,service-d"; got != want {
		t.Errorf("hops = %q, want %q", got, want)
	}
	if !strings.Contains(resp.GetMessage(), "direct route") {
		t.Errorf("ProcessDirect message must mention direct route: %q", resp.GetMessage())
	}
}
