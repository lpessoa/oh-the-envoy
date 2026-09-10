#!/usr/bin/env bash
# Five-step demo of the JWT-protected multi-route sandbox. Doubles as the
# E2E acceptance test: exits non-zero if any expectation fails.
set -u
KCTX="${KCTX:-k3d-envoy-experiment}"
PORT="${PORT:-8888}"
BASE="http://localhost:${PORT}"
HOSTHDR="Host: inbound.local"
pass=0; fail=0

check() { # check <label> <expected> <actual>
  local label="$1" expected="$2" actual="$3"
  if [[ "$actual" == *"$expected"* ]]; then
    echo "PASS  $label"; pass=$((pass+1))
  else
    echo "FAIL  $label"; echo "      expected to contain: $expected"; echo "      got: $actual"; fail=$((fail+1))
  fi
}

kubectl --context "$KCTX" port-forward -n envoy-gateway-system svc/inbound-gateway "${PORT}:80" >/dev/null 2>&1 &
PF_PID=$!
trap 'kill $PF_PID 2>/dev/null' EXIT
sleep 3

echo "== 1. Mint a token (open /auth route) =="
TOKEN_RESP=$(curl -s -X POST -H "$HOSTHDR" "$BASE/auth/token")
TOKEN=$(printf '%s' "$TOKEN_RESP" | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')
check "token minted" '"token_type":"Bearer"' "$TOKEN_RESP"
[[ -n "$TOKEN" ]] || { echo "FAIL  empty token, aborting"; exit 1; }

echo "== 2. No token -> 401 =="
CODE=$(curl -s -o /dev/null -w '%{http_code}' --http2-prior-knowledge -H "$HOSTHDR" "$BASE/a/hello")
check "unauthenticated /a rejected" "401" "$CODE"

echo "== 3. Path A full cycle (A -> B -> egress -> D) =="
RESP=$(curl -s --http2-prior-knowledge -H "$HOSTHDR" -H "Authorization: Bearer $TOKEN" -X POST "$BASE/a/hello" -d 'ping-a')
check "path A hops" '"service-a","service-b","service-d","service-a(response)"' "$RESP"

echo "== 4. Path C short cycle (C -> egress -> D, ProcessDirect) =="
RESP=$(curl -s --http2-prior-knowledge -H "$HOSTHDR" -H "Authorization: Bearer $TOKEN" -X POST "$BASE/c/hello" -d 'ping-c')
check "path C hops" '"service-c","service-d","service-c(response)"' "$RESP"
check "path C direct route marker" "(direct route)" "$RESP"

echo "== 5. Tampered token -> 401 =="
CODE=$(curl -s -o /dev/null -w '%{http_code}' --http2-prior-knowledge -H "$HOSTHDR" -H "Authorization: Bearer ${TOKEN}tampered" "$BASE/a/hello")
check "tampered token rejected" "401" "$CODE"

echo ""
echo "demo: $pass passed, $fail failed"
[[ $fail -eq 0 ]]
