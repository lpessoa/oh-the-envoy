#!/usr/bin/env bash
# Generate the demo mTLS PKI into .certs/ (git-ignored, never committed):
#   ca.crt/ca.key         demo certificate authority
#   server.crt/server.key ingress server cert (SAN: inbound.local)
#   client.crt/client.key client cert the demo presents to the gateway
#   client-alice.crt/client-alice.key client cert for CN=alice
#   client-bob.crt/client-bob.key     client cert for CN=bob
# Idempotent: skips generation when .certs/ is already populated.
set -euo pipefail

CERTS_DIR="${CERTS_DIR:-.certs}"

if [[ -f "$CERTS_DIR/ca.crt" && -f "$CERTS_DIR/server.crt" && -f "$CERTS_DIR/client.crt" \
      && -f "$CERTS_DIR/client-alice.crt" && -f "$CERTS_DIR/client-bob.crt" ]]; then
  echo "certs already present in $CERTS_DIR, skipping (delete the dir to regenerate)"
  exit 0
fi

mkdir -p "$CERTS_DIR"

echo "== generating demo CA =="
openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -subj "/CN=envoy-experiment-demo-ca" \
  -keyout "$CERTS_DIR/ca.key" -out "$CERTS_DIR/ca.crt" >/dev/null 2>&1

echo "== generating server cert (SAN: inbound.local) =="
openssl req -newkey rsa:2048 -nodes \
  -subj "/CN=inbound.local" \
  -addext "subjectAltName=DNS:inbound.local" \
  -keyout "$CERTS_DIR/server.key" -out "$CERTS_DIR/server.csr" >/dev/null 2>&1
openssl x509 -req -days 365 \
  -in "$CERTS_DIR/server.csr" \
  -CA "$CERTS_DIR/ca.crt" -CAkey "$CERTS_DIR/ca.key" -CAcreateserial \
  -copy_extensions copy \
  -out "$CERTS_DIR/server.crt" >/dev/null 2>&1

echo "== generating client cert =="
openssl req -newkey rsa:2048 -nodes \
  -subj "/CN=demo-client" \
  -keyout "$CERTS_DIR/client.key" -out "$CERTS_DIR/client.csr" >/dev/null 2>&1
openssl x509 -req -days 365 \
  -in "$CERTS_DIR/client.csr" \
  -CA "$CERTS_DIR/ca.crt" -CAkey "$CERTS_DIR/ca.key" -CAcreateserial \
  -out "$CERTS_DIR/client.crt" >/dev/null 2>&1

echo "== generating named client cert: alice =="
openssl req -newkey rsa:2048 -nodes \
  -subj "/CN=alice" \
  -keyout "$CERTS_DIR/client-alice.key" -out "$CERTS_DIR/client-alice.csr" >/dev/null 2>&1
openssl x509 -req -days 365 \
  -in "$CERTS_DIR/client-alice.csr" \
  -CA "$CERTS_DIR/ca.crt" -CAkey "$CERTS_DIR/ca.key" -CAcreateserial \
  -out "$CERTS_DIR/client-alice.crt" >/dev/null 2>&1

echo "== generating named client cert: bob =="
openssl req -newkey rsa:2048 -nodes \
  -subj "/CN=bob" \
  -keyout "$CERTS_DIR/client-bob.key" -out "$CERTS_DIR/client-bob.csr" >/dev/null 2>&1
openssl x509 -req -days 365 \
  -in "$CERTS_DIR/client-bob.csr" \
  -CA "$CERTS_DIR/ca.crt" -CAkey "$CERTS_DIR/ca.key" -CAcreateserial \
  -out "$CERTS_DIR/client-bob.crt" >/dev/null 2>&1

rm -f "$CERTS_DIR"/*.csr "$CERTS_DIR"/*.srl
echo "demo PKI written to $CERTS_DIR/"
