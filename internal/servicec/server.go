// Package servicec exposes an HTTP/2 endpoint (behind the inbound Envoy
// Gateway) and calls service-d's ProcessDirect RPC through the outbound
// Envoy Gateway — the short path of the sandbox.
package servicec

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	chainv1 "envoy-experiment/gen/chain/v1"
	"envoy-experiment/internal/certident"
	"envoy-experiment/internal/egress"
)

const hopName = "service-c"

type Handler struct {
	OutboundGatewayAddr string
	ServiceDAuthority   string
}

func New(outboundGatewayAddr, serviceDAuthority string) *Handler {
	return &Handler{OutboundGatewayAddr: outboundGatewayAddr, ServiceDAuthority: serviceDAuthority}
}

type chainResult struct {
	Message  string              `json:"message"`
	Hops     []string            `json:"hops"`
	Protocol string              `json:"received_protocol"`
	Client   *certident.Identity `json:"client,omitempty"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	defer r.Body.Close()

	traceID := fmt.Sprintf("%d", time.Now().UnixNano())
	payload := fmt.Sprintf("method=%s path=%s body=%s", r.Method, r.URL.Path, string(body))

	log.Printf("[%s] received %s %s (proto=%s) trace=%s", hopName, r.Method, r.URL.Path, r.Proto, traceID)

	conn, err := egress.Dial(h.OutboundGatewayAddr, h.ServiceDAuthority)
	if err != nil {
		http.Error(w, fmt.Sprintf("%s: dial outbound gateway: %v", hopName, err), http.StatusBadGateway)
		return
	}
	defer conn.Close()

	client := chainv1.NewChainServiceClient(conn)
	resp, err := client.ProcessDirect(r.Context(), &chainv1.ChainRequest{
		TraceId: traceID,
		Payload: payload,
		Hops:    []string{hopName},
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("%s: call service-d via outbound gateway: %v", hopName, err), http.StatusBadGateway)
		return
	}

	result := chainResult{Message: resp.GetMessage(), Hops: append(resp.GetHops(), hopName+"(response)"), Protocol: r.Proto}
	if id, ok := certident.Parse(r.Header.Get("x-forwarded-client-cert")); ok {
		result.Client = &id
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
