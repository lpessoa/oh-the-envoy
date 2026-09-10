// Package tokenservice mints RS256 JWTs and serves the matching JWKS. Keys
// are generated in memory at startup — nothing is persisted, so a restart
// rotates the keypair and invalidates previously minted tokens.
package tokenservice

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Service struct {
	key      *rsa.PrivateKey
	kid      string
	issuer   string
	audience string
	ttl      time.Duration
}

func New(issuer, audience string, ttl time.Duration) (*Service, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate keypair: %w", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("marshal public key: %w", err)
	}
	sum := sha256.Sum256(der)
	return &Service{
		key:      key,
		kid:      base64.RawURLEncoding.EncodeToString(sum[:8]),
		issuer:   issuer,
		audience: audience,
		ttl:      ttl,
	}, nil
}

// Mint signs a fresh token valid from now for the configured TTL.
func (s *Service) Mint(now time.Time) (string, error) {
	claims := jwt.MapClaims{
		"iss": s.issuer,
		"aud": []string{s.audience},
		"sub": "demo-user",
		"iat": now.Unix(),
		"exp": now.Add(s.ttl).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = s.kid
	return tok.SignedString(s.key)
}

// JWKS returns the JSON Web Key Set for the current signing key.
func (s *Service) JWKS() ([]byte, error) {
	eBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(eBuf, uint64(s.key.PublicKey.E))
	// Strip leading zero bytes from the exponent per RFC 7518 base64url(uint).
	i := 0
	for i < 7 && eBuf[i] == 0 {
		i++
	}
	return json.Marshal(map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"kid": s.kid,
			"use": "sig",
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(s.key.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(eBuf[i:]),
		}},
	})
}

func (s *Service) TokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	tok, err := s.Mint(time.Now())
	if err != nil {
		http.Error(w, fmt.Sprintf("mint: %v", err), http.StatusInternalServerError)
		return
	}
	log.Printf("[token-service] minted token kid=%s", s.kid)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token": tok,
		"token_type":   "Bearer",
		"expires_in":   int(s.ttl.Seconds()),
	})
}

func (s *Service) JWKSHandler(w http.ResponseWriter, r *http.Request) {
	doc, err := s.JWKS()
	if err != nil {
		http.Error(w, fmt.Sprintf("jwks: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(doc)
}
