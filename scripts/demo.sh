#!/usr/bin/env bash
# Eight-step demo of the mTLS + JWT protected multi-route sandbox. Doubles as
# the E2E acceptance test: exits non-zero if any expectation fails.
set -u
KCTX="${KCTX:-k3d-envoy-experiment}"
PORT="${PORT:-8888}"
CERTS="${CERTS:-.certs}"
BASE="https://inbound.local:${PORT}"
HOSTHDR="Host: inbound.local"
# Client-side mTLS: trust the demo CA, present the demo client cert, and pin
# inbound.local to the local port-forward so SNI matches the server cert SAN.
TLS=(--cacert "$CERTS/ca.crt" --cert "$CERTS/client.crt" --key "$CERTS/client.key" --resolve "inbound.local:${PORT}:127.0.0.1")
[[ -f "$CERTS/client.crt" ]] || { echo "missing $CERTS/client.crt — run 'make certs' first"; exit 1; }
[[ -f "$CERTS/client-alice.crt" && -f "$CERTS/client-bob.crt" ]] || { echo "missing $CERTS/client-{alice,bob}.crt — run 'make certs' first"; exit 1; }
pass=0; fail=0

check() { # check <label> <expected> <actual>
  local label="$1" expected="$2" actual="$3"
  if [[ "$actual" == *"$expected"* ]]; then
    echo "PASS  $label"; pass=$((pass+1))
  else
    echo "FAIL  $label"; echo "      expected to contain: $expected"; echo "      got: $actual"; fail=$((fail+1))
  fi
}

kubectl --context "$KCTX" port-forward -n envoy-gateway-system svc/inbound-gateway "${PORT}:443" >/dev/null 2>&1 &
PF_PID=$!
trap 'kill $PF_PID 2>/dev/null' EXIT
sleep 3

echo "== 0. No client certificate -> TLS handshake rejected =="
NOCERT_OUT=$(curl -sS -o /dev/null --cacert "$CERTS/ca.crt" --resolve "inbound.local:${PORT}:127.0.0.1" "$BASE/auth/token" -X POST 2>&1)
NOCERT_RC=$?
if [[ $NOCERT_RC -ne 0 ]]; then
  echo "PASS  connection without client cert rejected (curl exit $NOCERT_RC)"; pass=$((pass+1))
else
  echo "FAIL  connection without client cert unexpectedly succeeded"; fail=$((fail+1))
fi

echo "== 1. Mint a token (open /auth route, mTLS still required) =="
TOKEN_RESP=$(curl -s "${TLS[@]}" -X POST -H "$HOSTHDR" "$BASE/auth/token")
TOKEN=$(printf '%s' "$TOKEN_RESP" | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')
check "token minted" '"token_type":"Bearer"' "$TOKEN_RESP"
[[ -n "$TOKEN" ]] || { echo "FAIL  empty token, aborting"; exit 1; }

echo "== 2. No token -> 401 =="
CODE=$(curl -s "${TLS[@]}" -o /dev/null -w '%{http_code}' -H "$HOSTHDR" "$BASE/a/hello")
check "unauthenticated /a rejected" "401" "$CODE"

echo "== 3. Path A full cycle (A -> B -> egress -> D) =="
RESP=$(curl -s "${TLS[@]}" -H "$HOSTHDR" -H "Authorization: Bearer $TOKEN" -X POST "$BASE/a/hello" -d 'ping-a')
check "path A hops" '"service-a","service-b","service-d","service-a(response)"' "$RESP"

echo "== 4. Path C short cycle (C -> egress -> D, ProcessDirect) =="
RESP=$(curl -s "${TLS[@]}" -H "$HOSTHDR" -H "Authorization: Bearer $TOKEN" -X POST "$BASE/c/hello" -d 'ping-c')
check "path C hops" '"service-c","service-d","service-c(response)"' "$RESP"
check "path C direct route marker" "(direct route)" "$RESP"

echo "== 5. Tampered token -> 401 =="
CODE=$(curl -s "${TLS[@]}" -o /dev/null -w '%{http_code}' -H "$HOSTHDR" -H "Authorization: Bearer ${TOKEN}tampered" "$BASE/a/hello")
check "tampered token rejected" "401" "$CODE"

echo "== 6. Distinct client identities (alice vs bob) surfaced by the gateway =="
ALICE_TLS=(--cacert "$CERTS/ca.crt" --cert "$CERTS/client-alice.crt" --key "$CERTS/client-alice.key" --resolve "inbound.local:${PORT}:127.0.0.1")
BOB_TLS=(--cacert "$CERTS/ca.crt" --cert "$CERTS/client-bob.crt" --key "$CERTS/client-bob.key" --resolve "inbound.local:${PORT}:127.0.0.1")
ALICE_RESP=$(curl -s "${ALICE_TLS[@]}" -H "$HOSTHDR" -H "Authorization: Bearer $TOKEN" -X POST "$BASE/a/hello" -d 'ping-alice')
BOB_RESP=$(curl -s "${BOB_TLS[@]}" -H "$HOSTHDR" -H "Authorization: Bearer $TOKEN" -X POST "$BASE/a/hello" -d 'ping-bob')
check "alice identified by gateway" '"client":{"cn":"alice"' "$ALICE_RESP"
check "bob identified by gateway" '"client":{"cn":"bob"' "$BOB_RESP"

echo "== 7. Rate limit enforced per client identity (bob: 10 req/min) =="
rl_pass=true
for i in $(seq 1 10); do
  CODE=$(curl -s "${BOB_TLS[@]}" -o /dev/null -w '%{http_code}' -H "$HOSTHDR" -H "Authorization: Bearer $TOKEN" -X POST "$BASE/a/hello" -d "burst-$i")
  if [[ "$CODE" != "200" ]]; then
    echo "      request $i got $CODE, want 200"; rl_pass=false
  fi
done
if $rl_pass; then echo "PASS  first 10 bob requests succeeded"; pass=$((pass+1)); else echo "FAIL  first 10 bob requests"; fail=$((fail+1)); fi

CODE=$(curl -s "${BOB_TLS[@]}" -o /dev/null -w '%{http_code}' -H "$HOSTHDR" -H "Authorization: Bearer $TOKEN" -X POST "$BASE/a/hello" -d 'burst-11')
check "11th bob request rate-limited" "429" "$CODE"

echo ""
echo "demo: $pass passed, $fail failed"
[[ $fail -eq 0 ]]
