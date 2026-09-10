package tokenservice

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "http://token-service.test:8080"
	testAudience = "envoy-experiment"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	s, err := New(testIssuer, testAudience, time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestMintedTokenVerifiesWithClaims(t *testing.T) {
	s := newTestService(t)
	tok, err := s.Mint(time.Now())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	parsed, err := jwt.Parse(tok, func(tk *jwt.Token) (any, error) {
		if tk.Method.Alg() != "RS256" {
			t.Fatalf("alg = %s, want RS256", tk.Method.Alg())
		}
		if kid, _ := tk.Header["kid"].(string); kid == "" {
			t.Fatal("missing kid header")
		}
		return &s.key.PublicKey, nil
	}, jwt.WithIssuer(testIssuer), jwt.WithAudience(testAudience), jwt.WithExpirationRequired())
	if err != nil || !parsed.Valid {
		t.Fatalf("token invalid: %v", err)
	}
}

func TestJWKSContainsSigningKey(t *testing.T) {
	s := newTestService(t)
	raw, err := s.JWKS()
	if err != nil {
		t.Fatalf("JWKS: %v", err)
	}
	var doc struct {
		Keys []struct {
			Kty, Kid, Use, Alg, N, E string
		} `json:"keys"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Keys) != 1 {
		t.Fatalf("keys = %d, want 1", len(doc.Keys))
	}
	k := doc.Keys[0]
	if k.Kty != "RSA" || k.Use != "sig" || k.Alg != "RS256" || k.Kid == "" || k.N == "" || k.E == "" {
		t.Errorf("bad JWKS entry: %+v", k)
	}
}

func TestTokenHandlerHappyPathAndMethodGuard(t *testing.T) {
	s := newTestService(t)

	rr := httptest.NewRecorder()
	s.TokenHandler(rr, httptest.NewRequest("POST", "/auth/token", nil))
	if rr.Code != 200 {
		t.Fatalf("POST status = %d", rr.Code)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.TokenType != "Bearer" || body.ExpiresIn != 3600 || strings.Count(body.AccessToken, ".") != 2 {
		t.Errorf("bad token response: %+v", body)
	}

	rr = httptest.NewRecorder()
	s.TokenHandler(rr, httptest.NewRequest("GET", "/auth/token", nil))
	if rr.Code != 405 {
		t.Errorf("GET status = %d, want 405", rr.Code)
	}
}
