package servicea

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	chainv1 "envoy-experiment/gen/chain/v1"

	"google.golang.org/grpc"
)

type fakeServiceB struct {
	chainv1.UnimplementedChainServiceServer
}

func (f *fakeServiceB) Process(ctx context.Context, req *chainv1.ChainRequest) (*chainv1.ChainResponse, error) {
	return &chainv1.ChainResponse{
		Message: "hello from service-b, payload=" + req.GetPayload(),
		Hops:    append(append([]string{}, req.GetHops()...), "service-b"),
	}, nil
}

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer()
	chainv1.RegisterChainServiceServer(gs, &fakeServiceB{})
	go gs.Serve(lis)
	t.Cleanup(gs.Stop)
	return New(lis.Addr().String())
}

type responseBody struct {
	Message string   `json:"message"`
	Hops    []string `json:"hops"`
	Client  *struct {
		CN          string `json:"cn"`
		Fingerprint string `json:"fingerprint"`
	} `json:"client"`
}

func TestHandlerAddsClientIdentityWhenXFCCPresent(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest("POST", "/a/hello", strings.NewReader("ping"))
	req.Header.Set("x-forwarded-client-cert", `Hash=ab12cd34;Subject="CN=alice"`)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var body responseBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Client == nil {
		t.Fatal("client field missing, want populated")
	}
	if body.Client.CN != "alice" || body.Client.Fingerprint != "ab12cd34" {
		t.Errorf("client = %+v, want cn=alice fingerprint=ab12cd34", *body.Client)
	}
}

func TestHandlerOmitsClientIdentityWhenXFCCAbsent(t *testing.T) {
	h := newTestHandler(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/a/hello", strings.NewReader("ping")))

	if rr.Code != 200 {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var body responseBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Client != nil {
		t.Errorf("client = %+v, want nil (no XFCC header)", *body.Client)
	}
}
