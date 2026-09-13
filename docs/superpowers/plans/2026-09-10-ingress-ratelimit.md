# Ingress Rate Limiting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rate-limit `/a` and `/c` on `inbound-gateway`, keyed by client-certificate CN (alice, bob get their own budget; everyone else shares a smaller default), using Envoy's Local rate-limit mode.

**Architecture:** A new `BackendTrafficPolicy` (Envoy Gateway CRD) targets the `route-a` and `route-c` HTTPRoutes. It defines Local rate-limit rules matching the `x-forwarded-client-cert` header by regex on CN (stable across cert regeneration, unlike matching the full header value which embeds a fingerprint that changes every rotation), plus a catch-all default rule. This depends on the mTLS multi-cert identity plan (`2026-09-10-mtls-client-identity.md`) having already wired `headers.xForwardedClientCert` into the `ClientTrafficPolicy`.

**Tech Stack:** Envoy Gateway v1.2.5 (`gateway.envoyproxy.io/v1alpha1` CRDs — `BackendTrafficPolicy`), Helm, bash + curl.

**Spec:** `docs/superpowers/specs/2026-09-10-ingress-ratelimit-design.md`

## Global Constraints

- Depends on the mTLS client-identity plan being implemented first (`docs/superpowers/plans/2026-09-10-mtls-client-identity.md`) — this plan assumes `inbound-gateway`'s `ClientTrafficPolicy` already forwards `x-forwarded-client-cert` and that `client-alice`/`client-bob` certs exist in `.certs/`.
- Envoy Gateway Helm chart pinned to `v1.2.5`; `BackendTrafficPolicy` is apiVersion `gateway.envoyproxy.io/v1alpha1`. Its rate-limit fields (verified against the actual v1.2.5 CRD Go types in `api/v1alpha1/ratelimit_types.go`): `spec.rateLimit.type` (`Global`|`Local`), `spec.rateLimit.local.rules[]`, each rule has `clientSelectors[].headers[]` (`name`, `type`: `Exact`|`RegularExpression`|`Distinct`, `value`) and `limit: {requests: uint, unit: Second|Minute|Hour|Day}`. A rule with no `clientSelectors` matches all traffic (used as the default/catch-all). `Distinct` header matching is Global-only — not used here.
- The per-CN-plus-default design depends on an Envoy Gateway v1.2.5 translator detail not visible in the CRD schema: the `clientSelectors`-less rule becomes the route-level default token bucket and the translator sets `always_consume_default_token_bucket: false`, so requests matching a per-CN rule do NOT also drain the default bucket. If that flag were true, alice/bob traffic would exhaust the 5/min default long before their own 10/min budgets.
- Envoy's Local rate-limit buckets refill continuously (`tokens_per_fill`/`fill_interval` — ~1 token every 6 s at 10/min), not on hard minute boundaries, and buckets are per-xDS-route (independent budgets on route-a and route-c). Demo assertions must not depend on exact request counts.
- Rate limiting is enforced entirely in the Envoy data plane (`BackendTrafficPolicy`); no Go code changes in this plan.
- Match by CN via regex against the XFCC header (e.g. `.*CN=alice.*`), never by exact header value — the header's `Hash=` portion changes on every `scripts/gen-certs.sh` regeneration, so exact matching would silently break after cert rotation.
- Setting `rateLimit.routes: []` in `deploy/charts/inbound-gateway/values.yaml` must fully disable the feature (template renders nothing) — preserve this escape hatch.
- Working dir for all commands: `/Users/luispessoa/development/envoy-experiment`.
- Commit messages end with trailer: `Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>`.

## File Structure

```
deploy/charts/inbound-gateway/values.yaml                        MODIFY  add rateLimit block (routes, perClient, defaultRequestsPerMinute)
deploy/charts/inbound-gateway/templates/backendtrafficpolicy.yaml CREATE  renders the BackendTrafficPolicy from rateLimit values
scripts/demo.sh                                                   MODIFY  add burst-and-429 step for client-bob
README.md                                                         MODIFY  document the rate-limit policy
```

---

### Task 1: Values schema for rate limiting

**Files:**
- Modify: `deploy/charts/inbound-gateway/values.yaml`

**Interfaces:**
- Consumes: nothing.
- Produces: `.Values.rateLimit.routes` (list of route names from the existing `routes` list), `.Values.rateLimit.perClient` (list of `{cn, requestsPerMinute}`), `.Values.rateLimit.defaultRequestsPerMinute` (int). Consumed by Task 2's template.

- [ ] **Step 1: Append the rate-limit values block**

Add to the end of `deploy/charts/inbound-gateway/values.yaml`:

```yaml
# Local (per-Envoy-instance) rate limiting, keyed by client-certificate CN
# (via the x-forwarded-client-cert header set by the ClientTrafficPolicy —
# see the mTLS multi-cert client identity feature). `routes` lists which
# route names (from the `routes` list above) this policy targets. Leave
# empty to disable rate limiting entirely.
rateLimit:
  routes:
    - route-a
    - route-c
  perClient:
    - cn: alice
      requestsPerMinute: 10
    - cn: bob
      requestsPerMinute: 10
  defaultRequestsPerMinute: 5
```

- [ ] **Step 2: Commit**

```bash
git add deploy/charts/inbound-gateway/values.yaml
git commit -m "feat: add rate-limit values schema to inbound-gateway chart

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

### Task 2: `BackendTrafficPolicy` template

**Files:**
- Create: `deploy/charts/inbound-gateway/templates/backendtrafficpolicy.yaml`

**Interfaces:**
- Consumes: `.Values.gatewayName`, `.Values.namespace` (existing), `.Values.rateLimit.routes`, `.Values.rateLimit.perClient`, `.Values.rateLimit.defaultRequestsPerMinute` from Task 1.
- Produces: a `BackendTrafficPolicy` resource named `{{ .Values.gatewayName }}-ratelimit`, rendered only when `.Values.rateLimit.routes` is non-empty.

- [ ] **Step 1: Create the template**

Create `deploy/charts/inbound-gateway/templates/backendtrafficpolicy.yaml`:

```yaml
{{- if .Values.rateLimit.routes }}
# Local rate limiting on the JWT-protected routes, keyed by client-cert CN
# (from the x-forwarded-client-cert header the ClientTrafficPolicy sets).
# Matches by regex on CN rather than exact header value because the
# header's Hash= portion changes every time scripts/gen-certs.sh
# regenerates certs. The final rule (no clientSelectors) is the catch-all
# default for any caller not explicitly listed in perClient.
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: {{ .Values.gatewayName }}-ratelimit
  namespace: {{ .Values.namespace }}
spec:
  targetRefs:
    {{- range .Values.rateLimit.routes }}
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: {{ $.Values.gatewayName }}-{{ . }}
    {{- end }}
  rateLimit:
    type: Local
    local:
      rules:
        {{- range .Values.rateLimit.perClient }}
        - clientSelectors:
            - headers:
                - type: RegularExpression
                  name: x-forwarded-client-cert
                  value: ".*CN={{ .cn }}.*"
          limit:
            requests: {{ .requestsPerMinute }}
            unit: Minute
        {{- end }}
        - limit:
            requests: {{ .Values.rateLimit.defaultRequestsPerMinute }}
            unit: Minute
{{- end }}
```

- [ ] **Step 2: Render the chart locally to verify templating**

```bash
helm template inbound-gateway deploy/charts/inbound-gateway | grep -A 30 "kind: BackendTrafficPolicy"
```
Expected: output shows `targetRefs` for `inbound-gateway-route-a` and
`inbound-gateway-route-c`, three rules under `local.rules` (alice 10/min,
bob 10/min, then the catch-all 5/min default with no `clientSelectors`),
no template errors.

- [ ] **Step 3: Verify the disable-by-empty-list escape hatch**

Note: `--set rateLimit.routes={}` does NOT produce an empty list in Helm —
it produces a one-element list `[""]` (a documented Helm `--set` parsing
quirk for list-typed values), which is truthy and renders one empty-named
route. Use `--set-json` or a values file to represent a real empty list:

```bash
helm template inbound-gateway deploy/charts/inbound-gateway --set-json 'rateLimit.routes=[]' | grep -c "kind: BackendTrafficPolicy"
```
Expected: `0` (the template renders nothing when `rateLimit.routes` is
empty).

- [ ] **Step 4: Commit**

```bash
git add deploy/charts/inbound-gateway/templates/backendtrafficpolicy.yaml
git commit -m "feat: add per-client-CN local rate limiting to inbound-gateway

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

### Task 3: Demo script — burst and 429

**Files:**
- Modify: `scripts/demo.sh`

**Interfaces:**
- Consumes: `client-bob.crt`/`.key` (from the mTLS client-identity plan), Task 2's `BackendTrafficPolicy` (bob: 10 req/min); requires a live cluster with both this plan's chart changes and the mTLS identity plan's chart changes deployed to actually pass.

- [ ] **Step 1: Add the burst step**

In `scripts/demo.sh`, insert after the alice/bob identity step (added by
the mTLS client-identity plan's Task 6) and before the final
`echo "demo: ..."` summary block:

```bash
echo "== 7. Rate limit enforced per client identity (bob: 10 req/min) =="
BOB_TLS=(--cacert "$CERTS/ca.crt" --cert "$CERTS/client-bob.crt" --key "$CERTS/client-bob.key" --resolve "inbound.local:${PORT}:127.0.0.1")
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
```

- [ ] **Step 2: Verify script syntax**

```bash
bash -n scripts/demo.sh
```
Expected: exit 0, no output (pure syntax check; running the full script
requires a live k3d cluster with both charts deployed — do that separately
with `make up && make demo`, or `make deploy-infra && make demo` on an
existing cluster, when available. Note: rate-limit windows are
per-minute, so re-running `make demo` within the same minute as a prior
run may see fewer than 10 successes if bob's earlier requests are still
inside the current window — this is expected Local rate-limit behavior,
not a bug).

- [ ] **Step 3: Commit**

```bash
git add scripts/demo.sh
git commit -m "test: demo per-client rate limit enforcement (429 on burst)

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

### Task 4: Documentation

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: none (documentation only).
- Produces: none (documentation only).

- [ ] **Step 1: Add a Rationale entry**

In `README.md`'s `## Rationale` section, add a new paragraph (after the
client-certificate-identity paragraph added by the mTLS client-identity
plan, or after the mTLS/JWT paragraph if that plan hasn't landed yet):

```markdown
**Rate limiting keys off client-certificate CN, matched by regex, in Local
mode.** `inbound-gateway`'s `BackendTrafficPolicy` rate-limits `/a` and
`/c`: alice and bob each get 10 requests/minute, matched against the
`x-forwarded-client-cert` header by a CN-only regex (`.*CN=<name>.*`)
rather than an exact value, because the header's `Hash=` fingerprint
changes every time `scripts/gen-certs.sh` regenerates certs — exact
matching would silently stop working after every rotation. Anyone else
(e.g. the original shared `demo-client` cert) falls through to a 5
requests/minute default rule. This uses Envoy Gateway's *Local* rate-limit
mode (in-memory counters per Envoy instance) rather than *Global*
(Redis-backed): Local is sufficient for a single-replica sandbox with a
small, known set of client identities, and avoids adding Redis plus the
rate-limit service to the cluster bootstrap. A caller who exceeds their
budget gets `429` at the gateway, before the request reaches service-a or
service-c.
```

- [ ] **Step 2: Update the Repo layout section**

In the `## Repo layout` code block, extend the existing
`deploy/charts/inbound-gateway/` line (or add one) to mention rate
limiting:

```
deploy/charts/inbound-gateway/   Helm chart: Gateway (HTTPS + mTLS) + HTTPRoutes (/auth, /a, /c) + JWT SecurityPolicy + ClientTrafficPolicy (mTLS + XFCC forwarding) + BackendTrafficPolicy (per-client-CN rate limiting)
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document per-client-CN rate limiting

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```
