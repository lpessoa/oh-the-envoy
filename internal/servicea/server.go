// Package servicea exposes an HTTP/2 endpoint (behind the inbound Envoy
// Gateway) and repasses each incoming request to service-b over gRPC.
package servicea

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	chainv1 "envoy-experiment/gen/chain/v1"
	"envoy-experiment/internal/certident"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const hopName = "service-a"

// Handler implements http.Handler and repasses incoming requests to
// service-b over gRPC.
type Handler struct {
	// ServiceBAddr is the host:port of service-b (in-cluster ClusterIP,
	// called directly — no gateway in between).
	ServiceBAddr string
}

func New(serviceBAddr string) *Handler {
	return &Handler{ServiceBAddr: serviceBAddr}
}

type chainResult struct {
	Message  string               `json:"message"`
	Hops     []string             `json:"hops"`
	Protocol string               `json:"received_protocol"`
	Client   *certident.Identity `json:"client,omitempty"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	defer r.Body.Close()

	traceID := fmt.Sprintf("%d", time.Now().UnixNano())
	payload := fmt.Sprintf("method=%s path=%s body=%s", r.Method, r.URL.Path, string(body))

	log.Printf("[%s] received %s %s (proto=%s) trace=%s", hopName, r.Method, r.URL.Path, r.Proto, traceID)

	conn, err := grpc.NewClient(h.ServiceBAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		http.Error(w, fmt.Sprintf("%s: dial service-b: %v", hopName, err), http.StatusBadGateway)
		return
	}
	defer conn.Close()

	client := chainv1.NewChainServiceClient(conn)
	resp, err := client.Process(r.Context(), &chainv1.ChainRequest{
		TraceId: traceID,
		Payload: payload,
		Hops:    []string{hopName},
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("%s: call service-b: %v", hopName, err), http.StatusBadGateway)
		return
	}

	result := chainResult{Message: resp.GetMessage(), Hops: append(resp.GetHops(), hopName+"(response)"), Protocol: r.Proto}
	if id, ok := certident.Parse(r.Header.Get("x-forwarded-client-cert")); ok {
		result.Client = &id
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
