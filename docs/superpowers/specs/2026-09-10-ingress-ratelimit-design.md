# Ingress Rate Limiting (per Client Certificate) — Design

## Context

This builds on the mTLS multi-cert client identity spec
(`2026-09-10-mtls-client-identity-design.md`), which makes the gateway
forward each caller's certificate `Subject` (CN) and `Hash` (fingerprint)
to service-a/service-c via the standard `x-forwarded-client-cert` (XFCC)
header. This spec adds rate limiting on `inbound-gateway`, keyed by that
same client-certificate identity, so distinct callers (alice, bob) get
independent budgets rather than sharing one bucket per route.

## Goals

- Rate-limit the JWT-protected routes (`/a`, `/c`) at the gateway, before
  requests reach service-a/service-c.
- Give named client identities (alice, bob) their own budget; anyone else
  (e.g. the original shared `demo-client` cert) shares a smaller default
  budget.
- No new infrastructure dependency (no Redis) — appropriate for this
  single-replica demo sandbox.
- Demonstrate enforcement end-to-end via `scripts/demo.sh` (a request past
  the limit gets `429`).

## Non-goals

- True global (cross-replica) rate limiting — this sandbox runs one
  gateway replica, so Local mode's per-instance counters are sufficient.
  If the project ever moves to multiple replicas, this should be revisited
  (see Alternatives).
- Rate limiting `/auth` (token minting) — out of scope for this spec.
- Rate limiting based on JWT claims — out of scope; the key is the client
  certificate identity established in the previous spec.

## Design

### Why Local mode, and why regex (not exact) header matching

Envoy Gateway's `BackendTrafficPolicy` supports two rate-limit modes:

- **Global** — shared counters via an external Redis-backed rate-limit
  service; supports dynamic per-value ("Distinct") bucketing, i.e. limits
  automatically split per unique header value without enumerating them.
- **Local** — per-Envoy-instance in-memory counters; no extra
  infrastructure, but does **not** support dynamic per-value bucketing —
  only statically enumerated `clientSelectors` rules.

Since this project has a small, known set of demo identities (alice, bob,
plus a default bucket), Local mode's static enumeration is sufficient and
avoids adding Redis + the rate-limit service to the cluster bootstrap.

The `x-forwarded-client-cert` header value is `Hash=<fingerprint>;Subject="CN=<name>"`.
The `Hash` portion changes every time `scripts/gen-certs.sh` regenerates
certs (new keypair), so matching the header by **exact** value would break
after every cert rotation. Instead, rules match with `type:
RegularExpression` against a CN-only pattern (e.g. `.*CN=alice.*`), which
stays stable across cert regeneration as long as the CN itself doesn't
change.

### 1. BackendTrafficPolicy (new chart template)

`deploy/charts/inbound-gateway/templates/backendtrafficpolicy.yaml`,
rendered only when `.Values.rateLimit.routes` is non-empty:

```yaml
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
```

The final rule has no `clientSelectors`, so it acts as the catch-all
default for any request that didn't match a named client rule (missing
XFCC header, or a cert CN not explicitly listed — e.g. the original shared
`demo-client` cert).

### 2. Values (`deploy/charts/inbound-gateway/values.yaml`)

```yaml
# Local (per-Envoy-instance) rate limiting, keyed by client-certificate CN
# (via the x-forwarded-client-cert header set by the ClientTrafficPolicy).
# `routes` lists which route names (from `routes` above) this policy
# targets. Leave empty to disable rate limiting entirely.
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

### 3. Demo (`scripts/demo.sh`)

New step, placed after the spec-1 alice/bob identity step: burst requests
to `/a/hello` back-to-back using the `client-bob` cert until a `429` is
observed (bounded at 15 attempts), asserting the limiter engages — more
than 5 successes proves bob drew from his own 10/min bucket rather than
the shared 5/min default, and the eventual `429` proves enforcement. An
exact "first 10 succeed, 11th fails" count is deliberately NOT asserted:
the spec-1 identity step already spends one of bob's tokens on the same
route, and Envoy's Local bucket refills continuously (~1 token every 6 s
at 10/min) rather than resetting on a minute boundary, so exact counts
drift by a token or two with wall-clock timing. This reuses the
`client-bob.crt`/`client-bob.key` pair added by the previous spec — no new
PKI changes needed here.

## Testing

- `scripts/demo.sh`'s new burst-and-429 step is the primary end-to-end
  test for this feature, consistent with how JWT/mTLS behavior is already
  verified in this repo.
- No unit tests are needed — the rate-limit enforcement lives entirely in
  Envoy's data plane (`BackendTrafficPolicy`), not in Go code.

## Alternatives considered

- **Global (Redis-backed) rate limiting** — would give true cross-replica
  limits and native per-distinct-value bucketing (no need to enumerate
  alice/bob by name), but requires deploying Redis and enabling Envoy
  Gateway's rate-limit service cluster-wide. Rejected for now given the
  single-replica sandbox; worth revisiting if the project grows multiple
  gateway replicas or an unbounded set of client identities.

## Rollout / compatibility

Purely additive: existing routes and policies are unaffected except that
`/a` and `/c` now enforce a request budget. Setting `rateLimit.routes: []`
in values.yaml disables the feature entirely (template renders nothing).
