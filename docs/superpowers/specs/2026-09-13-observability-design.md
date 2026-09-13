# Observability: OpenTelemetry + self-contained Prometheus/Grafana

Date: 2026-09-13
Status: Approved (autopilot; user requested "enable observability including
OpenTelemetry and add a simple Grafana/Prometheus self-contained setup").
Depends on: `ingress-ratelimit` branch (stacked on `mtls-client-identity`).

## Goal

Make the demo observable end to end:

1. The two Envoy Gateway data planes (inbound + outbound) emit Prometheus
   metrics and OpenTelemetry traces.
2. The Go services (service-a/b/c/d, token-service) emit OpenTelemetry traces
   that join the gateway spans into one distributed trace per request
   (ingress → A → B → egress gateway → D, and ingress → C → D).
3. A self-contained, in-cluster stack — OTel Collector, Prometheus, Tempo,
   Grafana — lets you browse all of it with zero external dependencies.

Non-goals: OTel metrics/logs pipelines for the Go services, alerting,
persistent storage (everything is ephemeral demo infrastructure), access-log
shipping (proxy access logs stay on stdout).

## Architecture

```
gateway proxies ──(Prometheus scrape :19001/stats/prometheus)──► Prometheus ─┐
gateway proxies ──(OTLP gRPC traces)──► otel-collector ──► Tempo ◄───────────┤ Grafana
Go services ─────(OTLP gRPC traces)──► otel-collector ──► Tempo              │ (provisioned
                                                                             │  datasources)
```

All observability components live in a new `monitoring` namespace, deployed
as plain Kubernetes manifests under `deploy/k8s/observability/` (matching the
existing `deploy/k8s/` style — no third-party Helm charts, versions pinned by
image tag).

### Components

| Component | Image (pinned) | Role |
|---|---|---|
| otel-collector | `otel/opentelemetry-collector` | Single OTLP ingest point (gRPC :4317); batches and forwards traces to Tempo |
| Tempo | `grafana/tempo` | Trace storage (single binary, local `/tmp` storage) + query API :3200 |
| Prometheus | `prom/prometheus` | Scrapes gateway proxy pods via kubernetes_sd (needs RBAC: get/list/watch pods) |
| Grafana | `grafana/grafana` | Anonymous-admin UI; datasources (Prometheus, Tempo) and one gateway dashboard provisioned via ConfigMaps |

### Gateway telemetry (EnvoyProxy CRD)

A cluster-scoped attachment: `deploy/k8s/envoyproxy.yaml` defines an
`EnvoyProxy` resource (`proxy-config`, namespace `envoy-gateway-system`) with:

- `telemetry.metrics.prometheus: {}` — exposes `/stats/prometheus` on the
  proxy admin metrics port (19001).
- `telemetry.tracing`: `samplingRate: 100`, provider `type: OpenTelemetry`
  with `backendRefs` → Service `otel-collector` in `monitoring`, port 4317.

The existing `GatewayClass envoy` gains `spec.parametersRef` pointing at this
EnvoyProxy, so **both** gateways (inbound and outbound) inherit it. A
`ReferenceGrant` in `monitoring` permits the cross-namespace backendRef.

Verified against the v1.2.5 CRD schema (extracted from the official
`oci://docker.io/envoyproxy/gateway-helm` chart, which this repo already uses
per the official install-helm instructions): `telemetry.metrics.prometheus`,
`telemetry.tracing.provider.type ∈ {OpenTelemetry, Zipkin}` with
`backendRefs`, `samplingRate` default 100.

### Go service instrumentation

New package `internal/otelsetup`:

```go
// Init configures a global OTLP/gRPC TracerProvider and W3C propagation.
// When OTEL_EXPORTER_OTLP_ENDPOINT is unset it is a no-op, so local runs
// and unit tests need no collector.
func Init(ctx context.Context, serviceName string) (shutdown func(context.Context) error)
```

Wiring (auto-instrumentation only, no manual spans):

- HTTP servers (a, c, d, token-service): wrap handler in `otelhttp.NewHandler`.
- gRPC server (b): `grpc.StatsHandler(otelgrpc.NewServerHandler())`.
- gRPC client (a → b): `otelgrpc.NewClientHandler()`.
- Outbound HTTP clients (b/c → egress gateway → d): `otelhttp.NewTransport`.
- Propagation: W3C `tracecontext` + `baggage` — matches Envoy's OTel tracer,
  so gateway and service spans share one trace.

Deployment manifests gain
`OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector.monitoring.svc.cluster.local:4317`
(and `OTEL_SERVICE_NAME` via the Init argument, not env).

### Make / demo / docs

- `make deploy-observability` applies `deploy/k8s/observability/`; `make up`
  runs it **before** `bootstrap-gateway-controller` so the EnvoyProxy's
  tracing backend Service exists when the GatewayClass resolves its
  parametersRef.
- `bootstrap-gateway-controller` applies `envoyproxy.yaml` before
  `gatewayclass.yaml`.
- `make grafana` port-forwards Grafana to http://localhost:3000.
- Demo gains **step 8**: port-forward Prometheus and Tempo, assert (a) the
  Prometheus API returns non-empty results for an `envoy_` gateway metric and
  (b) Tempo's search API returns at least one trace containing
  `service.name=service-a`. Tolerance-based polling (telemetry pipelines are
  eventually consistent); steps 0–7 already generated the traffic.
- README: rationale paragraph, step-8 walkthrough, repo-layout entries,
  `make grafana` usage.

## Error handling

- Services: if the collector is unreachable, the OTLP exporter drops spans in
  the background; request handling is never affected. No endpoint = no-op.
- Demo step 8 polls with a deadline (~30 s) before failing, since spans are
  batched and Tempo ingestion is async.
- Removing `deploy/k8s/envoyproxy.yaml` + the parametersRef fully reverts the
  gateways to their previous untelemetered behavior (escape hatch).

## Testing

- `go build ./...`, `go test ./...` (otelsetup no-op path covered by existing
  handler tests running without the env var), `gofmt`, `go vet`.
- `helm template` for both charts (unchanged) still renders.
- `bash -n scripts/demo.sh`.
- Full runtime acceptance: `make up && make demo` on a fresh k3d cluster —
  all 9 steps green, including the new step 8.
