# JWT-Protected Multi-Route Sandbox — Design

**Date:** 2026-09-09
**Status:** Approved for planning
**Project:** envoy-experiment (`/Users/luispessoa/development/envoy-experiment`)

## Goal

Extend the existing two-gateway Envoy sandbox into a demo of:

1. **Route-based dispatch at the ingress gateway** — one entry point, three
   HTTP routes (`/auth`, `/a`, `/c`) to three different backends.
2. **JWT validation at the gateway** — `/a` and `/c` require a valid RS256
   Bearer token; `/auth` is open and serves token minting + JWKS.
3. **Shared egress** — both service-b and the new service-c reach service-d
   only through the outbound gateway, using distinct gRPC methods that the
   egress `GRPCRoute` matches as separate rules.
4. **Demo-quality docs** — Mermaid diagrams, per-path curl samples, and a
   rationale section, plus a `make demo` target that doubles as the E2E test.

## Topology

```
                                    ┌── /auth/* ──> token-service :8080  (open — mints tokens, serves JWKS)
client ──HTTP/2──> [inbound-gateway]├── /a/*    ──> service-a :8080 ──gRPC Process──────> service-b ──┐
        (Bearer)    JWT on /a, /c   └── /c/*    ──> service-c :8080 ──gRPC ProcessDirect──────────────┤
                                                                                                      ▼
                                                        [outbound-gateway] ── GRPCRoute method rules:
                                                           Process       ──> service-d :9000
                                                           ProcessDirect ──> service-d :9000
```

- **Path A (long):** client → inbound gateway → service-a → service-b (direct
  gRPC) → outbound gateway (`Process`) → service-d.
- **Path C (short):** client → inbound gateway → service-c → outbound gateway
  (`ProcessDirect`) → service-d.
- Both egress calls dial the stable `outbound-gateway` Service and set gRPC
  `:authority = service-d.internal` so the egress `GRPCRoute` matches.

## Proto contract

`proto/chain/v1/chain.proto` gains one RPC (backward-compatible addition):

```proto
service ChainService {
  rpc Process(ChainRequest) returns (ChainResponse);        // existing: B → D
  rpc ProcessDirect(ChainRequest) returns (ChainResponse);  // new: C → D
}
```

- **service-d** implements both. `ProcessDirect` responds with
  `"hello from service-d (direct route), payload=..."` so responses prove
  which egress route ran. Both append `service-d` to `hops`.
- **service-b** keeps calling `Process`. **service-c** calls `ProcessDirect`.
- Regenerate with `make proto` (buf).

## New services

### service-c (`cmd/service-c`, `internal/servicec`)

Mirror of service-a's h2c HTTP/2 server, but the handler dials the outbound
gateway directly (same client pattern as service-b: insecure creds +
`grpc.WithAuthority(SERVICE_D_AUTHORITY)`) and invokes `ProcessDirect`.

- Env: `LISTEN_ADDR` (`:8080`), `OUTBOUND_GATEWAY_ADDR`
  (`outbound-gateway.envoy-gateway-system.svc.cluster.local:80`),
  `SERVICE_D_AUTHORITY` (`service-d.internal`).
- Response JSON identical in shape to service-a's:
  `{message, hops, received_protocol}`. Expected hops:
  `["service-c","service-d","service-c(response)"]`.

### token-service (`cmd/token-service`, `internal/tokenservice`)

Plain HTTP/1.1 Go service.

- **Startup:** generate a 2048-bit RSA keypair in memory; `kid` = truncated
  SHA-256 of the public key. No key material in the repo or in Secrets.
- **`POST /auth/token`** → `200 {"access_token","token_type":"Bearer","expires_in":3600}`.
  Claims: `iss=http://token-service.envoy-experiment.svc.cluster.local:8080`,
  `aud=["envoy-experiment"]`, `sub=demo-user`, `iat`, `exp` (+1h). Header
  carries `kid`, `alg=RS256`. Signing via `github.com/golang-jwt/jwt/v5`.
- **`GET /auth/jwks.json`** → `{"keys":[{"kty":"RSA","kid","use":"sig","alg":"RS256","n","e"}]}`.
  JWKS marshaling hand-rolled (base64url of n/e) — no extra dependency.
- Routes keep the `/auth` prefix end-to-end, so no URL rewriting anywhere.
- Env: `LISTEN_ADDR` (`:8080`), `ISSUER`, `AUDIENCE` (defaults as above).

## Gateway configuration changes

### Inbound chart (`deploy/charts/inbound-gateway`)

- `values.yaml` gains a `routes:` list; the single HTTPRoute template becomes
  a range over it:

  ```yaml
  routes:
    - name: route-auth
      pathPrefix: /auth
      backend: { name: token-service, port: 8080 }
      requireJWT: false
    - name: route-a
      pathPrefix: /a
      backend: { name: service-a, port: 8080 }
      requireJWT: true
    - name: route-c
      pathPrefix: /c
      backend: { name: service-c, port: 8080 }
      requireJWT: true
  jwt:
    issuer: http://token-service.envoy-experiment.svc.cluster.local:8080
    audience: envoy-experiment
    jwksURI: http://token-service.envoy-experiment.svc.cluster.local:8080/auth/jwks.json
  ```

- New template `securitypolicy.yaml`: one
  `gateway.envoyproxy.io/v1alpha1 SecurityPolicy` whose `targetRefs` list all
  routes with `requireJWT: true`. Untargeted routes (`/auth`) stay open.
- Behavior: missing/invalid/expired/tampered token → **401 from the gateway**;
  services never see the request.

### Outbound chart (`deploy/charts/outbound-gateway`)

`GRPCRoute` template changes from one catch-all rule to two explicit
method-match rules (values-driven):

```yaml
rules:
  - matches: [{ method: { service: chain.v1.ChainService, method: Process } }]
    backendRefs: [{ name: service-d, port: 9000 }]
  - matches: [{ method: { service: chain.v1.ChainService, method: ProcessDirect } }]
    backendRefs: [{ name: service-d, port: 9000 }]
```

Both target service-d today, but each egress path is now independently
visible and reconfigurable.

## Kubernetes manifests

- New `deploy/k8s/service-c.yaml` and `deploy/k8s/token-service.yaml`
  (Deployment + ClusterIP Service each, following the existing pattern;
  service-c's Service uses `appProtocol: kubernetes.io/h2c`).
- Existing manifests unchanged.

## Makefile

- `SERVICES := service-a service-b service-c service-d token-service`
  (Dockerfile is already parameterized by `--build-arg SERVICE=`).
- `make token` — port-forwards, mints a token, prints
  `export TOKEN=...`.
- `make demo` — unattended 5-step sequence with pass/fail per step
  (this is the acceptance test):
  1. `POST /auth/token` → 200, non-empty token.
  2. `GET /a/hello` without token → **401**.
  3. `POST /a/hello` with token → 200, hops
     `[service-a, service-b, service-d, service-a(response)]`.
  4. `POST /c/hello` with token → 200, hops
     `[service-c, service-d, service-c(response)]`, message contains
     "direct route".
  5. Tampered token on `/a/hello` → **401**.
  Exit non-zero on any mismatch.
- `make test` — kept as the quick smoke check, updated to mint a token first.

## Documentation (README)

- **Mermaid architecture flowchart** — styled; gateways and the JWT
  enforcement boundary visually distinct.
- **Two Mermaid sequence diagrams** — path A and path C full cycles,
  including token minting, the gateway's JWKS fetch, and a 401 branch.
- **Rationale section** — why two gateway instances (controller is a
  singleton; per-Gateway proxy Deployments are the "instances"), why egress
  via `:authority` routing, why per-route SecurityPolicy targeting, why
  in-memory keys (and the key-rotation side effect on restart), the
  stable-Service + named-targetPort workaround for Envoy Gateway's
  hash-suffixed proxy Services.
- **Demo walkthrough** — the five curl samples, each annotated with the hops
  it exercises.

## Failure modes (documented, accepted for a sandbox)

- JWKS unreachable at first protected request → 401 until the proxy's fetch
  succeeds (it retries).
- token-service restart → new keypair; previously minted tokens are rejected
  (doubles as a key-rotation demo).
- No refresh tokens, no scopes, no claims-based routing — deliberately out of
  scope (YAGNI).

## Acceptance criteria

1. `make up` from zero brings up cluster, controller, both gateways, all five
   services.
2. `make demo` passes all five steps.
3. `go build ./...`, `helm lint` on both charts pass.
4. `make down` removes everything.
