# mTLS Multi-Cert Client Identity — Design

## Context

`inbound-gateway` already terminates mutual TLS and requires every client to
present a certificate signed by the demo CA (`ClientTrafficPolicy`,
`inbound-gateway-mtls`), but every demo client currently shares one cert
(`.certs/client.crt`, CN=`demo-client`). The gateway has no way to
distinguish *which* client connected, and that information never reaches the
services behind it.

This spec adds per-client certificate identity: multiple distinct client
certs (same CA), extracted at the gateway and made available to the first
internal hop (service-a, service-c) via a header, without introducing a
second authentication path — JWT remains the sole authorization mechanism
on protected routes. The cert identifies *who*; the JWT still decides
*what they can do*.

## Goals

- Support multiple client certificates signed by the same demo CA, each
  identifying a distinct caller (by Subject CN).
- Have the gateway extract CN + certificate fingerprint (SHA-256 hash) and
  forward them to the first-hop service via a header.
- Surface the identity in the demo response JSON so it's visible via curl.
- Do not change the auth model: mTLS remains mandatory at the listener (as
  today), JWT remains required on `/a` and `/c`. Cert identity is additional
  context, not a new grant of access.

## Non-goals

- Propagating identity beyond the first hop (service-b, service-d, and the
  outbound-gateway chain are unaffected — this is deliberately scoped to
  `first_hop_only`; a later change can extend this if needed).
- Certificate revocation, per-client authorization rules, or using cert
  identity as a rate-limit key (tracked separately — see the rate-limiting
  spec).
- Replacing or duplicating JWT-based authorization.

## Design

### 1. Certificate generation (`scripts/gen-certs.sh`)

Extend the script to mint two additional named client certs off the same
demo CA, alongside the existing shared cert:

- `client-alice.crt` / `client-alice.key` — CN=`alice`
- `client-bob.crt` / `client-bob.key` — CN=`bob`

The existing `client.crt`/`client.key` (CN=`demo-client`) is unchanged and
kept for any other callers. The idempotency check (skip generation if certs
already present) extends to check for the new files too.

### 2. Gateway extraction (`ClientTrafficPolicy`)

Add `tls.clientCertificate.forwardClientCertDetails` to the existing
`inbound-gateway-mtls` `ClientTrafficPolicy`:

```yaml
tls:
  clientValidation:
    caCertificateRefs:
      - kind: Secret
        name: {{ .Values.tls.caSecretName }}
  clientCertificate:
    forwardClientCertDetails:
      mode: SanitizeSet      # discard any client-supplied XFCC; set our own
      details:
        - Subject            # DN, contains CN=<name>
        - Hash                # SHA-256 fingerprint of the leaf cert
```

`SanitizeSet` ensures no client can spoof the header themselves — Envoy
always overwrites it with the details of the cert it just validated during
the TLS handshake. This is a native Envoy Gateway feature; no Lua/wasm
extension is required. The result is a standard
`x-forwarded-client-cert` (XFCC) header on every request forwarded to
service-a/service-c, e.g.:

```
x-forwarded-client-cert: Hash=ab12...;Subject="CN=alice"
```

### 3. Parsing (`internal/certident`, new package)

A small, dependency-free package:

```go
package certident

// Parse extracts the Subject CN and certificate hash (fingerprint) from a
// standard XFCC header value as produced by Envoy's forwardClientCertDetails
// (SanitizeSet mode, details: [Subject, Hash]). Returns ok=false if the
// header is empty or doesn't contain a parseable Subject/CN.
func Parse(xfcc string) (cn, fingerprint string, ok bool)
```

Implementation: split the XFCC value on `;`, extract the `Hash=` and
`Subject=` fields, then extract `CN=` out of the Subject DN string. Include
unit tests covering: well-formed header, missing header, missing CN in
Subject, multiple DN attributes (e.g. `Subject="CN=alice,O=demo"`).

### 4. Handler changes (service-a, service-c)

Both handlers read `r.Header.Get("x-forwarded-client-cert")`, call
`certident.Parse`, and — when `ok` — add a `client` field to the existing
JSON response:

```json
{
  "message": "...",
  "hops": ["service-a", "service-b", "service-d", "service-a(response)"],
  "received_protocol": "HTTP/2.0",
  "client": {"cn": "alice", "fingerprint": "ab12..."}
}
```

When the header is missing or unparsable, `client` is simply omitted (not
an error) — this keeps behavior for any caller without a distinguishable
cert unchanged, and keeps `/auth` (token-service, not touched by this
change) unaffected.

`chainResult` in both `internal/servicea/server.go` and
`internal/servicec/server.go` gains an `omitempty` `Client *clientIdentity`
field populated from the parsed header.

### 5. Demo (`scripts/demo.sh`)

Add a new demo step after the existing path-A/path-C steps: call `/a/hello`
once presenting `client-alice.crt`/`client-alice.key` and once presenting
`client-bob.crt`/`client-bob.key`, asserting each response's `client.cn`
matches the cert used (`alice` / `bob` respectively). Existing steps keep
using the original shared `client.crt` unchanged.

## Testing

- Unit tests for `internal/certident.Parse` (table-driven, covering the
  cases above).
- `scripts/demo.sh` extended assertion step (acts as the E2E test for this
  feature, consistent with how the rest of the repo is tested).
- No changes needed to `internal/servicea` / `internal/servicec` existing
  tests' assertions beyond adding coverage for the new `client` field
  presence/absence.

## Rollout / compatibility

Purely additive: existing routes, existing JWT policy, and existing
callers using the shared `client.crt` continue to work unchanged (they'll
just see `client.cn: "demo-client"` in responses from `/a` and `/c`, since
that cert also flows through the same `forwardClientCertDetails` path).
