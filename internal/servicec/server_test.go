package servicec

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

type fakeGateway struct {
	chainv1.UnimplementedChainServiceServer
}

func (f *fakeGateway) ProcessDirect(ctx context.Context, req *chainv1.ChainRequest) (*chainv1.ChainResponse, error) {
	return &chainv1.ChainResponse{
		Message: "hello from service-d (direct route), payload=" + req.GetPayload(),
		Hops:    append(append([]string{}, req.GetHops()...), "service-d"),
	}, nil
}

func TestHandlerCallsProcessDirectAndReportsHops(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer()
	chainv1.RegisterChainServiceServer(gs, &fakeGateway{})
	go gs.Serve(lis)
	defer gs.Stop()

	h := New(lis.Addr().String(), "service-d.internal")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/c/hello", strings.NewReader("ping")))

	if rr.Code != 200 {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Message string   `json:"message"`
		Hops    []string `json:"hops"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, want := strings.Join(body.Hops, ","), "service-c,service-d,service-c(response)"; got != want {
		t.Errorf("hops = %q, want %q", got, want)
	}
	if !strings.Contains(body.Message, "direct route") {
		t.Errorf("message = %q, want direct route marker", body.Message)
	}
}

func TestHandlerAddsClientIdentityWhenXFCCPresent(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer()
	chainv1.RegisterChainServiceServer(gs, &fakeGateway{})
	go gs.Serve(lis)
	defer gs.Stop()

	h := New(lis.Addr().String(), "service-d.internal")
	req := httptest.NewRequest("POST", "/c/hello", strings.NewReader("ping"))
	req.Header.Set("x-forwarded-client-cert", `Hash=ef56gh78;Subject="CN=bob"`)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Client *struct {
			CN          string `json:"cn"`
			Fingerprint string `json:"fingerprint"`
		} `json:"client"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Client == nil {
		t.Fatal("client field missing, want populated")
	}
	if body.Client.CN != "bob" || body.Client.Fingerprint != "ef56gh78" {
		t.Errorf("client = %+v, want cn=bob fingerprint=ef56gh78", *body.Client)
	}
}

func TestHandlerOmitsClientIdentityWhenXFCCAbsent(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer()
	chainv1.RegisterChainServiceServer(gs, &fakeGateway{})
	go gs.Serve(lis)
	defer gs.Stop()

	h := New(lis.Addr().String(), "service-d.internal")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/c/hello", strings.NewReader("ping")))

	if rr.Code != 200 {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Client *struct {
			CN          string `json:"cn"`
			Fingerprint string `json:"fingerprint"`
		} `json:"client"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Client != nil {
		t.Errorf("client = %+v, want nil (no XFCC header)", *body.Client)
	}
}
