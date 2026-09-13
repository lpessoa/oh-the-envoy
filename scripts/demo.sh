#!/usr/bin/env bash
# Nine-step demo of the mTLS + JWT protected multi-route sandbox. Doubles as
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
# Bob's budget is 10 req/min on route-a. Step 6 already spent one token,
# and Envoy's Local bucket refills continuously (~1 token every 6s), so
# the exact success count before the first 429 can drift by a token or
# two. Burst up to 15 requests and assert the limiter engages: more than
# 5 successes proves bob got his own 10/min bucket (not the 5/min
# default), and a 429 proves the limit is enforced. Re-running the demo
# within the same minute starts with a drained bucket -- expect FAILs
# here (and a 429 in step 6) until the window refills.
rl_ok=0; rl_limited=no
for i in $(seq 1 15); do
  CODE=$(curl -s "${BOB_TLS[@]}" -o /dev/null -w '%{http_code}' -H "$HOSTHDR" -H "Authorization: Bearer $TOKEN" -X POST "$BASE/a/hello" -d "burst-$i")
  if [[ "$CODE" == "200" ]]; then
    rl_ok=$((rl_ok+1))
  elif [[ "$CODE" == "429" ]]; then
    rl_limited=yes; break
  else
    echo "      request $i got unexpected $CODE"; break
  fi
done
if [[ "$rl_limited" == "yes" && $rl_ok -gt 5 ]]; then
  echo "PASS  bob rate-limited after $rl_ok successes (429 observed)"; pass=$((pass+1))
else
  echo "FAIL  bob rate limit: successes=$rl_ok, 429 observed=$rl_limited (re-run within the same minute? wait 60s and retry)"; fail=$((fail+1))
fi

echo "== 8. Observability: gateway metrics in Prometheus, traces in Tempo =="
# Steps 0-7 generated the traffic; telemetry lands asynchronously (batched
# spans, 5s scrape interval), so poll each backend with a deadline.
kubectl --context "$KCTX" port-forward -n monitoring svc/prometheus 19090:9090 >/dev/null 2>&1 &
PROM_PF=$!
kubectl --context "$KCTX" port-forward -n monitoring svc/tempo 13200:3200 >/dev/null 2>&1 &
TEMPO_PF=$!
trap 'kill $PF_PID $PROM_PF $TEMPO_PF 2>/dev/null' EXIT
sleep 3

metrics_ok=no
for i in $(seq 1 10); do
  PROM_RESP=$(curl -s "http://127.0.0.1:19090/api/v1/query?query=envoy_cluster_upstream_rq_total")
  if [[ "$PROM_RESP" == *'"__name__":"envoy_cluster_upstream_rq_total"'* ]]; then metrics_ok=yes; break; fi
  sleep 3
done
if [[ "$metrics_ok" == "yes" ]]; then
  echo "PASS  gateway metrics scraped by Prometheus"; pass=$((pass+1))
else
  echo "FAIL  no gateway metrics in Prometheus after 30s (check 'kubectl get pods -n monitoring' and the port-forward)"; fail=$((fail+1))
fi

traces_ok=no
for i in $(seq 1 10); do
  TEMPO_RESP=$(curl -s "http://127.0.0.1:13200/api/search?tags=service.name%3Dservice-a&limit=1")
  if [[ "$TEMPO_RESP" == *'"traceID"'* ]]; then traces_ok=yes; break; fi
  sleep 3
done
if [[ "$traces_ok" == "yes" ]]; then
  echo "PASS  service-a traces stored in Tempo"; pass=$((pass+1))
else
  echo "FAIL  no service-a traces in Tempo after 30s (check 'kubectl get pods -n monitoring' and the port-forward)"; fail=$((fail+1))
fi

echo ""
echo "demo: $pass passed, $fail failed"
[[ $fail -eq 0 ]]
